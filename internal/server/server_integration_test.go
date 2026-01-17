//go:build integration

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thruflo/wisp/internal/auth"
)

// Integration tests for the web server.
// Run with: go test -tags=integration ./internal/server/...

// TestIntegrationAuthFlow tests the complete authentication flow including
// correct password, wrong password, and token-based access to protected endpoints.
func TestIntegrationAuthFlow(t *testing.T) {
	t.Parallel()

	// Create server with known password
	hash, err := auth.HashPassword(testPassword)
	require.NoError(t, err)

	server, err := NewServer(&Config{
		Port:         0,
		PasswordHash: hash,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start server
	go func() {
		server.Start(ctx)
	}()
	time.Sleep(100 * time.Millisecond)
	defer server.Stop()

	addr := server.ListenAddr()
	require.NotEmpty(t, addr)

	t.Run("correct_password_returns_valid_token", func(t *testing.T) {
		// Authenticate with correct password
		form := url.Values{"password": {testPassword}}
		resp, err := http.PostForm("http://"+addr+"/auth", form)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var authResp struct {
			Token string `json:"token"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&authResp))
		assert.NotEmpty(t, authResp.Token)
		assert.Len(t, authResp.Token, 64) // 32 bytes hex encoded

		// Token should work for protected endpoints
		req, _ := http.NewRequest("GET", "http://"+addr+"/stream", nil)
		req.Header.Set("Authorization", "Bearer "+authResp.Token)
		streamResp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer streamResp.Body.Close()

		assert.Equal(t, http.StatusOK, streamResp.StatusCode)
	})

	t.Run("incorrect_password_returns_401", func(t *testing.T) {
		form := url.Values{"password": {"wrong-password"}}
		resp, err := http.PostForm("http://"+addr+"/auth", form)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("empty_password_returns_400", func(t *testing.T) {
		form := url.Values{"password": {""}}
		resp, err := http.PostForm("http://"+addr+"/auth", form)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("invalid_token_returns_401", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "http://"+addr+"/stream", nil)
		req.Header.Set("Authorization", "Bearer invalid-token-12345")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("no_auth_header_returns_401", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "http://"+addr+"/stream", nil)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("multiple_tokens_all_valid", func(t *testing.T) {
		// Get multiple tokens
		var tokens []string
		for i := 0; i < 3; i++ {
			form := url.Values{"password": {testPassword}}
			resp, err := http.PostForm("http://"+addr+"/auth", form)
			require.NoError(t, err)

			var authResp struct {
				Token string `json:"token"`
			}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&authResp))
			resp.Body.Close()
			tokens = append(tokens, authResp.Token)
		}

		// All tokens should be unique
		assert.NotEqual(t, tokens[0], tokens[1])
		assert.NotEqual(t, tokens[1], tokens[2])

		// All tokens should work
		for _, token := range tokens {
			req, _ := http.NewRequest("GET", "http://"+addr+"/stream", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			resp.Body.Close()
		}
	})
}

// TestIntegrationStateSyncOnConnect tests that clients receive current state
// when connecting to the stream endpoint.
func TestIntegrationStateSyncOnConnect(t *testing.T) {
	t.Parallel()

	server := createTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	go func() {
		server.Start(ctx)
	}()
	time.Sleep(100 * time.Millisecond)
	defer server.Stop()

	addr := server.ListenAddr()
	token := getAuthToken(t, addr)

	// Broadcast state before client connects
	session := &Session{
		ID:        "test-session-1",
		Repo:      "owner/repo",
		Branch:    "wisp/feature-x",
		Spec:      "docs/rfc.md",
		Status:    SessionStatusRunning,
		Iteration: 5,
		StartedAt: "2024-01-15T10:00:00Z",
	}
	require.NoError(t, server.Streams().BroadcastSession(session))

	tasks := []*Task{
		{ID: "task-1", SessionID: "test-session-1", Order: 0, Content: "Setup project", Status: TaskStatusCompleted},
		{ID: "task-2", SessionID: "test-session-1", Order: 1, Content: "Implement feature", Status: TaskStatusInProgress},
		{ID: "task-3", SessionID: "test-session-1", Order: 2, Content: "Write tests", Status: TaskStatusPending},
	}
	for _, task := range tasks {
		require.NoError(t, server.Streams().BroadcastTask(task))
	}

	inputReq := &InputRequest{
		ID:        "input-1",
		SessionID: "test-session-1",
		Iteration: 5,
		Question:  "How should I proceed?",
		Responded: false,
		Response:  nil,
	}
	require.NoError(t, server.Streams().BroadcastInputRequest(inputReq))

	// Client connects and should receive all state from beginning
	t.Run("new_client_receives_all_state", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "http://"+addr+"/stream", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.NotEmpty(t, resp.Header.Get("Stream-Next-Offset"))
		// Note: Stream-Up-To-Date may or may not be set depending on whether
		// we've caught up with the tail. The important thing is we get all messages.

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var messages []StreamMessage
		require.NoError(t, json.Unmarshal(body, &messages))

		// Should have 5 messages: 1 session + 3 tasks + 1 input request
		assert.Len(t, messages, 5)

		// Verify session
		sessionMsg := messages[0]
		assert.Equal(t, MessageTypeSession, sessionMsg.Type)

		// Verify tasks
		for i := 1; i <= 3; i++ {
			assert.Equal(t, MessageTypeTask, messages[i].Type)
		}

		// Verify input request
		assert.Equal(t, MessageTypeInputRequest, messages[4].Type)
	})

	t.Run("client_with_offset_receives_only_new_messages", func(t *testing.T) {
		// First request to get current offset
		req, _ := http.NewRequest("GET", "http://"+addr+"/stream?offset=now", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		offset := resp.Header.Get("Stream-Next-Offset")
		resp.Body.Close()

		// Broadcast new message
		newTask := &Task{
			ID:        "task-4",
			SessionID: "test-session-1",
			Order:     3,
			Content:   "Deploy",
			Status:    TaskStatusPending,
		}
		require.NoError(t, server.Streams().BroadcastTask(newTask))

		// Request with offset should only get new message
		req, _ = http.NewRequest("GET", "http://"+addr+"/stream?offset="+offset, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err = http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var messages []StreamMessage
		require.NoError(t, json.Unmarshal(body, &messages))

		assert.Len(t, messages, 1)
		assert.Equal(t, MessageTypeTask, messages[0].Type)
	})
}

// TestIntegrationRealTimeUpdatesViaStream tests that updates are received
// in real-time through the stream endpoint using long-polling.
func TestIntegrationRealTimeUpdatesViaStream(t *testing.T) {
	t.Parallel()

	server := createTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	go func() {
		server.Start(ctx)
	}()
	time.Sleep(100 * time.Millisecond)
	defer server.Stop()

	addr := server.ListenAddr()
	token := getAuthToken(t, addr)

	t.Run("long_poll_receives_updates", func(t *testing.T) {
		// Get current offset
		req, _ := http.NewRequest("GET", "http://"+addr+"/stream?offset=now", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		offset := resp.Header.Get("Stream-Next-Offset")
		resp.Body.Close()

		// Start long-poll in goroutine
		resultCh := make(chan []StreamMessage, 1)
		errCh := make(chan error, 1)

		go func() {
			req, _ := http.NewRequest("GET", "http://"+addr+"/stream?live=long-poll&offset="+offset, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				errCh <- err
				return
			}
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				errCh <- err
				return
			}

			var messages []StreamMessage
			if err := json.Unmarshal(body, &messages); err != nil {
				errCh <- err
				return
			}
			resultCh <- messages
		}()

		// Wait for long-poll to establish, then broadcast
		time.Sleep(200 * time.Millisecond)

		session := &Session{
			ID:        "realtime-session",
			Repo:      "owner/repo",
			Branch:    "wisp/realtime",
			Spec:      "spec.md",
			Status:    SessionStatusRunning,
			Iteration: 1,
			StartedAt: "2024-01-15T12:00:00Z",
		}
		require.NoError(t, server.Streams().BroadcastSession(session))

		// Wait for result
		select {
		case messages := <-resultCh:
			require.Len(t, messages, 1)
			assert.Equal(t, MessageTypeSession, messages[0].Type)
		case err := <-errCh:
			t.Fatalf("long-poll error: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("long-poll did not receive message in time")
		}
	})

	t.Run("multiple_rapid_updates_delivered", func(t *testing.T) {
		// Get current offset
		req, _ := http.NewRequest("GET", "http://"+addr+"/stream?offset=now", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		offset := resp.Header.Get("Stream-Next-Offset")
		resp.Body.Close()

		// Broadcast multiple messages rapidly
		for i := 0; i < 10; i++ {
			task := &Task{
				ID:        fmt.Sprintf("rapid-task-%d", i),
				SessionID: "rapid-session",
				Order:     i,
				Content:   fmt.Sprintf("Task %d", i),
				Status:    TaskStatusPending,
			}
			require.NoError(t, server.Streams().BroadcastTask(task))
		}

		// Request should get all messages
		req, _ = http.NewRequest("GET", "http://"+addr+"/stream?offset="+offset, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err = http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var messages []StreamMessage
		require.NoError(t, json.Unmarshal(body, &messages))
		assert.Len(t, messages, 10)
	})

	t.Run("claude_events_delivered_in_order", func(t *testing.T) {
		// Get current offset
		req, _ := http.NewRequest("GET", "http://"+addr+"/stream?offset=now", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		offset := resp.Header.Get("Stream-Next-Offset")
		resp.Body.Close()

		// Broadcast Claude events
		for i := 0; i < 5; i++ {
			event := &ClaudeEvent{
				ID:        fmt.Sprintf("sess-1-%d", i),
				SessionID: "sess",
				Iteration: 1,
				Sequence:  i,
				Message: map[string]any{
					"type": "assistant",
					"message": map[string]any{
						"content": []any{
							map[string]any{
								"type": "text",
								"text": fmt.Sprintf("Message %d", i),
							},
						},
					},
				},
				Timestamp: time.Now().Format(time.RFC3339),
			}
			require.NoError(t, server.Streams().BroadcastClaudeEvent(event))
		}

		// Request should get all events in order
		req, _ = http.NewRequest("GET", "http://"+addr+"/stream?offset="+offset, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err = http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var messages []StreamMessage
		require.NoError(t, json.Unmarshal(body, &messages))
		assert.Len(t, messages, 5)

		for i, msg := range messages {
			assert.Equal(t, MessageTypeClaudeEvent, msg.Type)
			dataBytes, _ := json.Marshal(msg.Data)
			var evt ClaudeEvent
			json.Unmarshal(dataBytes, &evt)
			assert.Equal(t, i, evt.Sequence)
		}
	})
}

// TestIntegrationInputSubmission tests the input submission flow including
// storing responses and broadcasting updates.
func TestIntegrationInputSubmission(t *testing.T) {
	t.Parallel()

	server := createTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	go func() {
		server.Start(ctx)
	}()
	time.Sleep(100 * time.Millisecond)
	defer server.Stop()

	addr := server.ListenAddr()
	token := getAuthToken(t, addr)

	t.Run("submit_input_stores_and_broadcasts", func(t *testing.T) {
		// Get current offset to track broadcasts
		req, _ := http.NewRequest("GET", "http://"+addr+"/stream?offset=now", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		offset := resp.Header.Get("Stream-Next-Offset")
		resp.Body.Close()

		// Submit input
		body := `{"request_id": "submit-test-1", "response": "yes, proceed"}`
		req, _ = http.NewRequest("POST", "http://"+addr+"/input", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err = http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var result struct {
			Status string `json:"status"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
		assert.Equal(t, "received", result.Status)

		// Verify response was stored
		response, ok := server.GetPendingInput("submit-test-1")
		assert.True(t, ok)
		assert.Equal(t, "yes, proceed", response)

		// Verify broadcast was sent
		req, _ = http.NewRequest("GET", "http://"+addr+"/stream?offset="+offset, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err = http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		bodyBytes, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var messages []StreamMessage
		require.NoError(t, json.Unmarshal(bodyBytes, &messages))
		require.Len(t, messages, 1)
		assert.Equal(t, MessageTypeInputRequest, messages[0].Type)

		dataBytes, _ := json.Marshal(messages[0].Data)
		var inputReq InputRequest
		json.Unmarshal(dataBytes, &inputReq)
		assert.Equal(t, "submit-test-1", inputReq.ID)
		assert.True(t, inputReq.Responded)
		require.NotNil(t, inputReq.Response)
		assert.Equal(t, "yes, proceed", *inputReq.Response)
	})

	t.Run("submit_with_empty_response", func(t *testing.T) {
		body := `{"request_id": "empty-response-1", "response": ""}`
		req, _ := http.NewRequest("POST", "http://"+addr+"/input", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Empty string response should still be stored
		response, ok := server.GetPendingInput("empty-response-1")
		assert.True(t, ok)
		assert.Equal(t, "", response)
	})

	t.Run("submit_without_request_id_fails", func(t *testing.T) {
		body := `{"response": "yes"}`
		req, _ := http.NewRequest("POST", "http://"+addr+"/input", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("submit_invalid_json_fails", func(t *testing.T) {
		body := `{invalid json}`
		req, _ := http.NewRequest("POST", "http://"+addr+"/input", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

// TestIntegrationConcurrentInputHandling tests the first-response-wins semantics
// for concurrent input from TUI and web clients.
func TestIntegrationConcurrentInputHandling(t *testing.T) {
	t.Parallel()

	server := createTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	go func() {
		server.Start(ctx)
	}()
	time.Sleep(100 * time.Millisecond)
	defer server.Stop()

	addr := server.ListenAddr()
	token := getAuthToken(t, addr)

	t.Run("web_then_web_second_rejected", func(t *testing.T) {
		requestID := "concurrent-web-web-1"

		// First web response
		body := fmt.Sprintf(`{"request_id": "%s", "response": "first web response"}`, requestID)
		req, _ := http.NewRequest("POST", "http://"+addr+"/input", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()

		// Second web response should be rejected
		body = fmt.Sprintf(`{"request_id": "%s", "response": "second web response"}`, requestID)
		req, _ = http.NewRequest("POST", "http://"+addr+"/input", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err = http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusConflict, resp.StatusCode)

		var result struct {
			Status string `json:"status"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
		assert.Equal(t, "already_responded", result.Status)
	})

	t.Run("tui_then_web_rejected", func(t *testing.T) {
		requestID := "concurrent-tui-web-1"

		// Simulate TUI responding first
		server.MarkInputResponded(requestID)

		// Web response should be rejected
		body := fmt.Sprintf(`{"request_id": "%s", "response": "web response after tui"}`, requestID)
		req, _ := http.NewRequest("POST", "http://"+addr+"/input", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusConflict, resp.StatusCode)

		var result struct {
			Status string `json:"status"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
		assert.Equal(t, "already_responded", result.Status)
	})

	t.Run("concurrent_web_requests_one_wins", func(t *testing.T) {
		requestID := "concurrent-race-1"

		// Launch multiple concurrent requests
		var wg sync.WaitGroup
		var successCount int32
		var conflictCount int32

		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()

				body := fmt.Sprintf(`{"request_id": "%s", "response": "response %d"}`, requestID, n)
				req, _ := http.NewRequest("POST", "http://"+addr+"/input", strings.NewReader(body))
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("Content-Type", "application/json")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					return
				}
				defer resp.Body.Close()

				switch resp.StatusCode {
				case http.StatusOK:
					atomic.AddInt32(&successCount, 1)
				case http.StatusConflict:
					atomic.AddInt32(&conflictCount, 1)
				}
			}(i)
		}

		wg.Wait()

		// Exactly one should succeed, rest should get conflict
		assert.Equal(t, int32(1), successCount, "exactly one request should succeed")
		assert.Equal(t, int32(9), conflictCount, "rest should get conflict")
	})

	t.Run("isInputResponded_reflects_state", func(t *testing.T) {
		requestID := "state-check-1"

		// Initially not responded
		assert.False(t, server.IsInputResponded(requestID))

		// After web submission
		body := fmt.Sprintf(`{"request_id": "%s", "response": "test"}`, requestID)
		req, _ := http.NewRequest("POST", "http://"+addr+"/input", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()

		// Now should be responded
		assert.True(t, server.IsInputResponded(requestID))
	})

	t.Run("tui_check_before_response_prevents_conflict", func(t *testing.T) {
		requestID := "tui-check-1"

		// TUI checks if already responded (simulating loop behavior)
		assert.False(t, server.IsInputResponded(requestID))

		// Web submits first
		body := fmt.Sprintf(`{"request_id": "%s", "response": "web wins"}`, requestID)
		req, _ := http.NewRequest("POST", "http://"+addr+"/input", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()

		// TUI checks again and sees it's responded
		assert.True(t, server.IsInputResponded(requestID))

		// TUI can get the response
		response, ok := server.GetPendingInput(requestID)
		assert.True(t, ok)
		assert.Equal(t, "web wins", response)
	})
}

// TestIntegrationSSEStream tests Server-Sent Events streaming mode.
func TestIntegrationSSEStream(t *testing.T) {
	t.Parallel()

	server := createTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	go func() {
		server.Start(ctx)
	}()
	time.Sleep(100 * time.Millisecond)
	defer server.Stop()

	addr := server.ListenAddr()
	token := getAuthToken(t, addr)

	t.Run("sse_receives_initial_control_event", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "http://"+addr+"/stream?live=sse&offset=now", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")

		// Read first event (should be control)
		buf := make([]byte, 4096)
		n, err := resp.Body.Read(buf)
		// err might be timeout or nil, both ok
		if err != nil && err != io.EOF {
			// timeout is expected
			t.Logf("Read completed with: %v", err)
		}

		body := string(buf[:n])
		assert.Contains(t, body, "event: control")
		assert.Contains(t, body, "streamNextOffset")
	})

	t.Run("sse_receives_data_events", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "http://"+addr+"/stream?live=sse&offset=now", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		// Use a context with cancel for controlled shutdown
		reqCtx, reqCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer reqCancel()
		req = req.WithContext(reqCtx)

		client := &http.Client{}
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		// Read in goroutine while we broadcast
		dataCh := make(chan string, 10)
		go func() {
			buf := make([]byte, 8192)
			for {
				n, err := resp.Body.Read(buf)
				if n > 0 {
					dataCh <- string(buf[:n])
				}
				if err != nil {
					close(dataCh)
					return
				}
			}
		}()

		// Wait for initial control event
		time.Sleep(200 * time.Millisecond)

		// Broadcast a message
		session := &Session{
			ID:        "sse-test-session",
			Repo:      "test/repo",
			Branch:    "sse-test",
			Spec:      "spec.md",
			Status:    SessionStatusRunning,
			Iteration: 1,
			StartedAt: time.Now().Format(time.RFC3339),
		}
		require.NoError(t, server.Streams().BroadcastSession(session))

		// Collect data
		var allData strings.Builder
		timeout := time.After(3 * time.Second)
	collect:
		for {
			select {
			case data, ok := <-dataCh:
				if !ok {
					break collect
				}
				allData.WriteString(data)
				if strings.Contains(data, "sse-test-session") {
					break collect
				}
			case <-timeout:
				break collect
			}
		}

		assert.Contains(t, allData.String(), "event: data")
		assert.Contains(t, allData.String(), "sse-test-session")
	})
}

// TestIntegrationServerLifecycle tests server start, stop, and restart behavior.
func TestIntegrationServerLifecycle(t *testing.T) {
	t.Parallel()

	hash, err := auth.HashPassword(testPassword)
	require.NoError(t, err)

	t.Run("start_stop_restart", func(t *testing.T) {
		server, err := NewServer(&Config{
			Port:         0,
			PasswordHash: hash,
		})
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Start
		errCh := make(chan error, 1)
		go func() {
			errCh <- server.Start(ctx)
		}()
		time.Sleep(100 * time.Millisecond)

		addr := server.ListenAddr()
		require.NotEmpty(t, addr)

		// Verify working
		form := url.Values{"password": {testPassword}}
		resp, err := http.PostForm("http://"+addr+"/auth", form)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()

		// Stop via server.Stop() for more reliable shutdown
		err = server.Stop()
		require.NoError(t, err)

		// Wait for shutdown to complete
		select {
		case err := <-errCh:
			// Should exit cleanly
			assert.NoError(t, err)
		case <-time.After(3 * time.Second):
			t.Fatal("server did not stop in time")
		}

		// Should be stopped - connection should be refused
		client := &http.Client{Timeout: 1 * time.Second}
		_, err = client.PostForm("http://"+addr+"/auth", form)
		assert.Error(t, err, "expected connection refused after server stop")
	})

	t.Run("graceful_shutdown_with_active_connections", func(t *testing.T) {
		server, err := NewServer(&Config{
			Port:         0,
			PasswordHash: hash,
		})
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go func() {
			server.Start(ctx)
		}()
		time.Sleep(100 * time.Millisecond)

		addr := server.ListenAddr()
		token := getAuthToken(t, addr)

		// Start a long-poll connection
		go func() {
			req, _ := http.NewRequest("GET", "http://"+addr+"/stream?live=long-poll&offset=now", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			client := &http.Client{Timeout: 30 * time.Second}
			client.Do(req)
		}()

		time.Sleep(100 * time.Millisecond)

		// Stop should complete without hanging
		done := make(chan bool)
		go func() {
			server.Stop()
			done <- true
		}()

		select {
		case <-done:
			// Good
		case <-time.After(6 * time.Second):
			t.Fatal("graceful shutdown took too long")
		}
	})
}

// TestIntegrationEndToEndFlow tests a complete realistic flow:
// authenticate, sync state, receive updates, submit input.
func TestIntegrationEndToEndFlow(t *testing.T) {
	t.Parallel()

	server := createTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	go func() {
		server.Start(ctx)
	}()
	time.Sleep(100 * time.Millisecond)
	defer server.Stop()

	addr := server.ListenAddr()

	// Step 1: Authenticate
	form := url.Values{"password": {testPassword}}
	resp, err := http.PostForm("http://"+addr+"/auth", form)
	require.NoError(t, err)
	var authResp struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&authResp))
	resp.Body.Close()
	token := authResp.Token

	// Step 2: Initial state sync - should be empty
	req, _ := http.NewRequest("GET", "http://"+addr+"/stream", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, "[]", string(body))

	// Step 3: Simulate session starting (server-side broadcast)
	session := &Session{
		ID:        "e2e-session",
		Repo:      "owner/repo",
		Branch:    "wisp/e2e-test",
		Spec:      "docs/rfc.md",
		Status:    SessionStatusRunning,
		Iteration: 1,
		StartedAt: time.Now().Format(time.RFC3339),
	}
	require.NoError(t, server.Streams().BroadcastSession(session))

	task := &Task{
		ID:        "e2e-task-1",
		SessionID: "e2e-session",
		Order:     0,
		Content:   "Implement feature",
		Status:    TaskStatusInProgress,
	}
	require.NoError(t, server.Streams().BroadcastTask(task))

	// Step 4: Get updates
	req, _ = http.NewRequest("GET", "http://"+addr+"/stream", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	offset := resp.Header.Get("Stream-Next-Offset")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	var messages []StreamMessage
	require.NoError(t, json.Unmarshal(body, &messages))
	assert.Len(t, messages, 2)

	// Step 5: NEEDS_INPUT broadcast
	session.Status = SessionStatusNeedsInput
	session.Iteration = 2
	require.NoError(t, server.Streams().BroadcastSession(session))

	inputReq := &InputRequest{
		ID:        "e2e-input-1",
		SessionID: "e2e-session",
		Iteration: 2,
		Question:  "Should I continue with approach A or B?",
		Responded: false,
		Response:  nil,
	}
	require.NoError(t, server.Streams().BroadcastInputRequest(inputReq))

	// Step 6: Client receives NEEDS_INPUT update
	req, _ = http.NewRequest("GET", "http://"+addr+"/stream?offset="+offset, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	offset = resp.Header.Get("Stream-Next-Offset")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	require.NoError(t, json.Unmarshal(body, &messages))
	assert.Len(t, messages, 2) // session update + input request

	// Find input request
	var foundInput bool
	for _, msg := range messages {
		if msg.Type == MessageTypeInputRequest {
			foundInput = true
			dataBytes, _ := json.Marshal(msg.Data)
			var ir InputRequest
			json.Unmarshal(dataBytes, &ir)
			assert.Equal(t, "e2e-input-1", ir.ID)
			assert.False(t, ir.Responded)
		}
	}
	assert.True(t, foundInput)

	// Step 7: Submit response
	inputBody := `{"request_id": "e2e-input-1", "response": "Go with approach A"}`
	req, _ = http.NewRequest("POST", "http://"+addr+"/input", strings.NewReader(inputBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Step 8: Verify response was stored
	response, ok := server.GetPendingInput("e2e-input-1")
	assert.True(t, ok)
	assert.Equal(t, "Go with approach A", response)

	// Step 9: Client sees input as responded
	req, _ = http.NewRequest("GET", "http://"+addr+"/stream?offset="+offset, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	require.NoError(t, json.Unmarshal(body, &messages))
	require.Len(t, messages, 1)

	dataBytes, _ := json.Marshal(messages[0].Data)
	var finalInput InputRequest
	json.Unmarshal(dataBytes, &finalInput)
	assert.True(t, finalInput.Responded)
	require.NotNil(t, finalInput.Response)
	assert.Equal(t, "Go with approach A", *finalInput.Response)
}
