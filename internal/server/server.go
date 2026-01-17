// Package server provides a web server for remote monitoring and interaction
// with wisp sessions. It serves a React web client and exposes endpoints for
// authentication, state streaming via Durable Streams, and user input.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/durable-streams/durable-streams/packages/caddy-plugin/store"
	"github.com/thruflo/wisp/internal/auth"
	"github.com/thruflo/wisp/internal/config"
)

// Server represents the web server for remote access to wisp sessions.
type Server struct {
	port         int
	passwordHash string

	// HTTP server
	server   *http.Server
	listener net.Listener

	// Token management
	mu     sync.RWMutex
	tokens map[string]time.Time // token -> expiry time

	// Durable Streams
	streams *StreamManager

	// Pending user inputs from web client
	pendingInputs map[string]string // request_id -> response

	// Lifecycle
	started bool
}

// Config holds server configuration options.
type Config struct {
	Port         int
	PasswordHash string
}

// NewServer creates a new Server instance.
func NewServer(cfg *Config) (*Server, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}
	if cfg.PasswordHash == "" {
		return nil, errors.New("password hash is required")
	}

	streams, err := NewStreamManager()
	if err != nil {
		return nil, fmt.Errorf("failed to create stream manager: %w", err)
	}

	return &Server{
		port:         cfg.Port,
		passwordHash: cfg.PasswordHash,
		tokens:       make(map[string]time.Time),
		streams:      streams,
	}, nil
}

// NewServerFromConfig creates a new Server from a config.ServerConfig.
func NewServerFromConfig(cfg *config.ServerConfig) (*Server, error) {
	if cfg == nil {
		return nil, errors.New("server config is required")
	}
	return NewServer(&Config{
		Port:         cfg.Port,
		PasswordHash: cfg.PasswordHash,
	})
}

// Port returns the configured port.
func (s *Server) Port() int {
	return s.port
}

// Start starts the HTTP server.
// The server runs until ctx is cancelled or Stop is called.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return errors.New("server already started")
	}

	// Create listener
	addr := fmt.Sprintf(":%d", s.port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	s.listener = listener

	// Setup HTTP server with routes
	mux := http.NewServeMux()
	s.setupRoutes(mux)

	s.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	s.started = true
	s.mu.Unlock()

	// Start cleanup goroutine for expired tokens
	go s.cleanupExpiredTokens(ctx)

	// Run server (blocks until error or server closed)
	err = s.server.Serve(listener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("server error: %w", err)
	}

	return nil
}

// Stop gracefully shuts down the server.
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started || s.server == nil {
		return nil
	}

	// Shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown error: %w", err)
	}

	// Close the stream manager
	if s.streams != nil {
		s.streams.Close()
	}

	s.started = false
	return nil
}

// Streams returns the StreamManager for broadcasting state.
func (s *Server) Streams() *StreamManager {
	return s.streams
}

// ListenAddr returns the actual address the server is listening on.
// Useful when port 0 is used to get an available port.
// Returns empty string if not started.
func (s *Server) ListenAddr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

// setupRoutes configures the HTTP routes.
func (s *Server) setupRoutes(mux *http.ServeMux) {
	// Public endpoint
	mux.HandleFunc("/auth", s.handleAuth)

	// Protected endpoints
	mux.HandleFunc("/stream", s.withAuth(s.handleStream))
	mux.HandleFunc("/input", s.withAuth(s.handleInput))
	mux.HandleFunc("/", s.handleStatic) // Static assets are public for initial page load
}

// withAuth wraps a handler with authentication middleware.
func (s *Server) withAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Extract token from Authorization header
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "authorization required", http.StatusUnauthorized)
			return
		}

		// Expect "Bearer <token>" format
		const bearerPrefix = "Bearer "
		if !strings.HasPrefix(authHeader, bearerPrefix) {
			http.Error(w, "invalid authorization format", http.StatusUnauthorized)
			return
		}

		token := strings.TrimPrefix(authHeader, bearerPrefix)
		if !s.ValidateToken(token) {
			http.Error(w, "invalid or expired token", http.StatusUnauthorized)
			return
		}

		handler(w, r)
	}
}

// VerifyPassword checks if the provided password matches the stored hash.
func (s *Server) VerifyPassword(password string) (bool, error) {
	return auth.VerifyPassword(password, s.passwordHash)
}

// tokenExpiry is how long tokens are valid.
const tokenExpiry = 24 * time.Hour

// GenerateToken creates a new authentication token.
func (s *Server) GenerateToken() (string, error) {
	// Generate 32 bytes of random data (256 bits)
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate token: %w", err)
	}

	token := hex.EncodeToString(bytes)

	// Store token with expiry
	s.mu.Lock()
	s.tokens[token] = time.Now().Add(tokenExpiry)
	s.mu.Unlock()

	return token, nil
}

// ValidateToken checks if a token is valid and not expired.
func (s *Server) ValidateToken(token string) bool {
	if token == "" {
		return false
	}

	s.mu.RLock()
	expiry, exists := s.tokens[token]
	s.mu.RUnlock()

	if !exists {
		return false
	}

	return time.Now().Before(expiry)
}

// RevokeToken removes a token from the valid tokens map.
func (s *Server) RevokeToken(token string) {
	s.mu.Lock()
	delete(s.tokens, token)
	s.mu.Unlock()
}

// cleanupExpiredTokens periodically removes expired tokens.
func (s *Server) cleanupExpiredTokens(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			now := time.Now()
			for token, expiry := range s.tokens {
				if now.After(expiry) {
					delete(s.tokens, token)
				}
			}
			s.mu.Unlock()
		}
	}
}

// Protocol header names for Durable Streams
const (
	headerStreamNextOffset = "Stream-Next-Offset"
	headerStreamUpToDate   = "Stream-Up-To-Date"
	headerStreamCursor     = "Stream-Cursor"
)

// handleStream handles GET /stream for Durable Streams.
// It supports catch-up, long-poll, and SSE modes.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Set CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Expose-Headers", "Stream-Next-Offset, Stream-Up-To-Date, Stream-Cursor")

	// Get stream path
	path := s.streams.StreamPath()

	// Parse offset from query
	query := r.URL.Query()
	offsetStr := query.Get("offset")

	var offset store.Offset
	var err error
	if offsetStr == "" {
		offset = store.Offset{} // Start from beginning
	} else {
		offset, err = store.ParseOffset(offsetStr)
		if err != nil {
			http.Error(w, "invalid offset", http.StatusBadRequest)
			return
		}
	}

	// Check for live mode
	liveMode := query.Get("live")

	// Get current offset to check if we're at the tail
	currentOffset, err := s.streams.GetCurrentOffset()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Handle offset=now
	if offset.IsNow() {
		offset = currentOffset
	}

	// Handle SSE mode
	if liveMode == "sse" {
		s.handleSSE(w, r, path, offset)
		return
	}

	// Read messages
	messages, upToDate, err := s.streams.Read(offset)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Handle long-poll mode
	if liveMode == "long-poll" && len(messages) == 0 {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		messages, upToDate, err = s.streams.WaitForMessages(ctx, offset, 30*time.Second)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				// Timeout - return 204 with current offset
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set(headerStreamNextOffset, offset.String())
				w.Header().Set(headerStreamUpToDate, "true")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	// Calculate next offset
	nextOffset := offset
	if len(messages) > 0 {
		nextOffset = messages[len(messages)-1].Offset
	} else {
		nextOffset = currentOffset
	}

	// Set response headers
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(headerStreamNextOffset, nextOffset.String())
	if upToDate || len(messages) == 0 {
		w.Header().Set(headerStreamUpToDate, "true")
	}

	// Format response as JSON array
	body := formatJSONResponse(messages)
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

// handleSSE handles Server-Sent Events streaming.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request, path string, offset store.Offset) {
	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx := r.Context()
	currentOffset := offset
	sentInitialControl := false

	// Reconnect interval (60 seconds)
	reconnectTimer := time.NewTimer(60 * time.Second)
	defer reconnectTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-reconnectTimer.C:
			return
		default:
			// Read any available messages
			messages, upToDate, err := s.streams.Read(currentOffset)
			if err != nil {
				return
			}

			if len(messages) > 0 {
				// Send data event
				body := formatJSONResponse(messages)
				fmt.Fprintf(w, "event: data\n")
				// Handle line terminators for SSE safety
				for _, line := range strings.Split(string(body), "\n") {
					fmt.Fprintf(w, "data:%s\n", line)
				}
				fmt.Fprintf(w, "\n")

				// Update current offset
				currentOffset = messages[len(messages)-1].Offset

				// Send control event
				control := map[string]interface{}{
					"streamNextOffset": currentOffset.String(),
				}
				if upToDate {
					control["upToDate"] = true
				}
				controlJSON, _ := json.Marshal(control)
				fmt.Fprintf(w, "event: control\n")
				fmt.Fprintf(w, "data:%s\n\n", controlJSON)

				flusher.Flush()
				sentInitialControl = true
			} else if !sentInitialControl {
				// Send initial control event
				tailOffset, _ := s.streams.GetCurrentOffset()
				control := map[string]interface{}{
					"streamNextOffset": tailOffset.String(),
					"upToDate":         true,
				}
				controlJSON, _ := json.Marshal(control)
				fmt.Fprintf(w, "event: control\n")
				fmt.Fprintf(w, "data:%s\n\n", controlJSON)

				flusher.Flush()
				sentInitialControl = true
			}

			// Wait for more data (100ms polling)
			waitCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			s.streams.WaitForMessages(waitCtx, currentOffset, 100*time.Millisecond)
			cancel()
		}
	}
}

// formatJSONResponse formats messages as a JSON array.
func formatJSONResponse(messages []store.Message) []byte {
	if len(messages) == 0 {
		return []byte("[]")
	}
	return store.FormatJSONResponse(messages)
}

// handleInput handles POST /input for user responses.
func (s *Server) handleInput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse JSON body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	var req struct {
		RequestID string `json:"request_id"`
		Response  string `json:"response"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if req.RequestID == "" {
		http.Error(w, "request_id is required", http.StatusBadRequest)
		return
	}

	// Store the input response for the session to handle
	// Note: Actual implementation of writing to response.json will be done
	// in the loop integration task. For now, we just acknowledge receipt.
	s.mu.Lock()
	if s.pendingInputs == nil {
		s.pendingInputs = make(map[string]string)
	}
	s.pendingInputs[req.RequestID] = req.Response
	s.mu.Unlock()

	// Return success
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"received"}`)
}

// GetPendingInput retrieves and removes a pending input response.
func (s *Server) GetPendingInput(requestID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingInputs == nil {
		return "", false
	}
	response, ok := s.pendingInputs[requestID]
	if ok {
		delete(s.pendingInputs, requestID)
	}
	return response, ok
}

// handleStatic handles GET / for serving static web assets.
// For now, returns a simple placeholder. Asset embedding will be done in the next task.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	// For now, return a simple HTML page indicating the web client is not yet built
	if r.URL.Path == "/" || r.URL.Path == "/index.html" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<!DOCTYPE html>
<html>
<head><title>Wisp</title></head>
<body>
<h1>Wisp Remote Access</h1>
<p>Web client not yet built. This is a placeholder.</p>
</body>
</html>`))
		return
	}

	// Return 404 for other paths
	http.NotFound(w, r)
}

// handleAuth handles POST /auth for password authentication.
func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse form data
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	password := r.FormValue("password")
	if password == "" {
		http.Error(w, "password required", http.StatusBadRequest)
		return
	}

	// Verify password
	valid, err := s.VerifyPassword(password)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !valid {
		http.Error(w, "invalid password", http.StatusUnauthorized)
		return
	}

	// Generate token
	token, err := s.GenerateToken()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Return token as JSON
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"token":"%s"}`, token)
}
