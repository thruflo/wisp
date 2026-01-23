package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRateLimiter_BasicRateLimit(t *testing.T) {
	t.Parallel()

	config := RateLimitConfig{
		MaxAttempts: 3,
		Window:      time.Second,
		BlockAfter:  10,
		BlockTime:   time.Second,
	}
	rl := newRateLimiter(config)

	ip := "192.168.1.1"

	// First 3 attempts should be allowed
	for i := 0; i < 3; i++ {
		result := rl.check(ip)
		assert.True(t, result.Allowed, "attempt %d should be allowed", i+1)
	}

	// 4th attempt should be rate limited
	result := rl.check(ip)
	assert.False(t, result.Allowed)
	assert.False(t, result.IsBlocked)
	assert.Equal(t, "rate limit exceeded", result.Reason)
	assert.Greater(t, result.RetryAfter, time.Duration(0))
}

func TestRateLimiter_WindowExpiry(t *testing.T) {
	t.Parallel()

	config := RateLimitConfig{
		MaxAttempts: 2,
		Window:      50 * time.Millisecond,
		BlockAfter:  10,
		BlockTime:   time.Second,
	}
	rl := newRateLimiter(config)

	ip := "192.168.1.2"

	// Use up the limit
	for i := 0; i < 2; i++ {
		result := rl.check(ip)
		assert.True(t, result.Allowed)
	}

	// Should be rate limited now
	result := rl.check(ip)
	assert.False(t, result.Allowed)

	// Wait for window to expire
	time.Sleep(60 * time.Millisecond)

	// Should be allowed again
	result = rl.check(ip)
	assert.True(t, result.Allowed)
}

func TestRateLimiter_FailureBlocking(t *testing.T) {
	t.Parallel()

	config := RateLimitConfig{
		MaxAttempts: 20, // High limit so we don't hit rate limit
		Window:      time.Minute,
		BlockAfter:  3,
		BlockTime:   50 * time.Millisecond,
	}
	rl := newRateLimiter(config)

	ip := "192.168.1.3"

	// Record failures
	for i := 0; i < 3; i++ {
		rl.recordFailure(ip)
	}

	// Should now be blocked
	result := rl.check(ip)
	assert.False(t, result.Allowed)
	assert.True(t, result.IsBlocked)
	assert.Equal(t, "too many failed attempts", result.Reason)
}

func TestRateLimiter_SuccessResetsFailures(t *testing.T) {
	t.Parallel()

	config := RateLimitConfig{
		MaxAttempts: 20,
		Window:      time.Minute,
		BlockAfter:  5,
		BlockTime:   time.Second,
	}
	rl := newRateLimiter(config)

	ip := "192.168.1.4"

	// Record some failures (but not enough to block)
	for i := 0; i < 4; i++ {
		rl.recordFailure(ip)
	}

	// Success should reset failures
	rl.recordSuccess(ip)

	// Recording more failures shouldn't immediately block
	for i := 0; i < 4; i++ {
		rl.recordFailure(ip)
	}

	result := rl.check(ip)
	assert.True(t, result.Allowed, "should not be blocked since failures were reset")
}

func TestRateLimiter_DifferentIPs(t *testing.T) {
	t.Parallel()

	config := RateLimitConfig{
		MaxAttempts: 2,
		Window:      time.Minute,
		BlockAfter:  10,
		BlockTime:   time.Second,
	}
	rl := newRateLimiter(config)

	ip1 := "192.168.1.10"
	ip2 := "192.168.1.11"

	// Use up limit for IP1
	for i := 0; i < 2; i++ {
		result := rl.check(ip1)
		assert.True(t, result.Allowed)
	}

	// IP1 should be rate limited
	result := rl.check(ip1)
	assert.False(t, result.Allowed)

	// IP2 should still be allowed
	result = rl.check(ip2)
	assert.True(t, result.Allowed)
}

func TestRateLimiter_Cleanup(t *testing.T) {
	t.Parallel()

	config := RateLimitConfig{
		MaxAttempts: 2,
		Window:      50 * time.Millisecond,
		BlockAfter:  2,
		BlockTime:   50 * time.Millisecond,
	}
	rl := newRateLimiter(config)

	// Add some entries
	ip := "192.168.1.20"
	rl.check(ip)
	rl.recordFailure(ip)
	rl.recordFailure(ip)

	// Wait for entries to expire
	time.Sleep(100 * time.Millisecond)

	// Cleanup
	rl.cleanup()

	// Check that entries were removed (check by verifying we can make requests again)
	result := rl.check(ip)
	assert.True(t, result.Allowed, "should be allowed after cleanup")
}

func TestExtractIP(t *testing.T) {
	tests := []struct {
		name     string
		headers  map[string]string
		remoteIP string
		expected string
	}{
		{
			name:     "X-Forwarded-For single IP",
			headers:  map[string]string{"X-Forwarded-For": "203.0.113.50"},
			remoteIP: "10.0.0.1:12345",
			expected: "203.0.113.50",
		},
		{
			name:     "X-Forwarded-For multiple IPs",
			headers:  map[string]string{"X-Forwarded-For": "203.0.113.50, 70.41.3.18, 150.172.238.178"},
			remoteIP: "10.0.0.1:12345",
			expected: "203.0.113.50",
		},
		{
			name:     "X-Real-IP",
			headers:  map[string]string{"X-Real-IP": "203.0.113.51"},
			remoteIP: "10.0.0.1:12345",
			expected: "203.0.113.51",
		},
		{
			name:     "X-Forwarded-For takes precedence over X-Real-IP",
			headers:  map[string]string{"X-Forwarded-For": "203.0.113.50", "X-Real-IP": "203.0.113.51"},
			remoteIP: "10.0.0.1:12345",
			expected: "203.0.113.50",
		},
		{
			name:     "Falls back to remote address",
			headers:  map[string]string{},
			remoteIP: "10.0.0.1:12345",
			expected: "10.0.0.1",
		},
		{
			name:     "Remote address without port",
			headers:  map[string]string{},
			remoteIP: "10.0.0.1",
			expected: "10.0.0.1",
		},
		{
			name:     "X-Forwarded-For with whitespace",
			headers:  map[string]string{"X-Forwarded-For": "  203.0.113.50  "},
			remoteIP: "10.0.0.1:12345",
			expected: "203.0.113.50",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/auth", nil)
			req.RemoteAddr = tt.remoteIP
			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}

			result := extractIP(req)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDefaultRateLimitConfig(t *testing.T) {
	t.Parallel()

	config := DefaultRateLimitConfig()
	assert.Equal(t, 5, config.MaxAttempts)
	assert.Equal(t, time.Minute, config.Window)
	assert.Equal(t, 10, config.BlockAfter)
	assert.Equal(t, 5*time.Minute, config.BlockTime)
}

func TestRateLimiter_ExponentialBackoff(t *testing.T) {
	t.Parallel()

	config := RateLimitConfig{
		MaxAttempts: 100,
		Window:      time.Minute,
		BlockAfter:  2,
		BlockTime:   10 * time.Millisecond,
	}
	rl := newRateLimiter(config)

	ip := "192.168.1.30"

	// First block: 2 failures
	rl.recordFailure(ip)
	rl.recordFailure(ip)

	// Should be blocked
	result := rl.check(ip)
	require.False(t, result.Allowed)
	require.True(t, result.IsBlocked)

	// Wait for block to expire
	time.Sleep(15 * time.Millisecond)

	// Should be allowed again
	result = rl.check(ip)
	assert.True(t, result.Allowed)

	// Record more failures (4 total now, should get longer block)
	rl.recordFailure(ip)
	rl.recordFailure(ip)

	// Should be blocked again
	result = rl.check(ip)
	require.False(t, result.Allowed)
	require.True(t, result.IsBlocked)

	// The block duration should be longer due to exponential backoff
	// First block was 10ms, second should be 20ms
	assert.Greater(t, result.RetryAfter, 10*time.Millisecond)
}

func TestItoa(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{0, "0"},
		{1, "1"},
		{10, "10"},
		{123, "123"},
		{-1, "-1"},
		{-123, "-123"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := itoa(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTrimSpace(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"  hello", "hello"},
		{"hello  ", "hello"},
		{"  hello  ", "hello"},
		{"\thello\t", "hello"},
		{"", ""},
		{"   ", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := trimSpace(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
