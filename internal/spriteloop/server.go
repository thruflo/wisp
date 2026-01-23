package spriteloop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/thruflo/wisp/internal/stream"
)

const (
	// DefaultServerPort is the default port for the HTTP server.
	DefaultServerPort = 8374

	// DefaultPollInterval is the default interval for polling the FileStore.
	DefaultPollInterval = 100 * time.Millisecond
)

// Server provides an HTTP server for streaming events and receiving commands.
// It runs on the Sprite VM and serves as the communication endpoint for
// TUI and web clients.
type Server struct {
	// Configuration
	port         int
	token        string // Bearer token for authentication
	pollInterval time.Duration

	// Dependencies
	fileStore        *stream.FileStore
	commandProcessor *CommandProcessor
	loop             *Loop

	// State
	server   *http.Server
	mu       sync.Mutex
	running  bool
	shutdown chan struct{}
}

// ServerOptions holds configuration for creating a Server instance.
type ServerOptions struct {
	Port             int
	Token            string
	PollInterval     time.Duration
	FileStore        *stream.FileStore
	CommandProcessor *CommandProcessor
	Loop             *Loop
}

// NewServer creates a new HTTP server with the given options.
func NewServer(opts ServerOptions) *Server {
	port := opts.Port
	if port == 0 {
		port = DefaultServerPort
	}

	pollInterval := opts.PollInterval
	if pollInterval == 0 {
		pollInterval = DefaultPollInterval
	}

	return &Server{
		port:             port,
		token:            opts.Token,
		pollInterval:     pollInterval,
		fileStore:        opts.FileStore,
		commandProcessor: opts.CommandProcessor,
		loop:             opts.Loop,
		shutdown:         make(chan struct{}),
	}
}

// Start starts the HTTP server on the configured port.
// It returns immediately after starting the server in a goroutine.
func (s *Server) Start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("server already running")
	}
	s.running = true
	s.mu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("/stream", s.handleStream)
	mux.HandleFunc("/command", s.handleCommand)
	mux.HandleFunc("/state", s.handleState)
	mux.HandleFunc("/health", s.handleHealth)

	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.port),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 0, // No timeout for SSE
		IdleTimeout:  120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	// Give the server a moment to start
	select {
	case err := <-errCh:
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
		return fmt.Errorf("failed to start server: %w", err)
	case <-time.After(100 * time.Millisecond):
		// Server started successfully
		return nil
	}
}

// Stop gracefully shuts down the HTTP server.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	// Signal shutdown to subscribers
	close(s.shutdown)

	err := s.server.Shutdown(ctx)

	s.mu.Lock()
	s.running = false
	s.shutdown = make(chan struct{}) // Reset for potential restart
	s.mu.Unlock()

	return err
}

// Port returns the port the server is configured to listen on.
func (s *Server) Port() int {
	return s.port
}

// Running returns whether the server is currently running.
func (s *Server) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// handleStream implements the SSE (Server-Sent Events) endpoint for streaming events.
// GET /stream?from_seq=N
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check authentication
	if !s.authenticate(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Parse from_seq parameter
	fromSeq := uint64(0)
	if fromSeqStr := r.URL.Query().Get("from_seq"); fromSeqStr != "" {
		parsed, err := strconv.ParseUint(fromSeqStr, 10, 64)
		if err != nil {
			http.Error(w, "Invalid from_seq parameter", http.StatusBadRequest)
			return
		}
		fromSeq = parsed
	}

	// Check if client supports SSE
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	// Send initial events
	events, err := s.fileStore.Read(fromSeq)
	if err == nil {
		for _, event := range events {
			if err := s.sendSSEEvent(w, event); err != nil {
				return
			}
			flusher.Flush()
			if event.Seq >= fromSeq {
				fromSeq = event.Seq + 1
			}
		}
	}

	// Subscribe for new events
	ctx := r.Context()
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	// Send keepalive comments periodically
	keepaliveTicker := time.NewTicker(15 * time.Second)
	defer keepaliveTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.shutdown:
			return
		case <-keepaliveTicker.C:
			// Send keepalive comment
			if _, err := fmt.Fprintf(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			events, err := s.fileStore.Read(fromSeq)
			if err != nil {
				continue
			}
			for _, event := range events {
				if err := s.sendSSEEvent(w, event); err != nil {
					return
				}
				flusher.Flush()
				if event.Seq >= fromSeq {
					fromSeq = event.Seq + 1
				}
			}
		}
	}
}

// sendSSEEvent sends a single event in SSE format.
func (s *Server) sendSSEEvent(w http.ResponseWriter, event *stream.Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Seq, event.Type, data)
	return err
}

// handleCommand receives commands from clients.
// POST /command
func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check authentication
	if !s.authenticate(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Parse command from request body
	var cmd stream.Command
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		http.Error(w, fmt.Sprintf("Invalid command JSON: %v", err), http.StatusBadRequest)
		return
	}

	// Validate command
	if cmd.ID == "" {
		http.Error(w, "Command ID is required", http.StatusBadRequest)
		return
	}
	if cmd.Type == "" {
		http.Error(w, "Command type is required", http.StatusBadRequest)
		return
	}

	// Process command via CommandProcessor if available
	if s.commandProcessor != nil {
		if err := s.commandProcessor.ProcessCommand(&cmd); err != nil {
			// Error ack was already published by CommandProcessor
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{
				"status":     "accepted",
				"command_id": cmd.ID,
				"note":       "Command processing failed, check ack in stream",
			})
			return
		}
	} else {
		// Fall back to sending directly to loop's command channel
		if s.loop != nil {
			select {
			case s.loop.CommandCh() <- &cmd:
			default:
				http.Error(w, "Command channel full", http.StatusServiceUnavailable)
				return
			}
		}
	}

	// Return accepted status
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{
		"status":     "accepted",
		"command_id": cmd.ID,
	})
}

// handleState returns the current state snapshot.
// GET /state
func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check authentication
	if !s.authenticate(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Build state snapshot from recent events
	state := s.buildStateSnapshot()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(state)
}

// StateSnapshot represents the current state of the session.
type StateSnapshot struct {
	LastSeq   uint64                 `json:"last_seq"`
	Session   *stream.SessionEvent   `json:"session,omitempty"`
	Tasks     []*stream.TaskEvent    `json:"tasks,omitempty"`
	LastInput *stream.InputRequestEvent `json:"last_input,omitempty"`
}

// buildStateSnapshot constructs a state snapshot from the FileStore.
func (s *Server) buildStateSnapshot() *StateSnapshot {
	snapshot := &StateSnapshot{
		LastSeq: s.fileStore.LastSeq(),
		Tasks:   []*stream.TaskEvent{},
	}

	// Read all events
	events, err := s.fileStore.Read(0)
	if err != nil {
		return snapshot
	}

	// Track tasks by order (later updates override earlier)
	taskByOrder := make(map[int]*stream.TaskEvent)

	for _, event := range events {
		switch event.Type {
		case stream.MessageTypeSession:
			session, err := event.SessionData()
			if err == nil {
				snapshot.Session = session
			}
		case stream.MessageTypeTask:
			task, err := event.TaskData()
			if err == nil {
				taskByOrder[task.Order] = task
			}
		case stream.MessageTypeInputRequest:
			input, err := event.InputRequestData()
			if err == nil {
				// In State Protocol, presence means it's pending
				snapshot.LastInput = input
			}
		case stream.MessageTypeInputResponse:
			// Response received - clear pending input
			snapshot.LastInput = nil
		}
	}

	// Convert task map to slice, sorted by order
	for i := 0; i < len(taskByOrder); i++ {
		if task, ok := taskByOrder[i]; ok {
			snapshot.Tasks = append(snapshot.Tasks, task)
		}
	}

	return snapshot
}

// handleHealth returns a simple health check response.
// GET /health
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":   "ok",
		"last_seq": s.fileStore.LastSeq(),
		"port":     s.port,
	})
}

// authenticate checks the request for valid authentication.
// If no token is configured, all requests are allowed.
func (s *Server) authenticate(r *http.Request) bool {
	if s.token == "" {
		return true
	}

	// Check Bearer token in Authorization header
	auth := r.Header.Get("Authorization")
	if auth == "" {
		// Also check query parameter as fallback for SSE
		token := r.URL.Query().Get("token")
		return token == s.token
	}

	// Expect "Bearer <token>"
	if len(auth) > 7 && auth[:7] == "Bearer " {
		return auth[7:] == s.token
	}

	return false
}
