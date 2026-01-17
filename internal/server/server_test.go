package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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
		req := httptest.NewRequest(http.MethodPost, "/auth", nil)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()

		mux.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
		}
	})

	t.Run("wrong password", func(t *testing.T) {
		form := url.Values{}
		form.Add("password", "wrong-password")
		req := httptest.NewRequest(http.MethodPost, "/auth", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()

		mux.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
		}
	})

	t.Run("correct password", func(t *testing.T) {
		form := url.Values{}
		form.Add("password", testPassword)
		req := httptest.NewRequest(http.MethodPost, "/auth", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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
	form := url.Values{}
	form.Add("password", testPassword)
	resp, err := http.PostForm("http://"+addr+"/auth", form)
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
	form = url.Values{}
	form.Add("password", "wrong-password")
	resp2, err := http.PostForm("http://"+addr+"/auth", form)
	if err != nil {
		t.Fatalf("failed to make request: %v", err)
	}
	resp2.Body.Close()

	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected status %d for wrong password, got %d", http.StatusUnauthorized, resp2.StatusCode)
	}
}
