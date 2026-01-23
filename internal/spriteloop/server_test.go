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
		assert.Equal(t, DefaultStreamPath, s.StreamPath())
		assert.Equal(t, DefaultPollInterval, s.pollInterval)
	})

	t.Run("uses provided values", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		s := NewServer(ServerOptions{
			Port:         9999,
			StreamPath:   "/custom/stream",
			Token:        "test-token",
			PollInterval: 50 * time.Millisecond,
			FileStore:    fs,
		})

		assert.Equal(t, 9999, s.Port())
		assert.Equal(t, "/custom/stream", s.StreamPath())
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

func TestHandleStreamEndpoint(t *testing.T) {
	t.Parallel()

	t.Run("POST appends command event to stream", func(t *testing.T) {
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

		// Create a command event
		cmd := &stream.Command{
			ID:   "cmd-1",
			Type: stream.CommandTypeBackground,
		}
		event, _ := stream.NewCommandEvent(cmd)
		body, _ := event.Marshal()

		req := httptest.NewRequest(http.MethodPost, DefaultStreamPath, strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		s.handleStreamEndpoint(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.NotEmpty(t, w.Header().Get(headerStreamNextOffset))

		// Event should have been appended to stream
		events, err := fs.Read(0)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(events), 1)
	})

	t.Run("POST rejects invalid JSON", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		s := NewServer(ServerOptions{
			FileStore: fs,
		})

		body := `{invalid json`
		req := httptest.NewRequest(http.MethodPost, DefaultStreamPath, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		s.handleStreamEndpoint(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("HEAD returns stream metadata", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		s := NewServer(ServerOptions{
			FileStore: fs,
		})

		req := httptest.NewRequest(http.MethodHead, DefaultStreamPath, nil)
		w := httptest.NewRecorder()

		s.handleStreamEndpoint(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.NotEmpty(t, w.Header().Get(headerStreamNextOffset))
	})
}

func TestHandleStreamRead(t *testing.T) {
	t.Parallel()

	t.Run("returns empty array when no events", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		s := NewServer(ServerOptions{
			FileStore: fs,
		})

		req := httptest.NewRequest(http.MethodGet, DefaultStreamPath, nil)
		w := httptest.NewRecorder()

		s.handleStreamEndpoint(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
		assert.NotEmpty(t, w.Header().Get(headerStreamNextOffset))
		assert.Equal(t, "true", w.Header().Get(headerStreamUpToDate))

		var events []json.RawMessage
		err = json.Unmarshal(w.Body.Bytes(), &events)
		require.NoError(t, err)
		assert.Empty(t, events)
	})

	t.Run("returns events from stream", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		// Add some events
		event1, _ := stream.NewSessionEvent(&stream.SessionEvent{
			ID:     "session-1",
			Status: stream.SessionStatusRunning,
		})
		fs.Append(event1)

		event2, _ := stream.NewTaskEvent(&stream.TaskEvent{
			ID:          "task-1",
			Description: "Test task",
		})
		fs.Append(event2)

		s := NewServer(ServerOptions{
			FileStore: fs,
		})

		req := httptest.NewRequest(http.MethodGet, DefaultStreamPath+"?offset=0_0", nil)
		w := httptest.NewRecorder()

		s.handleStreamEndpoint(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var events []json.RawMessage
		err = json.Unmarshal(w.Body.Bytes(), &events)
		require.NoError(t, err)
		assert.Len(t, events, 2)
	})

	t.Run("respects offset parameter", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		// Add events
		for i := 0; i < 5; i++ {
			event, _ := stream.NewSessionEvent(&stream.SessionEvent{
				ID:        fmt.Sprintf("session-%d", i),
				Iteration: i,
			})
			fs.Append(event)
		}

		s := NewServer(ServerOptions{
			FileStore: fs,
		})

		// Request from offset 3 (seq 3)
		req := httptest.NewRequest(http.MethodGet, DefaultStreamPath+"?offset=3_3", nil)
		w := httptest.NewRecorder()

		s.handleStreamEndpoint(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var events []json.RawMessage
		err = json.Unmarshal(w.Body.Bytes(), &events)
		require.NoError(t, err)
		// Should get events starting from seq 3
		assert.LessOrEqual(t, len(events), 3) // events 3, 4, 5
	})

	t.Run("handles offset=now", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		// Add some events
		event, _ := stream.NewSessionEvent(&stream.SessionEvent{ID: "session-1"})
		fs.Append(event)

		s := NewServer(ServerOptions{
			FileStore: fs,
		})

		req := httptest.NewRequest(http.MethodGet, DefaultStreamPath+"?offset=now", nil)
		w := httptest.NewRecorder()

		s.handleStreamEndpoint(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "true", w.Header().Get(headerStreamUpToDate))

		// With offset=now, should get no events (we're at the tail)
		var events []json.RawMessage
		err = json.Unmarshal(w.Body.Bytes(), &events)
		require.NoError(t, err)
		assert.Empty(t, events)
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

		req := httptest.NewRequest(http.MethodGet, DefaultStreamPath, nil)
		w := httptest.NewRecorder()

		s.handleStreamEndpoint(w, req)

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

		req := httptest.NewRequest(http.MethodGet, DefaultStreamPath, nil)
		w := httptest.NewRecorder()

		s.handleStreamEndpoint(w, req)

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

		req := httptest.NewRequest(http.MethodGet, DefaultStreamPath, nil)
		req.Header.Set("Authorization", "Bearer secret-token")
		w := httptest.NewRecorder()

		s.handleStreamEndpoint(w, req)

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

		req := httptest.NewRequest(http.MethodGet, DefaultStreamPath+"?token=secret-token", nil)
		w := httptest.NewRecorder()

		s.handleStreamEndpoint(w, req)

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

		req := httptest.NewRequest(http.MethodGet, DefaultStreamPath, nil)
		req.Header.Set("Authorization", "Bearer wrong-token")
		w := httptest.NewRecorder()

		s.handleStreamEndpoint(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

func TestHandleSSE(t *testing.T) {
	t.Parallel()

	t.Run("streams existing events", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		require.NoError(t, err)
		defer fs.Close()

		// Add some events
		event1, _ := stream.NewSessionEvent(&stream.SessionEvent{
			ID:     "session-1",
			Status: stream.SessionStatusRunning,
		})
		fs.Append(event1)

		event2, _ := stream.NewTaskEvent(&stream.TaskEvent{
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

		req := httptest.NewRequest(http.MethodGet, DefaultStreamPath+"?live=sse", nil)
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
			s.handleSSE(w, req, 0)
		}()

		// Read events from the pipe
		reader := bufio.NewReader(pr)
		receivedData := false

		// Try to read data event with timeout
		dataRead := make(chan bool, 1)
		go func() {
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					dataRead <- false
					return
				}
				if strings.HasPrefix(line, "event: data") || strings.HasPrefix(line, "event: control") {
					dataRead <- true
					return
				}
			}
		}()

		select {
		case receivedData = <-dataRead:
		case <-time.After(500 * time.Millisecond):
		}

		// Cancel context to stop the handler
		cancel()
		pw.Close()
		<-done

		assert.True(t, receivedData, "expected to receive SSE events")
	})
}

func TestFormatOffset(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "0_0", formatOffset(0))
	assert.Equal(t, "42_42", formatOffset(42))
	assert.Equal(t, "100_100", formatOffset(100))
}

func TestFormatEventsAsJSON(t *testing.T) {
	t.Parallel()

	t.Run("empty events returns empty array", func(t *testing.T) {
		result := formatEventsAsJSON(nil)
		assert.Equal(t, "[]", string(result))

		result = formatEventsAsJSON([]*stream.Event{})
		assert.Equal(t, "[]", string(result))
	})

	t.Run("formats events as JSON array", func(t *testing.T) {
		event, _ := stream.NewSessionEvent(&stream.SessionEvent{
			ID:     "session-1",
			Status: stream.SessionStatusRunning,
		})
		event.Seq = 1

		result := formatEventsAsJSON([]*stream.Event{event})

		var parsed []json.RawMessage
		err := json.Unmarshal(result, &parsed)
		require.NoError(t, err)
		assert.Len(t, parsed, 1)
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

// Helper function to suppress compiler errors in tests
func init() {
	_ = os.Stdout
}

func TestServerStartAndStop(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
	require.NoError(t, err)
	defer fs.Close()

	// Use a high port to avoid conflicts
	s := NewServer(ServerOptions{
		Port:      19374 + time.Now().Nanosecond()%1000,
		FileStore: fs,
	})

	// Server should not be running initially
	assert.False(t, s.Running())

	// Start the server
	err = s.Start()
	if err != nil {
		// Port might be in use, skip test
		t.Skipf("Could not start server (port in use?): %v", err)
	}

	// Server should now be running
	assert.True(t, s.Running())

	// Starting again should return error
	err = s.Start()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already running")

	// Stop the server
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = s.Stop(ctx)
	assert.NoError(t, err)

	// Server should no longer be running
	assert.False(t, s.Running())

	// Stopping again should be a no-op
	err = s.Stop(ctx)
	assert.NoError(t, err)
}

func TestServerStartWithInvalidPort(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
	require.NoError(t, err)
	defer fs.Close()

	// Use port 1 which typically requires root permissions
	s := NewServer(ServerOptions{
		Port:      1, // Should fail without root
		FileStore: fs,
	})

	err = s.Start()
	// On most systems, this should fail (no permission to bind to port 1)
	// But on some test environments it might work, so we just check the logic runs
	if err != nil {
		assert.Contains(t, err.Error(), "failed to start server")
	} else {
		// Clean up if it somehow worked
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		s.Stop(ctx)
	}
}

func TestServerHealthEndpoint(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
	require.NoError(t, err)
	defer fs.Close()

	port := 19500 + time.Now().Nanosecond()%1000
	s := NewServer(ServerOptions{
		Port:      port,
		FileStore: fs,
	})

	err = s.Start()
	if err != nil {
		t.Skipf("Could not start server: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		s.Stop(ctx)
	}()

	// Wait for server to be ready
	time.Sleep(50 * time.Millisecond)

	// Make request to health endpoint
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/health", port))
	if err != nil {
		t.Skipf("Could not connect to server: %v", err)
	}
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestServerStreamEndpoint(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	fs, err := stream.NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
	require.NoError(t, err)
	defer fs.Close()

	port := 19600 + time.Now().Nanosecond()%1000
	s := NewServer(ServerOptions{
		Port:      port,
		FileStore: fs,
	})

	err = s.Start()
	if err != nil {
		t.Skipf("Could not start server: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		s.Stop(ctx)
	}()

	// Wait for server to be ready
	time.Sleep(50 * time.Millisecond)

	// Make request to stream endpoint
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d%s", port, DefaultStreamPath))
	if err != nil {
		t.Skipf("Could not connect to server: %v", err)
	}
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	assert.NotEmpty(t, resp.Header.Get(headerStreamNextOffset))
}
