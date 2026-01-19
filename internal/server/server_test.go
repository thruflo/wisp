package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thruflo/wisp/internal/auth"
	"github.com/thruflo/wisp/internal/config"
)

const testPassword = "test-password-123"

// createTestServer creates a server with a known password hash for testing.
func createTestServer(t *testing.T) *Server {
	t.Helper()

	hash, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}

	server, err := NewServer(&Config{
		Port:         0, // random available port
		PasswordHash: hash,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	return server
}

func TestNewServer(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr string
	}{
		{
			name:    "nil config",
			cfg:     nil,
			wantErr: "config is required",
		},
		{
			name: "empty password hash",
			cfg: &Config{
				Port:         8080,
				PasswordHash: "",
			},
			wantErr: "password hash is required",
		},
		{
			name: "valid config",
			cfg: &Config{
				Port:         8080,
				PasswordHash: "$argon2id$v=19$m=65536,t=3,p=4$test$hash",
			},
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, err := NewServer(tt.cfg)
			if tt.wantErr != "" {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.wantErr)
					return
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got %q", tt.wantErr, err.Error())
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if server == nil {
				t.Error("expected server, got nil")
				return
			}
			if server.Port() != tt.cfg.Port {
				t.Errorf("expected port %d, got %d", tt.cfg.Port, server.Port())
			}
		})
	}
}

func TestNewServerFromConfig(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		_, err := NewServerFromConfig(nil)
		if err == nil {
			t.Error("expected error for nil config")
		}
	})

	t.Run("valid config", func(t *testing.T) {
		cfg := &config.ServerConfig{
			Port:         8374,
			PasswordHash: "$argon2id$v=19$m=65536,t=3,p=4$test$hash",
		}
		server, err := NewServerFromConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if server.Port() != 8374 {
			t.Errorf("expected port 8374, got %d", server.Port())
		}
	})
}

func TestVerifyPassword(t *testing.T) {
	server := createTestServer(t)

	t.Run("correct password", func(t *testing.T) {
		valid, err := server.VerifyPassword(testPassword)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !valid {
			t.Error("expected password to be valid")
		}
	})

	t.Run("wrong password", func(t *testing.T) {
		valid, err := server.VerifyPassword("wrong-password")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if valid {
			t.Error("expected password to be invalid")
		}
	})

	t.Run("empty password", func(t *testing.T) {
		valid, err := server.VerifyPassword("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if valid {
			t.Error("expected empty password to be invalid")
		}
	})
}

func TestGenerateToken(t *testing.T) {
	server := createTestServer(t)

	t.Run("generates token", func(t *testing.T) {
		token, err := server.GenerateToken()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if token == "" {
			t.Error("expected non-empty token")
		}
		// Token should be 64 hex characters (32 bytes)
		if len(token) != 64 {
			t.Errorf("expected token length 64, got %d", len(token))
		}
	})

	t.Run("tokens are unique", func(t *testing.T) {
		token1, _ := server.GenerateToken()
		token2, _ := server.GenerateToken()
		if token1 == token2 {
			t.Error("expected unique tokens")
		}
	})

	t.Run("token is valid after generation", func(t *testing.T) {
		token, _ := server.GenerateToken()
		if !server.ValidateToken(token) {
			t.Error("expected generated token to be valid")
		}
	})
}

func TestValidateToken(t *testing.T) {
	server := createTestServer(t)

	t.Run("empty token", func(t *testing.T) {
		if server.ValidateToken("") {
			t.Error("expected empty token to be invalid")
		}
	})

	t.Run("non-existent token", func(t *testing.T) {
		if server.ValidateToken("non-existent-token") {
			t.Error("expected non-existent token to be invalid")
		}
	})

	t.Run("valid token", func(t *testing.T) {
		token, _ := server.GenerateToken()
		if !server.ValidateToken(token) {
			t.Error("expected valid token to pass validation")
		}
	})

	t.Run("revoked token", func(t *testing.T) {
		token, _ := server.GenerateToken()
		server.RevokeToken(token)
		if server.ValidateToken(token) {
			t.Error("expected revoked token to be invalid")
		}
	})
}

func TestRevokeToken(t *testing.T) {
	server := createTestServer(t)

	t.Run("revoke existing token", func(t *testing.T) {
		token, _ := server.GenerateToken()
		server.RevokeToken(token)
		if server.ValidateToken(token) {
			t.Error("expected revoked token to be invalid")
		}
	})

	t.Run("revoke non-existent token", func(t *testing.T) {
		// Should not panic
		server.RevokeToken("non-existent")
	})
}

func TestHandleAuth(t *testing.T) {
	server := createTestServer(t)

	mux := http.NewServeMux()
	server.setupRoutes(mux)

	t.Run("wrong method", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/auth", nil)
		w := httptest.NewRecorder()

		mux.ServeHTTP(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected status %d, got %d", http.StatusMethodNotAllowed, w.Code)
		}
	})

	t.Run("missing password", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/auth", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		mux.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
		}
	})

	t.Run("wrong password", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/auth", strings.NewReader(`{"password":"wrong-password"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		mux.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
		}
	})

	t.Run("correct password", func(t *testing.T) {
		body := fmt.Sprintf(`{"password":"%s"}`, testPassword)
		req := httptest.NewRequest(http.MethodPost, "/auth", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
		}

		// Parse response
		var response struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		if response.Token == "" {
			t.Error("expected non-empty token in response")
		}

		// Verify token is valid
		if !server.ValidateToken(response.Token) {
			t.Error("expected returned token to be valid")
		}
	})
}

func TestServerStartStop(t *testing.T) {
	server := createTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start server in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start(ctx)
	}()

	// Give server time to start
	time.Sleep(50 * time.Millisecond)

	// Verify server is listening
	addr := server.ListenAddr()
	if addr == "" {
		t.Fatal("expected server to be listening")
	}

	// Make a request
	resp, err := http.Get("http://" + addr + "/auth")
	if err != nil {
		t.Fatalf("failed to make request: %v", err)
	}
	resp.Body.Close()

	// Status should be 405 (method not allowed for GET)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected status %d, got %d", http.StatusMethodNotAllowed, resp.StatusCode)
	}

	// Stop server
	if err := server.Stop(); err != nil {
		t.Fatalf("failed to stop server: %v", err)
	}

	// Server should have exited cleanly
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("server returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("server did not exit in time")
	}
}

func TestServerDoubleStart(t *testing.T) {
	server := createTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start server
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start(ctx)
	}()

	time.Sleep(50 * time.Millisecond)

	// Try to start again
	err := server.Start(ctx)
	if err == nil {
		t.Error("expected error when starting already-started server")
	}
	if !strings.Contains(err.Error(), "already started") {
		t.Errorf("expected 'already started' error, got: %v", err)
	}

	// Cleanup
	server.Stop()
}

func TestServerStopNotStarted(t *testing.T) {
	server := createTestServer(t)

	// Stop without starting should not error
	if err := server.Stop(); err != nil {
		t.Errorf("unexpected error stopping non-started server: %v", err)
	}
}

func TestAuthEndToEnd(t *testing.T) {
	server := createTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start server
	go func() {
		server.Start(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	defer server.Stop()

	addr := server.ListenAddr()

	// Authenticate with correct password
	body := fmt.Sprintf(`{"password":"%s"}`, testPassword)
	resp, err := http.Post("http://"+addr+"/auth", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("failed to authenticate: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
	}

	var response struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	// Token should be valid
	if !server.ValidateToken(response.Token) {
		t.Error("token should be valid")
	}

	// Authenticate with wrong password
	resp2, err := http.Post("http://"+addr+"/auth", "application/json", strings.NewReader(`{"password":"wrong-password"}`))
	if err != nil {
		t.Fatalf("failed to make request: %v", err)
	}
	resp2.Body.Close()

	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected status %d for wrong password, got %d", http.StatusUnauthorized, resp2.StatusCode)
	}
}

// Helper to get an authenticated token for tests
func getAuthToken(t *testing.T, addr string) string {
	t.Helper()
	body := fmt.Sprintf(`{"password":"%s"}`, testPassword)
	resp, err := http.Post("http://"+addr+"/auth", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("failed to authenticate: %v", err)
	}
	defer resp.Body.Close()

	var response struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	return response.Token
}

func TestAuthMiddleware(t *testing.T) {
	server := createTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		server.Start(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	defer server.Stop()

	addr := server.ListenAddr()

	t.Run("no auth header", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "http://"+addr+"/stream", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to make request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("expected status %d, got %d", http.StatusUnauthorized, resp.StatusCode)
		}
	})

	t.Run("invalid auth format", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "http://"+addr+"/stream", nil)
		req.Header.Set("Authorization", "Basic sometoken")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to make request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("expected status %d, got %d", http.StatusUnauthorized, resp.StatusCode)
		}
	})

	t.Run("invalid token", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "http://"+addr+"/stream", nil)
		req.Header.Set("Authorization", "Bearer invalid-token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to make request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("expected status %d, got %d", http.StatusUnauthorized, resp.StatusCode)
		}
	})

	t.Run("valid token", func(t *testing.T) {
		token := getAuthToken(t, addr)

		req, _ := http.NewRequest("GET", "http://"+addr+"/stream", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to make request: %v", err)
		}
		defer resp.Body.Close()

		// Should get through auth (200 or other non-401 status)
		if resp.StatusCode == http.StatusUnauthorized {
			t.Errorf("expected authenticated request to succeed")
		}
	})
}

func TestStreamEndpoint(t *testing.T) {
	server := createTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		server.Start(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	defer server.Stop()

	addr := server.ListenAddr()
	token := getAuthToken(t, addr)

	t.Run("wrong method", func(t *testing.T) {
		req, _ := http.NewRequest("POST", "http://"+addr+"/stream", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to make request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("expected status %d, got %d", http.StatusMethodNotAllowed, resp.StatusCode)
		}
	})

	t.Run("empty stream", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "http://"+addr+"/stream", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to make request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
		}

		// Check headers
		if resp.Header.Get("Stream-Next-Offset") == "" {
			t.Error("expected Stream-Next-Offset header")
		}
		if resp.Header.Get("Stream-Up-To-Date") != "true" {
			t.Error("expected Stream-Up-To-Date header to be true for empty stream")
		}

		// Check body is empty JSON array
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "[]" {
			t.Errorf("expected empty array, got %s", string(body))
		}
	})

	t.Run("with messages", func(t *testing.T) {
		// Broadcast a session to the stream
		session := &Session{
			ID:        "test-session",
			Repo:      "test/repo",
			Branch:    "main",
			Spec:      "spec.md",
			Status:    SessionStatusRunning,
			Iteration: 1,
			StartedAt: "2024-01-01T00:00:00Z",
		}
		if err := server.Streams().BroadcastSession(session); err != nil {
			t.Fatalf("failed to broadcast: %v", err)
		}

		req, _ := http.NewRequest("GET", "http://"+addr+"/stream", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to make request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
		}

		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "test-session") {
			t.Errorf("expected body to contain session, got %s", string(body))
		}
	})

	t.Run("invalid offset", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "http://"+addr+"/stream?offset=invalid", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to make request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected status %d, got %d", http.StatusBadRequest, resp.StatusCode)
		}
	})
}

func TestInputEndpoint(t *testing.T) {
	server := createTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		server.Start(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	defer server.Stop()

	addr := server.ListenAddr()
	token := getAuthToken(t, addr)

	t.Run("wrong method", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "http://"+addr+"/input", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to make request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("expected status %d, got %d", http.StatusMethodNotAllowed, resp.StatusCode)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		req, _ := http.NewRequest("POST", "http://"+addr+"/input", strings.NewReader("not json"))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to make request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected status %d, got %d", http.StatusBadRequest, resp.StatusCode)
		}
	})

	t.Run("missing request_id", func(t *testing.T) {
		body := `{"response": "test response"}`
		req, _ := http.NewRequest("POST", "http://"+addr+"/input", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to make request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected status %d, got %d", http.StatusBadRequest, resp.StatusCode)
		}
	})

	t.Run("valid input", func(t *testing.T) {
		body := `{"request_id": "req-123", "response": "test response"}`
		req, _ := http.NewRequest("POST", "http://"+addr+"/input", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to make request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			t.Errorf("expected status %d, got %d: %s", http.StatusOK, resp.StatusCode, string(bodyBytes))
		}

		// Verify the input was stored
		response, ok := server.GetPendingInput("req-123")
		if !ok {
			t.Error("expected pending input to be stored")
		}
		if response != "test response" {
			t.Errorf("expected response 'test response', got '%s'", response)
		}

		// Getting it again should return not found (it's been consumed)
		_, ok = server.GetPendingInput("req-123")
		if ok {
			t.Error("expected pending input to be consumed after first get")
		}
	})
}

func TestStaticEndpoint(t *testing.T) {
	server := createTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		server.Start(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	defer server.Stop()

	addr := server.ListenAddr()

	t.Run("root path", func(t *testing.T) {
		resp, err := http.Get("http://" + addr + "/")
		if err != nil {
			t.Fatalf("failed to make request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
		}

		if !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
			t.Errorf("expected HTML content type, got %s", resp.Header.Get("Content-Type"))
		}

		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), "Wisp") {
			t.Error("expected body to contain 'Wisp'")
		}
	})

	t.Run("index.html", func(t *testing.T) {
		resp, err := http.Get("http://" + addr + "/index.html")
		if err != nil {
			t.Fatalf("failed to make request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
		}
	})

	t.Run("unknown path", func(t *testing.T) {
		resp, err := http.Get("http://" + addr + "/unknown")
		if err != nil {
			t.Fatalf("failed to make request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected status %d, got %d", http.StatusNotFound, resp.StatusCode)
		}
	})
}

func TestStreamLongPoll(t *testing.T) {
	server := createTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		server.Start(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	defer server.Stop()

	addr := server.ListenAddr()
	token := getAuthToken(t, addr)

	t.Run("long-poll receives new message", func(t *testing.T) {
		// Get current offset
		req, _ := http.NewRequest("GET", "http://"+addr+"/stream?offset=now", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to make request: %v", err)
		}
		offset := resp.Header.Get("Stream-Next-Offset")
		resp.Body.Close()

		// Start long-poll in goroutine
		resultCh := make(chan int, 1)
		go func() {
			req, _ := http.NewRequest("GET", "http://"+addr+"/stream?live=long-poll&offset="+offset, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				resultCh <- -1
				return
			}
			defer resp.Body.Close()
			resultCh <- resp.StatusCode
		}()

		// Wait a bit and broadcast a message
		time.Sleep(100 * time.Millisecond)
		task := &Task{
			ID:        "task-1",
			SessionID: "test-session",
			Order:     1,
			Content:   "Test task",
			Status:    TaskStatusPending,
		}
		server.Streams().BroadcastTask(task)

		// Wait for result
		select {
		case status := <-resultCh:
			if status != http.StatusOK {
				t.Errorf("expected status %d, got %d", http.StatusOK, status)
			}
		case <-time.After(3 * time.Second):
			t.Error("long-poll did not return in time")
		}
	})
}

func TestPendingInputConcurrency(t *testing.T) {
	server := createTestServer(t)

	// Test concurrent access to pending inputs
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			reqID := fmt.Sprintf("req-%d", id)

			// Store
			server.mu.Lock()
			if server.pendingInputs == nil {
				server.pendingInputs = make(map[string]string)
			}
			server.pendingInputs[reqID] = fmt.Sprintf("response-%d", id)
			server.mu.Unlock()

			// Retrieve
			resp, ok := server.GetPendingInput(reqID)
			if !ok {
				t.Errorf("expected to find input %s", reqID)
			}
			if resp != fmt.Sprintf("response-%d", id) {
				t.Errorf("wrong response for %s", reqID)
			}
		}(i)
	}
	wg.Wait()
}

func TestInputFirstResponseWins(t *testing.T) {
	server := createTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		server.Start(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	defer server.Stop()

	addr := server.ListenAddr()
	token := getAuthToken(t, addr)

	t.Run("first response succeeds", func(t *testing.T) {
		// First response should succeed
		body := `{"request_id": "first-wins-123", "response": "first response"}`
		req, _ := http.NewRequest("POST", "http://"+addr+"/input", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to make request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
		}

		var result struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if result.Status != "received" {
			t.Errorf("expected status 'received', got '%s'", result.Status)
		}
	})

	t.Run("second response rejected with 409 Conflict", func(t *testing.T) {
		// Second response to same request_id should fail with 409 Conflict
		body := `{"request_id": "first-wins-123", "response": "second response"}`
		req, _ := http.NewRequest("POST", "http://"+addr+"/input", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to make request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusConflict {
			t.Errorf("expected status %d (Conflict), got %d", http.StatusConflict, resp.StatusCode)
		}

		var result struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if result.Status != "already_responded" {
			t.Errorf("expected status 'already_responded', got '%s'", result.Status)
		}
	})
}

func TestInputMarkResponded(t *testing.T) {
	server := createTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		server.Start(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	defer server.Stop()

	addr := server.ListenAddr()
	token := getAuthToken(t, addr)

	t.Run("MarkInputResponded blocks subsequent web input", func(t *testing.T) {
		reqID := "tui-first-123"

		// Simulate TUI responding first by calling MarkInputResponded
		server.MarkInputResponded(reqID)

		// Now try to respond via web - should fail with 409
		body := fmt.Sprintf(`{"request_id": "%s", "response": "web response"}`, reqID)
		req, _ := http.NewRequest("POST", "http://"+addr+"/input", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("failed to make request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusConflict {
			t.Errorf("expected status %d (Conflict), got %d", http.StatusConflict, resp.StatusCode)
		}
	})

	t.Run("IsInputResponded returns correct state", func(t *testing.T) {
		// New request_id should not be responded
		if server.IsInputResponded("new-request-456") {
			t.Error("expected new request to not be responded")
		}

		// After marking, it should be responded
		server.MarkInputResponded("new-request-456")
		if !server.IsInputResponded("new-request-456") {
			t.Error("expected marked request to be responded")
		}
	})
}

func TestInputBroadcastsUpdate(t *testing.T) {
	server := createTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		server.Start(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	defer server.Stop()

	addr := server.ListenAddr()
	token := getAuthToken(t, addr)

	// Submit input
	body := `{"request_id": "broadcast-test-123", "response": "broadcast response"}`
	req, _ := http.NewRequest("POST", "http://"+addr+"/input", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to make request: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
	}

	// Verify the input request was broadcast as responded
	_, _, inputRequests := server.Streams().GetCurrentState()

	var found *InputRequest
	for _, req := range inputRequests {
		if req.ID == "broadcast-test-123" {
			found = req
			break
		}
	}

	if found == nil {
		t.Fatal("expected input request to be broadcast")
	}

	if !found.Responded {
		t.Error("expected input request to be marked as responded")
	}

	if found.Response == nil || *found.Response != "broadcast response" {
		t.Error("expected response to be set correctly")
	}
}

// Tests for static asset serving

func TestHandleStatic(t *testing.T) {
	server := createTestServer(t)

	tests := []struct {
		name           string
		method         string
		path           string
		accept         string
		wantStatus     int
		wantType       string
		wantContains   string
		wantCacheCtrl  string
	}{
		{
			name:          "root returns index.html",
			method:        "GET",
			path:          "/",
			wantStatus:    http.StatusOK,
			wantType:      "text/html; charset=utf-8",
			wantContains:  "Wisp",
			wantCacheCtrl: "no-cache",
		},
		{
			name:          "explicit index.html",
			method:        "GET",
			path:          "/index.html",
			wantStatus:    http.StatusOK,
			wantType:      "text/html; charset=utf-8",
			wantContains:  "<!DOCTYPE html>",
			wantCacheCtrl: "no-cache",
		},
		{
			name:       "HEAD request works",
			method:     "HEAD",
			path:       "/",
			wantStatus: http.StatusOK,
			wantType:   "text/html; charset=utf-8",
		},
		{
			name:       "POST not allowed",
			method:     "POST",
			path:       "/",
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "PUT not allowed",
			method:     "PUT",
			path:       "/",
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "nonexistent file returns 404",
			method:     "GET",
			path:       "/nonexistent.txt",
			wantStatus: http.StatusNotFound,
		},
		{
			name:         "nonexistent path with HTML accept returns index.html (SPA support)",
			method:       "GET",
			path:         "/some/spa/route",
			accept:       "text/html",
			wantStatus:   http.StatusOK,
			wantType:     "text/html; charset=utf-8",
			wantContains: "<!DOCTYPE html>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.accept != "" {
				req.Header.Set("Accept", tt.accept)
			}
			w := httptest.NewRecorder()

			server.handleStatic(w, req)

			resp := w.Result()
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("expected status %d, got %d", tt.wantStatus, resp.StatusCode)
			}

			if tt.wantType != "" {
				contentType := resp.Header.Get("Content-Type")
				if contentType != tt.wantType {
					t.Errorf("expected Content-Type %q, got %q", tt.wantType, contentType)
				}
			}

			if tt.wantContains != "" {
				body, _ := io.ReadAll(resp.Body)
				if !strings.Contains(string(body), tt.wantContains) {
					t.Errorf("expected body to contain %q, got %q", tt.wantContains, string(body))
				}
			}

			if tt.wantCacheCtrl != "" {
				cacheCtrl := resp.Header.Get("Cache-Control")
				if cacheCtrl != tt.wantCacheCtrl {
					t.Errorf("expected Cache-Control %q, got %q", tt.wantCacheCtrl, cacheCtrl)
				}
			}
		})
	}
}

func TestGetContentType(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"index.html", "text/html; charset=utf-8"},
		{"style.css", "text/css; charset=utf-8"},
		{"app.js", "application/javascript; charset=utf-8"},
		{"data.json", "application/json; charset=utf-8"},
		{"icon.svg", "image/svg+xml"},
		{"photo.png", "image/png"},
		{"photo.jpg", "image/jpeg"},
		{"photo.jpeg", "image/jpeg"},
		{"animation.gif", "image/gif"},
		{"favicon.ico", "image/x-icon"},
		{"font.woff", "font/woff"},
		{"font.woff2", "font/woff2"},
		{"font.ttf", "font/ttf"},
		{"image.webp", "image/webp"},
		{"unknown.xyz", "application/octet-stream"},
		{"nested/path/file.html", "text/html; charset=utf-8"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := getContentType(tt.path)
			if got != tt.want {
				t.Errorf("getContentType(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsImmutableAsset(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		// Hashed assets (immutable)
		{"index-BcD123aF.js", true},
		{"style-abc456.css", true},
		{"vendor.def789.js", true},

		// Non-hashed assets (mutable)
		{"index.html", false},
		{"style.css", false},
		{"app.js", false},
		{"favicon.ico", false},

		// Edge cases
		{"a.b", false},           // too short
		{"file.short.js", false}, // 5 chars is too short for hash
		{"file.abc123.js", true}, // 6 chars is minimum for hash
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isImmutableAsset(tt.path)
			if got != tt.want {
				t.Errorf("isImmutableAsset(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
