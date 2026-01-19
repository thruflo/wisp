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
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/durable-streams/durable-streams/packages/caddy-plugin/store"
	"github.com/thruflo/wisp/internal/auth"
	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/web"
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

	// Input handling
	inputMu          sync.Mutex
	pendingInputs    map[string]string // request_id -> response
	respondedInputs  map[string]bool   // request_id -> true if already responded

	// Static assets filesystem
	assets fs.FS

	// Lifecycle
	started bool
}

// Config holds server configuration options.
type Config struct {
	Port         int
	PasswordHash string
	Assets       fs.FS // Optional: static assets filesystem. If nil, uses embedded assets.
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

	// Use provided assets or default to embedded web assets
	assets := cfg.Assets
	if assets == nil {
		assets = web.GetAssets("")
	}

	return &Server{
		port:         cfg.Port,
		passwordHash: cfg.PasswordHash,
		tokens:       make(map[string]time.Time),
		streams:      streams,
		assets:       assets,
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
// Implements first-response-wins: if the request has already been responded to
// (either from web or TUI), subsequent responses are rejected.
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

	// Use inputMu for input-specific operations (first-response-wins)
	s.inputMu.Lock()

	// Check if this request has already been responded to
	if s.respondedInputs != nil && s.respondedInputs[req.RequestID] {
		s.inputMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		fmt.Fprintf(w, `{"status":"already_responded"}`)
		return
	}

	// Initialize maps if needed
	if s.pendingInputs == nil {
		s.pendingInputs = make(map[string]string)
	}
	if s.respondedInputs == nil {
		s.respondedInputs = make(map[string]bool)
	}

	// Mark as responded and store the response
	s.respondedInputs[req.RequestID] = true
	s.pendingInputs[req.RequestID] = req.Response
	s.inputMu.Unlock()

	// Broadcast that this input request has been responded to
	// This allows web clients to see the updated state immediately
	if s.streams != nil {
		inputReq := &InputRequest{
			ID:        req.RequestID,
			Responded: true,
			Response:  &req.Response,
		}
		s.streams.BroadcastInputRequest(inputReq)
	}

	// Return success
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"received"}`)
}

// GetPendingInput retrieves and removes a pending input response.
// This is called by the loop when polling for web client input.
func (s *Server) GetPendingInput(requestID string) (string, bool) {
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	if s.pendingInputs == nil {
		return "", false
	}
	response, ok := s.pendingInputs[requestID]
	if ok {
		delete(s.pendingInputs, requestID)
	}
	return response, ok
}

// MarkInputResponded marks an input request as responded.
// This is called by the loop when the TUI provides input, to prevent
// subsequent web client responses from being accepted.
func (s *Server) MarkInputResponded(requestID string) {
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	if s.respondedInputs == nil {
		s.respondedInputs = make(map[string]bool)
	}
	s.respondedInputs[requestID] = true
}

// IsInputResponded checks if an input request has already been responded to.
func (s *Server) IsInputResponded(requestID string) bool {
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	if s.respondedInputs == nil {
		return false
	}
	return s.respondedInputs[requestID]
}

// handleStatic handles GET requests for serving static web assets.
// It serves files from the embedded or development assets filesystem.
// For SPA support, requests for non-existent paths return index.html.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Clean the path and remove leading slash
	path := r.URL.Path
	if path == "/" {
		path = "index.html"
	} else {
		path = strings.TrimPrefix(path, "/")
	}

	// Try to open the requested file
	file, err := s.assets.Open(path)
	if err != nil {
		// File not found - for SPA support, serve index.html for HTML requests
		// This allows client-side routing to work
		if strings.Contains(r.Header.Get("Accept"), "text/html") || path == "" {
			s.serveFile(w, r, "index.html")
			return
		}
		http.NotFound(w, r)
		return
	}
	file.Close()

	// Serve the file
	s.serveFile(w, r, path)
}

// serveFile serves a file from the assets filesystem.
func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, path string) {
	file, err := s.assets.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()

	// Get file info for size and modification time
	stat, err := file.Stat()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Don't serve directories
	if stat.IsDir() {
		// Try index.html in the directory
		indexPath := path + "/index.html"
		if path == "" || path == "." {
			indexPath = "index.html"
		}
		s.serveFile(w, r, indexPath)
		return
	}

	// Set content type based on extension
	contentType := getContentType(path)
	w.Header().Set("Content-Type", contentType)

	// Set cache headers for static assets
	if isImmutableAsset(path) {
		// Hashed assets can be cached indefinitely
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		// HTML and other files should be revalidated
		w.Header().Set("Cache-Control", "no-cache")
	}

	// Read and serve the file content
	content, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.ServeContent(w, r, path, stat.ModTime(), strings.NewReader(string(content)))
}

// getContentType returns the MIME type for a file based on its extension.
func getContentType(path string) string {
	// Common web asset types
	switch {
	case strings.HasSuffix(path, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(path, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(path, ".js"):
		return "application/javascript; charset=utf-8"
	case strings.HasSuffix(path, ".json"):
		return "application/json; charset=utf-8"
	case strings.HasSuffix(path, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(path, ".png"):
		return "image/png"
	case strings.HasSuffix(path, ".jpg"), strings.HasSuffix(path, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(path, ".gif"):
		return "image/gif"
	case strings.HasSuffix(path, ".ico"):
		return "image/x-icon"
	case strings.HasSuffix(path, ".woff"):
		return "font/woff"
	case strings.HasSuffix(path, ".woff2"):
		return "font/woff2"
	case strings.HasSuffix(path, ".ttf"):
		return "font/ttf"
	case strings.HasSuffix(path, ".webp"):
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

// isImmutableAsset returns true if the asset path looks like a hashed asset
// that can be cached indefinitely (e.g., main.abc123.js, index-BcD123.js).
func isImmutableAsset(path string) bool {
	// Get the filename without extension
	// Vite-style: index-BcD123.js (hash after hyphen)
	// Webpack-style: index.abc123.js (hash as middle segment)

	// Get just the filename
	lastSlash := strings.LastIndex(path, "/")
	if lastSlash >= 0 {
		path = path[lastSlash+1:]
	}

	// Remove extension
	dotIdx := strings.LastIndex(path, ".")
	if dotIdx <= 0 {
		return false
	}
	nameWithoutExt := path[:dotIdx]

	// Check for Vite-style: name-HASH (hyphen separator)
	hyphenIdx := strings.LastIndex(nameWithoutExt, "-")
	if hyphenIdx > 0 {
		hash := nameWithoutExt[hyphenIdx+1:]
		if len(hash) >= 6 && len(hash) <= 16 && isAlphanumeric(hash) {
			return true
		}
	}

	// Check for Webpack-style: name.HASH (dot separator with 3+ parts)
	parts := strings.Split(nameWithoutExt, ".")
	if len(parts) >= 2 {
		hash := parts[len(parts)-1]
		if len(hash) >= 6 && len(hash) <= 16 && isAlphanumeric(hash) {
			return true
		}
	}

	return false
}

// isAlphanumeric returns true if the string contains only alphanumeric characters.
func isAlphanumeric(s string) bool {
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return len(s) > 0
}

// handleAuth handles POST /auth for password authentication.
func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
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
		Password string `json:"password"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Password == "" {
		http.Error(w, "password required", http.StatusBadRequest)
		return
	}

	password := req.Password

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
