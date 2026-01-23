package spriteloop

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/thruflo/wisp/internal/stream"
)

const (
	// DefaultServerPort is the default port for the HTTP server.
	DefaultServerPort = 8374

	// DefaultPollInterval is the default interval for polling the FileStore.
	DefaultPollInterval = 100 * time.Millisecond

	// DefaultStreamPath is the path for the durable-streams endpoint.
	DefaultStreamPath = "/wisp/events"

	// Durable-streams protocol headers.
	headerStreamNextOffset = "Stream-Next-Offset"
	headerStreamUpToDate   = "Stream-Up-To-Date"
)

// Server provides an HTTP server implementing the durable-streams protocol.
// It runs on the Sprite VM and serves as the communication endpoint for
// TUI and web clients.
//
// Endpoints:
//   - GET /wisp/events - Durable streams read (supports offset, live=sse, live=long-poll)
//   - POST /wisp/events - Durable streams append (for commands and input responses)
//   - HEAD /wisp/events - Stream metadata
//   - GET /health - Health check
type Server struct {
	// Configuration
	port         int
	streamPath   string
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
	StreamPath       string
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

	streamPath := opts.StreamPath
	if streamPath == "" {
		streamPath = DefaultStreamPath
	}

	pollInterval := opts.PollInterval
	if pollInterval == 0 {
		pollInterval = DefaultPollInterval
	}

	return &Server{
		port:             port,
		streamPath:       streamPath,
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
	// Durable-streams endpoint - handles GET (read/subscribe), POST (append), HEAD (metadata)
	mux.HandleFunc(s.streamPath, s.handleStreamEndpoint)
	// Health check endpoint
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

// StreamPath returns the path for the stream endpoint.
func (s *Server) StreamPath() string {
	return s.streamPath
}

// Running returns whether the server is currently running.
func (s *Server) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// handleStreamEndpoint implements the durable-streams HTTP protocol.
// - GET: Read events with optional live streaming (SSE or long-poll)
// - POST: Append events to the stream (commands, input responses)
// - HEAD: Return stream metadata
func (s *Server) handleStreamEndpoint(w http.ResponseWriter, r *http.Request) {
	// Check authentication
	if !s.authenticate(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.handleStreamRead(w, r)
	case http.MethodPost:
		s.handleStreamAppend(w, r)
	case http.MethodHead:
		s.handleStreamHead(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleStreamRead handles GET requests for reading from the stream.
// Supports durable-streams protocol:
//   - ?offset=X - Start reading from offset (default: 0_0 for beginning)
//   - ?offset=now - Start from current tail
//   - ?live=sse - Server-Sent Events streaming
//   - ?live=long-poll - Long polling for new messages
func (s *Server) handleStreamRead(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	// Parse offset
	offsetStr := query.Get("offset")
	fromSeq := uint64(0)
	isNow := false

	if offsetStr == "" || offsetStr == "0_0" {
		fromSeq = 0
	} else if offsetStr == "now" {
		isNow = true
		fromSeq = s.fileStore.LastSeq() + 1
	} else {
		// Parse offset format "readseq_byteoffset" - we use byte offset as sequence
		parts := strings.Split(offsetStr, "_")
		if len(parts) == 2 {
			var seq uint64
			fmt.Sscanf(parts[1], "%d", &seq)
			fromSeq = seq
		}
	}

	liveMode := query.Get("live")

	// Handle SSE mode
	if liveMode == "sse" {
		s.handleSSE(w, r, fromSeq)
		return
	}

	// Read available messages
	events, err := s.fileStore.Read(fromSeq)
	if err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	// Handle long-poll mode - wait if no messages and not at "now"
	if liveMode == "long-poll" && len(events) == 0 && !isNow {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		// Subscribe and wait for new events
		eventCh, err := s.fileStore.Subscribe(ctx, fromSeq, s.pollInterval)
		if err == nil {
			select {
			case event := <-eventCh:
				if event != nil {
					events = []*stream.Event{event}
				}
			case <-ctx.Done():
				// Timeout - return empty response
			}
		}
	}

	// Calculate next offset
	lastSeq := s.fileStore.LastSeq()
	nextOffset := formatOffset(lastSeq)
	if len(events) > 0 {
		nextOffset = formatOffset(events[len(events)-1].Seq)
	}

	// Set response headers
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(headerStreamNextOffset, nextOffset)
	if len(events) == 0 || (len(events) > 0 && events[len(events)-1].Seq >= lastSeq) {
		w.Header().Set(headerStreamUpToDate, "true")
	}

	// Format response as JSON array
	body := formatEventsAsJSON(events)
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

// handleSSE handles Server-Sent Events streaming per durable-streams protocol.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request, fromSeq uint64) {
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

	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx := r.Context()
	currentSeq := fromSeq
	sentInitialControl := false

	// Reconnect timeout (60 seconds)
	reconnectTimer := time.NewTimer(60 * time.Second)
	defer reconnectTimer.Stop()

	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.shutdown:
			return
		case <-reconnectTimer.C:
			return
		case <-ticker.C:
			events, err := s.fileStore.Read(currentSeq)
			if err != nil {
				continue
			}

			if len(events) > 0 {
				// Send data event with JSON array
				body := formatEventsAsJSON(events)
				fmt.Fprintf(w, "event: data\n")
				for _, line := range strings.Split(string(body), "\n") {
					fmt.Fprintf(w, "data:%s\n", line)
				}
				fmt.Fprintf(w, "\n")

				// Update current sequence
				currentSeq = events[len(events)-1].Seq + 1

				// Send control event
				lastSeq := s.fileStore.LastSeq()
				control := map[string]interface{}{
					"streamNextOffset": formatOffset(events[len(events)-1].Seq),
				}
				if currentSeq > lastSeq {
					control["upToDate"] = true
				}
				controlJSON, _ := json.Marshal(control)
				fmt.Fprintf(w, "event: control\n")
				fmt.Fprintf(w, "data:%s\n\n", controlJSON)

				flusher.Flush()
				sentInitialControl = true
			} else if !sentInitialControl {
				// Send initial control event showing current position
				lastSeq := s.fileStore.LastSeq()
				control := map[string]interface{}{
					"streamNextOffset": formatOffset(lastSeq),
					"upToDate":         true,
				}
				controlJSON, _ := json.Marshal(control)
				fmt.Fprintf(w, "event: control\n")
				fmt.Fprintf(w, "data:%s\n\n", controlJSON)

				flusher.Flush()
				sentInitialControl = true
			}
		}
	}
}

// handleStreamAppend handles POST requests to append events to the stream.
// Per durable-streams protocol, events are appended and acknowledged.
func (s *Server) handleStreamAppend(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	// Try to unmarshal as a stream event
	event, err := stream.UnmarshalEvent(body)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid event JSON: %v", err), http.StatusBadRequest)
		return
	}

	// Process based on event type
	switch event.Type {
	case stream.MessageTypeCommand:
		cmd, err := event.CommandData()
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid command data: %v", err), http.StatusBadRequest)
			return
		}

		// Append command to stream (CommandProcessor watches the stream)
		if err := s.fileStore.Append(event); err != nil {
			http.Error(w, fmt.Sprintf("Failed to append event: %v", err), http.StatusInternalServerError)
			return
		}

		// Return success with next offset
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(headerStreamNextOffset, formatOffset(event.Seq))
		w.WriteHeader(http.StatusNoContent)

		// Note: CommandProcessor will process the command and publish an ack
		_ = cmd // cmd is processed asynchronously via stream subscription

	case stream.MessageTypeInputResponse:
		// Append input response to stream
		if err := s.fileStore.Append(event); err != nil {
			http.Error(w, fmt.Sprintf("Failed to append event: %v", err), http.StatusInternalServerError)
			return
		}

		// Return success with next offset
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(headerStreamNextOffset, formatOffset(event.Seq))
		w.WriteHeader(http.StatusNoContent)

	default:
		// For other event types, just append to stream
		if err := s.fileStore.Append(event); err != nil {
			http.Error(w, fmt.Sprintf("Failed to append event: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set(headerStreamNextOffset, formatOffset(event.Seq))
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleStreamHead handles HEAD requests for stream metadata.
func (s *Server) handleStreamHead(w http.ResponseWriter, r *http.Request) {
	lastSeq := s.fileStore.LastSeq()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(headerStreamNextOffset, formatOffset(lastSeq))
	w.WriteHeader(http.StatusOK)
}

// formatOffset formats a sequence number as a durable-streams offset string.
// Format: "readseq_byteoffset" - we use seq for both since we don't track byte offsets.
func formatOffset(seq uint64) string {
	return fmt.Sprintf("%d_%d", seq, seq)
}

// formatEventsAsJSON formats events as a JSON array.
func formatEventsAsJSON(events []*stream.Event) []byte {
	if len(events) == 0 {
		return []byte("[]")
	}

	var result []json.RawMessage
	for _, event := range events {
		data, err := event.Marshal()
		if err != nil {
			continue
		}
		result = append(result, data)
	}

	body, _ := json.Marshal(result)
	return body
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
