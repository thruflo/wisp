package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStreamClient(t *testing.T) {
	t.Parallel()

	t.Run("creates client with defaults", func(t *testing.T) {
		t.Parallel()

		client := NewStreamClient("http://localhost:8374")
		assert.Equal(t, "http://localhost:8374", client.BaseURL())
		assert.Equal(t, 5*time.Second, client.reconnectInterval)
		assert.Equal(t, 0, client.maxReconnectAttempts)
	})

	t.Run("trims trailing slash from URL", func(t *testing.T) {
		t.Parallel()

		client := NewStreamClient("http://localhost:8374/")
		assert.Equal(t, "http://localhost:8374", client.BaseURL())
	})

	t.Run("applies options", func(t *testing.T) {
		t.Parallel()

		customClient := &http.Client{Timeout: 10 * time.Second}
		client := NewStreamClient(
			"http://localhost:8374",
			WithAuthToken("test-token"),
			WithHTTPClient(customClient),
			WithReconnectInterval(2*time.Second),
			WithMaxReconnectAttempts(5),
		)

		assert.Equal(t, "test-token", client.authToken)
		assert.Equal(t, customClient, client.httpClient)
		assert.Equal(t, 2*time.Second, client.reconnectInterval)
		assert.Equal(t, 5, client.maxReconnectAttempts)
	})
}

func TestStreamClientConnect(t *testing.T) {
	t.Parallel()

	t.Run("succeeds when server returns OK", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/state", r.URL.Path)
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(StateSnapshot{LastSeq: 10})
		}))
		defer server.Close()

		client := NewStreamClient(server.URL)
		err := client.Connect(context.Background())
		require.NoError(t, err)
	})

	t.Run("fails when server returns error", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("server error"))
		}))
		defer server.Close()

		client := NewStreamClient(server.URL)
		err := client.Connect(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "500")
	})

	t.Run("fails when server not reachable", func(t *testing.T) {
		t.Parallel()

		client := NewStreamClient("http://localhost:59999")
		err := client.Connect(context.Background())
		require.Error(t, err)
	})

	t.Run("includes auth token in request", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(StateSnapshot{})
		}))
		defer server.Close()

		client := NewStreamClient(server.URL, WithAuthToken("test-token"))
		err := client.Connect(context.Background())
		require.NoError(t, err)
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(5 * time.Second)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		client := NewStreamClient(server.URL)
		err := client.Connect(ctx)
		require.Error(t, err)
	})
}

func TestStreamClientSubscribe(t *testing.T) {
	t.Parallel()

	t.Run("receives events via SSE", func(t *testing.T) {
		t.Parallel()

		events := []*Event{
			MustNewEvent(MessageTypeSession, "session:sess-1", Session{ID: "sess-1"}),
			MustNewEvent(MessageTypeTask, "task:task-1", Task{ID: "task-1"}),
			MustNewEvent(MessageTypeClaudeEvent, "claude_event:claude-1", ClaudeEvent{ID: "claude-1"}),
		}
		// Assign sequence numbers
		for i, e := range events {
			e.Seq = uint64(i + 1)
		}

		// Channel to signal when to close the server connection
		done := make(chan struct{})

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/stream" {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.WriteHeader(http.StatusOK)

				flusher, ok := w.(http.Flusher)
				require.True(t, ok)

				for _, event := range events {
					data, _ := event.Marshal()
					fmt.Fprintf(w, "data: %s\n\n", string(data))
					flusher.Flush()
				}

				// Keep connection open until test is done
				<-done
			}
		}))
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		client := NewStreamClient(server.URL)
		eventCh, errCh := client.Subscribe(ctx, 0)

		var received []*Event
		for len(received) < 3 {
			select {
			case event, ok := <-eventCh:
				if !ok {
					t.Fatal("event channel closed unexpectedly")
				}
				require.NotNil(t, event)
				received = append(received, event)
			case err, ok := <-errCh:
				if ok && err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("timeout waiting for events")
			}
		}

		// Signal server to close
		close(done)

		assert.Len(t, received, 3)
		assert.Equal(t, MessageTypeSession, received[0].Type)
		assert.Equal(t, MessageTypeTask, received[1].Type)
		assert.Equal(t, MessageTypeClaudeEvent, received[2].Type)
	})

	t.Run("updates lastSeq as events are received", func(t *testing.T) {
		t.Parallel()

		event := MustNewEvent(MessageTypeSession, "session:sess-1", Session{ID: "sess-1"})
		event.Seq = 42

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/stream" {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				data, _ := event.Marshal()
				fmt.Fprintf(w, "data: %s\n\n", string(data))
			}
		}))
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		client := NewStreamClient(server.URL)
		assert.Equal(t, uint64(0), client.LastSeq())

		eventCh, errCh := client.Subscribe(ctx, 0)

		select {
		case <-eventCh:
		case err := <-errCh:
			t.Fatalf("unexpected error: %v", err)
		case <-ctx.Done():
			t.Fatal("timeout")
		}

		assert.Equal(t, uint64(42), client.LastSeq())
	})

	t.Run("passes fromSeq parameter to server", func(t *testing.T) {
		t.Parallel()

		var receivedFromSeq string

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/stream" {
				receivedFromSeq = r.URL.Query().Get("from_seq")
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				// Send one event and close
				event := MustNewEvent(MessageTypeSession, "session:sess-1", Session{ID: "sess-1"})
				event.Seq = 10
				data, _ := event.Marshal()
				fmt.Fprintf(w, "data: %s\n\n", string(data))
			}
		}))
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		client := NewStreamClient(server.URL)
		eventCh, _ := client.Subscribe(ctx, 5)

		// Wait for first event
		select {
		case <-eventCh:
		case <-ctx.Done():
			t.Fatal("timeout")
		}

		assert.Equal(t, "5", receivedFromSeq)
	})

	t.Run("closes channel when context is canceled", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/stream" {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				// Keep connection open
				<-r.Context().Done()
			}
		}))
		defer server.Close()

		ctx, cancel := context.WithCancel(context.Background())

		client := NewStreamClient(server.URL)
		eventCh, errCh := client.Subscribe(ctx, 0)

		// Cancel context
		cancel()

		// Wait for channels to close
		select {
		case _, ok := <-eventCh:
			if ok {
				// Drain any remaining events
				for range eventCh {
				}
			}
		case <-time.After(2 * time.Second):
			t.Fatal("event channel was not closed")
		}

		select {
		case _, ok := <-errCh:
			assert.False(t, ok, "error channel should be closed")
		case <-time.After(2 * time.Second):
			t.Fatal("error channel was not closed")
		}
	})

	t.Run("reconnects after connection failure", func(t *testing.T) {
		t.Parallel()

		var requestCount int
		var mu sync.Mutex

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/stream" {
				mu.Lock()
				requestCount++
				count := requestCount
				mu.Unlock()

				if count == 1 {
					// First request: fail immediately
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}

				// Second request: succeed
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)

				event := MustNewEvent(MessageTypeSession, "session:sess-1", Session{ID: "sess-1"})
				event.Seq = 1
				data, _ := event.Marshal()
				fmt.Fprintf(w, "data: %s\n\n", string(data))
			}
		}))
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		client := NewStreamClient(server.URL, WithReconnectInterval(100*time.Millisecond))
		eventCh, errCh := client.Subscribe(ctx, 0)

		// Should eventually receive event after reconnect
		select {
		case event := <-eventCh:
			require.NotNil(t, event)
			assert.Equal(t, MessageTypeSession, event.Type)
		case err := <-errCh:
			t.Fatalf("unexpected error: %v", err)
		case <-ctx.Done():
			t.Fatal("timeout waiting for event")
		}

		mu.Lock()
		assert.GreaterOrEqual(t, requestCount, 2)
		mu.Unlock()
	})

	t.Run("gives up after max reconnect attempts", func(t *testing.T) {
		t.Parallel()

		var requestCount int
		var mu sync.Mutex

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/stream" {
				mu.Lock()
				requestCount++
				mu.Unlock()
				// Always fail
				w.WriteHeader(http.StatusServiceUnavailable)
			}
		}))
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		client := NewStreamClient(
			server.URL,
			WithReconnectInterval(50*time.Millisecond),
			WithMaxReconnectAttempts(3),
		)
		eventCh, errCh := client.Subscribe(ctx, 0)

		// Should receive error after max attempts
		select {
		case <-eventCh:
			// Channel should eventually close
		case err := <-errCh:
			require.Error(t, err)
			assert.Contains(t, err.Error(), "max reconnection attempts")
		case <-ctx.Done():
			t.Fatal("timeout waiting for error")
		}

		// Drain event channel
		for range eventCh {
		}

		mu.Lock()
		assert.Equal(t, 3, requestCount)
		mu.Unlock()
	})
}

func TestStreamClientSendCommand(t *testing.T) {
	t.Parallel()

	t.Run("sends command and receives ack", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/command" {
				assert.Equal(t, "POST", r.Method)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

				// Parse the incoming event
				var event Event
				require.NoError(t, json.NewDecoder(r.Body).Decode(&event))
				assert.Equal(t, MessageTypeCommand, event.Type)

				cmd, err := event.CommandData()
				require.NoError(t, err)
				assert.Equal(t, "cmd-123", cmd.ID)
				assert.Equal(t, CommandTypeKill, cmd.Type)

				// Return ack
				ack := NewSuccessAck("cmd-123")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(ack)
			}
		}))
		defer server.Close()

		client := NewStreamClient(server.URL)
		cmd, err := NewKillCommand("cmd-123", false)
		require.NoError(t, err)

		ack, err := client.SendCommand(context.Background(), cmd)
		require.NoError(t, err)
		assert.Equal(t, "cmd-123", ack.CommandID)
		assert.Equal(t, AckStatusSuccess, ack.Status)
	})

	t.Run("handles error response", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/command" {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte("invalid command"))
			}
		}))
		defer server.Close()

		client := NewStreamClient(server.URL)
		cmd := NewBackgroundCommand("cmd-456")

		ack, err := client.SendCommand(context.Background(), cmd)
		require.Error(t, err)
		assert.Nil(t, ack)
		assert.Contains(t, err.Error(), "400")
	})

	t.Run("includes auth token", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/command" {
				assert.Equal(t, "Bearer secret-token", r.Header.Get("Authorization"))
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(NewSuccessAck("cmd-1"))
			}
		}))
		defer server.Close()

		client := NewStreamClient(server.URL, WithAuthToken("secret-token"))
		cmd := NewBackgroundCommand("cmd-1")

		_, err := client.SendCommand(context.Background(), cmd)
		require.NoError(t, err)
	})
}

func TestStreamClientSendKillCommand(t *testing.T) {
	t.Parallel()

	t.Run("sends kill command with delete sprite", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var event Event
			json.NewDecoder(r.Body).Decode(&event)
			cmd, _ := event.CommandData()
			payload, _ := cmd.KillPayloadData()

			assert.Equal(t, CommandTypeKill, cmd.Type)
			assert.True(t, payload.DeleteSprite)

			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(NewSuccessAck(cmd.ID))
		}))
		defer server.Close()

		client := NewStreamClient(server.URL)
		ack, err := client.SendKillCommand(context.Background(), "kill-1", true)
		require.NoError(t, err)
		assert.Equal(t, AckStatusSuccess, ack.Status)
	})
}

func TestStreamClientSendBackgroundCommand(t *testing.T) {
	t.Parallel()

	t.Run("sends background command", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var event Event
			json.NewDecoder(r.Body).Decode(&event)
			cmd, _ := event.CommandData()

			assert.Equal(t, CommandTypeBackground, cmd.Type)
			assert.Equal(t, "bg-1", cmd.ID)

			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(NewSuccessAck(cmd.ID))
		}))
		defer server.Close()

		client := NewStreamClient(server.URL)
		ack, err := client.SendBackgroundCommand(context.Background(), "bg-1")
		require.NoError(t, err)
		assert.Equal(t, AckStatusSuccess, ack.Status)
	})
}

func TestStreamClientSendInputResponse(t *testing.T) {
	t.Parallel()

	t.Run("sends input response", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Input response is sent to /input endpoint, not /command
			assert.Equal(t, "/input", r.URL.Path)
			assert.Equal(t, "POST", r.Method)

			var event Event
			json.NewDecoder(r.Body).Decode(&event)

			// Input response is now its own event type, not a command
			assert.Equal(t, MessageTypeInputResponse, event.Type)

			ir, err := event.InputResponseData()
			require.NoError(t, err)
			assert.Equal(t, "resp-1", ir.ID)
			assert.Equal(t, "req-123", ir.RequestID)
			assert.Equal(t, "user's response", ir.Response)

			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(NewSuccessAck(ir.ID))
		}))
		defer server.Close()

		client := NewStreamClient(server.URL)
		ack, err := client.SendInputResponse(context.Background(), "resp-1", "req-123", "user's response")
		require.NoError(t, err)
		assert.Equal(t, AckStatusSuccess, ack.Status)
	})
}

func TestStreamClientGetState(t *testing.T) {
	t.Parallel()

	t.Run("fetches state snapshot", func(t *testing.T) {
		t.Parallel()

		snapshot := StateSnapshot{
			Session: &SessionEvent{
				ID:        "sess-1",
				Repo:      "owner/repo",
				Branch:    "main",
				Status:    SessionStatusRunning,
				Iteration: 5,
			},
			Tasks: []*TaskEvent{
				{ID: "task-1", Status: TaskStatusCompleted},
				{ID: "task-2", Status: TaskStatusInProgress},
			},
			LastSeq: 42,
			InputRequest: &InputRequestEvent{
				ID:       "input-1",
				Question: "What should I do?",
			},
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/state", r.URL.Path)
			assert.Equal(t, "GET", r.Method)
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(snapshot)
		}))
		defer server.Close()

		client := NewStreamClient(server.URL)
		state, err := client.GetState(context.Background())
		require.NoError(t, err)

		assert.Equal(t, "sess-1", state.Session.ID)
		assert.Equal(t, SessionStatusRunning, state.Session.Status)
		assert.Len(t, state.Tasks, 2)
		assert.Equal(t, uint64(42), state.LastSeq)
		assert.Equal(t, "What should I do?", state.InputRequest.Question)
	})

	t.Run("handles error response", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("session not found"))
		}))
		defer server.Close()

		client := NewStreamClient(server.URL)
		state, err := client.GetState(context.Background())
		require.Error(t, err)
		assert.Nil(t, state)
		assert.Contains(t, err.Error(), "404")
	})
}

func TestStreamClientConcurrency(t *testing.T) {
	t.Parallel()

	t.Run("concurrent command sends are safe", func(t *testing.T) {
		t.Parallel()

		var mu sync.Mutex
		receivedCommands := make(map[string]bool)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var event Event
			json.NewDecoder(r.Body).Decode(&event)
			cmd, _ := event.CommandData()

			mu.Lock()
			receivedCommands[cmd.ID] = true
			mu.Unlock()

			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(NewSuccessAck(cmd.ID))
		}))
		defer server.Close()

		client := NewStreamClient(server.URL)

		var wg sync.WaitGroup
		const numCommands = 50

		for i := 0; i < numCommands; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				cmdID := fmt.Sprintf("cmd-%d", id)
				cmd := NewBackgroundCommand(cmdID)
				_, err := client.SendCommand(context.Background(), cmd)
				assert.NoError(t, err)
			}(i)
		}

		wg.Wait()

		mu.Lock()
		assert.Len(t, receivedCommands, numCommands)
		mu.Unlock()
	})
}

func TestSSEParsing(t *testing.T) {
	t.Parallel()

	t.Run("handles data without space after colon", func(t *testing.T) {
		t.Parallel()

		event := MustNewEvent(MessageTypeSession, "session:sess-1", Session{ID: "sess-1"})
		event.Seq = 1

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			// Note: no space after "data:"
			data, _ := event.Marshal()
			fmt.Fprintf(w, "data:%s\n\n", string(data))
		}))
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		client := NewStreamClient(server.URL)
		eventCh, _ := client.Subscribe(ctx, 0)

		select {
		case received := <-eventCh:
			require.NotNil(t, received)
			assert.Equal(t, uint64(1), received.Seq)
		case <-ctx.Done():
			t.Fatal("timeout")
		}
	})

	t.Run("skips malformed events", func(t *testing.T) {
		t.Parallel()

		validEvent := MustNewEvent(MessageTypeSession, "session:sess-1", Session{ID: "sess-1"})
		validEvent.Seq = 1

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher := w.(http.Flusher)

			// Send malformed event
			fmt.Fprintf(w, "data: {invalid json}\n\n")
			flusher.Flush()

			// Send valid event
			data, _ := validEvent.Marshal()
			fmt.Fprintf(w, "data: %s\n\n", string(data))
			flusher.Flush()
		}))
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		client := NewStreamClient(server.URL)
		eventCh, _ := client.Subscribe(ctx, 0)

		// Should receive the valid event
		select {
		case received := <-eventCh:
			require.NotNil(t, received)
			assert.Equal(t, uint64(1), received.Seq)
		case <-ctx.Done():
			t.Fatal("timeout")
		}
	})

	t.Run("ignores other SSE fields", func(t *testing.T) {
		t.Parallel()

		event := MustNewEvent(MessageTypeSession, "session:sess-1", Session{ID: "sess-1"})
		event.Seq = 1

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			data, _ := event.Marshal()
			// Include other SSE fields
			fmt.Fprintf(w, "event: message\n")
			fmt.Fprintf(w, "id: 123\n")
			fmt.Fprintf(w, "retry: 5000\n")
			fmt.Fprintf(w, "data: %s\n\n", string(data))
		}))
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		client := NewStreamClient(server.URL)
		eventCh, _ := client.Subscribe(ctx, 0)

		select {
		case received := <-eventCh:
			require.NotNil(t, received)
			assert.Equal(t, uint64(1), received.Seq)
		case <-ctx.Done():
			t.Fatal("timeout")
		}
	})
}
