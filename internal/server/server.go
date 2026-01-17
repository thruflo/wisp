// Package server provides a web server for remote monitoring and interaction
// with wisp sessions. It serves a React web client and exposes endpoints for
// authentication, state streaming via Durable Streams, and user input.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

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

	return &Server{
		port:         cfg.Port,
		passwordHash: cfg.PasswordHash,
		tokens:       make(map[string]time.Time),
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

	s.started = false
	return nil
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
	mux.HandleFunc("/auth", s.handleAuth)
	// Additional routes will be added in future tasks:
	// mux.HandleFunc("/stream", s.handleStream)
	// mux.HandleFunc("/input", s.handleInput)
	// mux.HandleFunc("/", s.handleStatic)
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
