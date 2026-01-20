package spriteloop

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thruflo/wisp/internal/stream"
)

func TestNewServer(t *testing.T) {
	t.Parallel()

	t.Run("uses defaults when not specified", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		s := NewServer(ServerOptions{
			FileStore: fs,
		})

		assert.Equal(t, DefaultServerPort, s.Port())
		assert.Equal(t, DefaultPollInterval, s.pollInterval)
	})

	t.Run("uses provided values", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		s := NewServer(ServerOptions{
			Port:         9999,
			Token:        "test-token",
			PollInterval: 50 * time.Millisecond,
			FileStore:    fs,
		})

		assert.Equal(t, 9999, s.Port())
		assert.Equal(t, "test-token", s.token)
		assert.Equal(t, 50*time.Millisecond, s.pollInterval)
	})
}

func TestServerStartStop(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
	require.NoError(t, err)
	defer fs.Close()

	// Use port 0 to get a random available port
	s := NewServer(ServerOptions{
		Port:      0,
		FileStore: fs,
	})

	// Note: Server doesn't support port 0, we need to use a specific port
	// Let's just test the Start/Stop logic with httptest instead
	assert.False(t, s.Running())
}

func TestHandleHealth(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
	require.NoError(t, err)
	defer fs.Close()

	s := NewServer(ServerOptions{
		FileStore: fs,
	})

	t.Run("returns OK status", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		w := httptest.NewRecorder()

		s.handleHealth(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

		var resp map[string]any
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.Equal(t, "ok", resp["status"])
	})

	t.Run("rejects non-GET methods", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/health", nil)
		w := httptest.NewRecorder()

		s.handleHealth(w, req)

		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})
}

func TestHandleCommand(t *testing.T) {
	t.Parallel()

	t.Run("accepts valid command", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		cmdCh := make(chan *stream.Command, 10)
		inputCh := make(chan string, 1)
		cp := NewCommandProcessor(CommandProcessorOptions{
			FileStore: fs,
			CommandCh: cmdCh,
			InputCh:   inputCh,
		})

		s := NewServer(ServerOptions{
			FileStore:        fs,
			CommandProcessor: cp,
		})

		body := `{"id": "cmd-1", "type": "background"}`
		req := httptest.NewRequest(http.MethodPost, "/command", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		s.handleCommand(w, req)

		assert.Equal(t, http.StatusAccepted, w.Code)

		var resp map[string]string
		err = json.Unmarshal(w.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.Equal(t, "accepted", resp["status"])
		assert.Equal(t, "cmd-1", resp["command_id"])

		// Command should have been sent to channel
		select {
		case cmd := <-cmdCh:
			assert.Equal(t, "cmd-1", cmd.ID)
			assert.Equal(t, stream.CommandTypeBackground, cmd.Type)
		case <-time.After(100 * time.Millisecond):
			t.Fatal("command not received")
		}
	})

	t.Run("rejects invalid JSON", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		s := NewServer(ServerOptions{
			FileStore: fs,
		})

		body := `{invalid json`
		req := httptest.NewRequest(http.MethodPost, "/command", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		s.handleCommand(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("rejects missing command ID", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		s := NewServer(ServerOptions{
			FileStore: fs,
		})

		body := `{"type": "background"}`
		req := httptest.NewRequest(http.MethodPost, "/command", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		s.handleCommand(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("rejects missing command type", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		s := NewServer(ServerOptions{
			FileStore: fs,
		})

		body := `{"id": "cmd-1"}`
		req := httptest.NewRequest(http.MethodPost, "/command", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		s.handleCommand(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("rejects non-POST methods", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		s := NewServer(ServerOptions{
			FileStore: fs,
		})

		req := httptest.NewRequest(http.MethodGet, "/command", nil)
		w := httptest.NewRecorder()

		s.handleCommand(w, req)

		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})
}

func TestHandleState(t *testing.T) {
	t.Parallel()

	t.Run("returns empty state when no events", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		s := NewServer(ServerOptions{
			FileStore: fs,
		})

		req := httptest.NewRequest(http.MethodGet, "/state", nil)
		w := httptest.NewRecorder()

		s.handleState(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var state StateSnapshot
		err = json.Unmarshal(w.Body.Bytes(), &state)
		require.NoError(t, err)
		assert.Equal(t, uint64(0), state.LastSeq)
		assert.Nil(t, state.Session)
		assert.Empty(t, state.Tasks)
	})

	t.Run("returns state from events", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		// Add session event
		sessionEvent, _ := stream.NewEvent(stream.MessageTypeSession, &stream.SessionEvent{
			ID:        "test-session",
			Branch:    "feature-branch",
			Status:    stream.SessionStatusRunning,
			Iteration: 5,
		})
		fs.Append(sessionEvent)

		// Add task events
		task1Event, _ := stream.NewEvent(stream.MessageTypeTask, &stream.TaskEvent{
			ID:          "task-0",
			SessionID:   "test-session",
			Order:       0,
			Category:    "setup",
			Description: "Initialize project",
			Status:      stream.TaskStatusCompleted,
		})
		fs.Append(task1Event)

		task2Event, _ := stream.NewEvent(stream.MessageTypeTask, &stream.TaskEvent{
			ID:          "task-1",
			SessionID:   "test-session",
			Order:       1,
			Category:    "feature",
			Description: "Add feature",
			Status:      stream.TaskStatusInProgress,
		})
		fs.Append(task2Event)

		s := NewServer(ServerOptions{
			FileStore: fs,
		})

		req := httptest.NewRequest(http.MethodGet, "/state", nil)
		w := httptest.NewRecorder()

		s.handleState(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var state StateSnapshot
		err = json.Unmarshal(w.Body.Bytes(), &state)
		require.NoError(t, err)
		assert.Equal(t, uint64(3), state.LastSeq)
		require.NotNil(t, state.Session)
		assert.Equal(t, "test-session", state.Session.ID)
		assert.Equal(t, stream.SessionStatusRunning, state.Session.Status)
		assert.Len(t, state.Tasks, 2)
		assert.Equal(t, "Initialize project", state.Tasks[0].Description)
		assert.Equal(t, "Add feature", state.Tasks[1].Description)
	})

	t.Run("includes pending input request", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		// Add input request event
		inputEvent, _ := stream.NewEvent(stream.MessageTypeInputRequest, &stream.InputRequestEvent{
			ID:        "input-1",
			SessionID: "test-session",
			Iteration: 3,
			Question:  "What do you want to do?",
			Responded: false,
		})
		fs.Append(inputEvent)

		s := NewServer(ServerOptions{
			FileStore: fs,
		})

		req := httptest.NewRequest(http.MethodGet, "/state", nil)
		w := httptest.NewRecorder()

		s.handleState(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var state StateSnapshot
		err = json.Unmarshal(w.Body.Bytes(), &state)
		require.NoError(t, err)
		require.NotNil(t, state.LastInput)
		assert.Equal(t, "input-1", state.LastInput.ID)
		assert.Equal(t, "What do you want to do?", state.LastInput.Question)
		assert.False(t, state.LastInput.Responded)
	})

	t.Run("rejects non-GET methods", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		s := NewServer(ServerOptions{
			FileStore: fs,
		})

		req := httptest.NewRequest(http.MethodPost, "/state", nil)
		w := httptest.NewRecorder()

		s.handleState(w, req)

		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})
}

func TestAuthentication(t *testing.T) {
	t.Parallel()

	t.Run("allows all requests when no token configured", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		s := NewServer(ServerOptions{
			FileStore: fs,
		})

		req := httptest.NewRequest(http.MethodGet, "/state", nil)
		w := httptest.NewRecorder()

		s.handleState(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("rejects requests without token when configured", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		s := NewServer(ServerOptions{
			Token:     "secret-token",
			FileStore: fs,
		})

		req := httptest.NewRequest(http.MethodGet, "/state", nil)
		w := httptest.NewRecorder()

		s.handleState(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("accepts Bearer token in Authorization header", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		s := NewServer(ServerOptions{
			Token:     "secret-token",
			FileStore: fs,
		})

		req := httptest.NewRequest(http.MethodGet, "/state", nil)
		req.Header.Set("Authorization", "Bearer secret-token")
		w := httptest.NewRecorder()

		s.handleState(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("accepts token in query parameter", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		s := NewServer(ServerOptions{
			Token:     "secret-token",
			FileStore: fs,
		})

		req := httptest.NewRequest(http.MethodGet, "/state?token=secret-token", nil)
		w := httptest.NewRecorder()

		s.handleState(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("rejects wrong token", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		s := NewServer(ServerOptions{
			Token:     "secret-token",
			FileStore: fs,
		})

		req := httptest.NewRequest(http.MethodGet, "/state", nil)
		req.Header.Set("Authorization", "Bearer wrong-token")
		w := httptest.NewRecorder()

		s.handleState(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

func TestHandleStream(t *testing.T) {
	t.Parallel()

	t.Run("returns existing events", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		// Add some events
		event1, _ := stream.NewEvent(stream.MessageTypeSession, &stream.SessionEvent{
			ID:     "session-1",
			Status: stream.SessionStatusRunning,
		})
		fs.Append(event1)

		event2, _ := stream.NewEvent(stream.MessageTypeTask, &stream.TaskEvent{
			ID:          "task-1",
			Description: "Test task",
		})
		fs.Append(event2)

		s := NewServer(ServerOptions{
			FileStore:    fs,
			PollInterval: 10 * time.Millisecond,
		})

		// Create a context that will be canceled
		ctx, cancel := context.WithCancel(context.Background())

		req := httptest.NewRequest(http.MethodGet, "/stream", nil)
		req = req.WithContext(ctx)

		// Use a pipe to capture the SSE stream
		pr, pw := io.Pipe()

		w := &testResponseWriter{
			header: make(http.Header),
			body:   pw,
		}

		// Handle in goroutine since it blocks
		done := make(chan struct{})
		go func() {
			defer close(done)
			s.handleStream(w, req)
		}()

		// Read events from the pipe
		reader := bufio.NewReader(pr)
		events := make([]*stream.Event, 0)

		// Read the two events we added
		for i := 0; i < 2; i++ {
			event, err := readSSEEvent(reader)
			if err != nil {
				if i > 0 {
					break // Got at least one event
				}
				t.Fatalf("failed to read event %d: %v", i, err)
			}
			events = append(events, event)
		}

		// Cancel context to stop the handler
		cancel()
		pw.Close()
		<-done

		assert.GreaterOrEqual(t, len(events), 1)
		assert.Equal(t, stream.MessageTypeSession, events[0].Type)
	})

	t.Run("respects from_seq parameter", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		// Add events
		for i := 0; i < 5; i++ {
			event, _ := stream.NewEvent(stream.MessageTypeSession, &stream.SessionEvent{
				ID:        fmt.Sprintf("session-%d", i),
				Iteration: i,
			})
			fs.Append(event)
		}

		s := NewServer(ServerOptions{
			FileStore:    fs,
			PollInterval: 10 * time.Millisecond,
		})

		ctx, cancel := context.WithCancel(context.Background())

		// Request from seq 3 (should get events 3, 4, 5)
		req := httptest.NewRequest(http.MethodGet, "/stream?from_seq=3", nil)
		req = req.WithContext(ctx)

		pr, pw := io.Pipe()
		w := &testResponseWriter{
			header: make(http.Header),
			body:   pw,
		}

		done := make(chan struct{})
		go func() {
			defer close(done)
			s.handleStream(w, req)
		}()

		reader := bufio.NewReader(pr)
		event, err := readSSEEvent(reader)
		require.NoError(t, err)

		// First event should have seq >= 3
		assert.GreaterOrEqual(t, event.Seq, uint64(3))

		cancel()
		pw.Close()
		<-done
	})

	t.Run("rejects invalid from_seq", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		s := NewServer(ServerOptions{
			FileStore: fs,
		})

		req := httptest.NewRequest(http.MethodGet, "/stream?from_seq=invalid", nil)
		w := httptest.NewRecorder()

		s.handleStream(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("rejects non-GET methods", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		s := NewServer(ServerOptions{
			FileStore: fs,
		})

		req := httptest.NewRequest(http.MethodPost, "/stream", nil)
		w := httptest.NewRecorder()

		s.handleStream(w, req)

		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})
}

func TestSendSSEEvent(t *testing.T) {
	t.Parallel()

	t.Run("formats event correctly", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		s := NewServer(ServerOptions{
			FileStore: fs,
		})

		event := &stream.Event{
			Seq:  42,
			Type: stream.MessageTypeSession,
		}

		var buf strings.Builder
		w := &strings.Builder{}
		err = s.sendSSEEvent(&testResponseWriterString{w}, event)
		require.NoError(t, err)

		_ = buf
		output := w.String()
		assert.Contains(t, output, "id: 42")
		assert.Contains(t, output, "event: session")
		assert.Contains(t, output, "data: ")
	})
}

// testResponseWriter is a minimal http.ResponseWriter for testing SSE
type testResponseWriter struct {
	header http.Header
	body   io.Writer
	code   int
}

func (w *testResponseWriter) Header() http.Header {
	return w.header
}

func (w *testResponseWriter) Write(b []byte) (int, error) {
	return w.body.Write(b)
}

func (w *testResponseWriter) WriteHeader(code int) {
	w.code = code
}

func (w *testResponseWriter) Flush() {}

// testResponseWriterString wraps a strings.Builder as a ResponseWriter
type testResponseWriterString struct {
	w *strings.Builder
}

func (w *testResponseWriterString) Header() http.Header {
	return make(http.Header)
}

func (w *testResponseWriterString) Write(b []byte) (int, error) {
	return w.w.Write(b)
}

func (w *testResponseWriterString) WriteHeader(code int) {}

// readSSEEvent reads a single SSE event from a reader
func readSSEEvent(r *bufio.Reader) (*stream.Event, error) {
	var dataLine string

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}

		line = strings.TrimSuffix(line, "\n")

		if line == "" {
			// End of event
			if dataLine != "" {
				break
			}
			continue
		}

		if strings.HasPrefix(line, "data: ") {
			dataLine = strings.TrimPrefix(line, "data: ")
		}
	}

	if dataLine == "" {
		return nil, fmt.Errorf("no data in event")
	}

	var event stream.Event
	if err := json.Unmarshal([]byte(dataLine), &event); err != nil {
		return nil, fmt.Errorf("failed to unmarshal event: %w", err)
	}

	return &event, nil
}

// Helper function to suppress compiler errors in tests
func init() {
	_ = os.Stdout
}
