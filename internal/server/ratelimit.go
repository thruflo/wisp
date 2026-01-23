package server

import (
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

// RateLimitConfig holds rate limiting configuration.
type RateLimitConfig struct {
	MaxAttempts int           // Maximum attempts per window (default: 5)
	Window      time.Duration // Time window for rate limiting (default: 1 minute)
	BlockAfter  int           // Block after this many failed attempts (default: 10)
	BlockTime   time.Duration // Base block duration (default: 5 minutes, doubles each block)
}

// DefaultRateLimitConfig returns the default rate limiting configuration.
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		MaxAttempts: 5,
		Window:      time.Minute,
		BlockAfter:  10,
		BlockTime:   5 * time.Minute,
	}
}

// rateLimiter implements a sliding window rate limiter with exponential backoff.
type rateLimiter struct {
	mu     sync.Mutex
	config RateLimitConfig

	// attempts tracks timestamps of attempts per IP
	attempts map[string][]time.Time

	// failures tracks consecutive failed attempts per IP
	failures map[string]int

	// blocked tracks IPs that are blocked due to too many failures
	// value is the time when the block expires
	blocked map[string]time.Time
}

// newRateLimiter creates a new rate limiter with the given configuration.
func newRateLimiter(config RateLimitConfig) *rateLimiter {
	if config.MaxAttempts <= 0 {
		config.MaxAttempts = 5
	}
	if config.Window <= 0 {
		config.Window = time.Minute
	}
	if config.BlockAfter <= 0 {
		config.BlockAfter = 10
	}
	if config.BlockTime <= 0 {
		config.BlockTime = 5 * time.Minute
	}

	return &rateLimiter{
		config:   config,
		attempts: make(map[string][]time.Time),
		failures: make(map[string]int),
		blocked:  make(map[string]time.Time),
	}
}

// checkResult represents the result of a rate limit check.
type checkResult struct {
	Allowed     bool
	RetryAfter  time.Duration // How long until the client can retry
	IsBlocked   bool          // True if blocked due to too many failures
	Reason      string        // Human-readable reason for rejection
	AttemptsLog string        // Log message with attempt details
}

// check checks if the IP is allowed to make a request.
// Returns a checkResult with details about whether the request is allowed.
func (rl *rateLimiter) check(ip string) checkResult {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	// Check if IP is blocked due to too many failures
	if blockExpiry, isBlocked := rl.blocked[ip]; isBlocked {
		if now.Before(blockExpiry) {
			remaining := blockExpiry.Sub(now)
			return checkResult{
				Allowed:     false,
				RetryAfter:  remaining,
				IsBlocked:   true,
				Reason:      "too many failed attempts",
				AttemptsLog: formatLog(ip, "blocked", rl.failures[ip], remaining),
			}
		}
		// Block has expired, remove it
		delete(rl.blocked, ip)
	}

	// Clean up old attempts outside the window
	windowStart := now.Add(-rl.config.Window)
	if timestamps, exists := rl.attempts[ip]; exists {
		validTimestamps := make([]time.Time, 0, len(timestamps))
		for _, ts := range timestamps {
			if ts.After(windowStart) {
				validTimestamps = append(validTimestamps, ts)
			}
		}
		rl.attempts[ip] = validTimestamps
	}

	// Check rate limit
	currentAttempts := len(rl.attempts[ip])
	if currentAttempts >= rl.config.MaxAttempts {
		// Find when the oldest attempt in the window will expire
		oldestAttempt := rl.attempts[ip][0]
		retryAfter := oldestAttempt.Add(rl.config.Window).Sub(now)
		if retryAfter < 0 {
			retryAfter = time.Second // Minimum retry time
		}
		return checkResult{
			Allowed:     false,
			RetryAfter:  retryAfter,
			IsBlocked:   false,
			Reason:      "rate limit exceeded",
			AttemptsLog: formatLog(ip, "rate_limited", currentAttempts, retryAfter),
		}
	}

	// Record this attempt
	rl.attempts[ip] = append(rl.attempts[ip], now)

	return checkResult{
		Allowed:     true,
		AttemptsLog: formatLog(ip, "allowed", currentAttempts+1, 0),
	}
}

// recordSuccess records a successful authentication, resetting the failure counter.
func (rl *rateLimiter) recordSuccess(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Reset failure count on successful auth
	delete(rl.failures, ip)
	delete(rl.blocked, ip)
}

// recordFailure records a failed authentication attempt.
// If the failure count exceeds the threshold, the IP will be blocked with exponential backoff.
func (rl *rateLimiter) recordFailure(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.failures[ip]++
	failCount := rl.failures[ip]

	// Check if we should block this IP
	if failCount >= rl.config.BlockAfter {
		// Calculate exponential backoff: blockTime * 2^(blocks-1)
		// Where blocks is the number of times we've had to block this IP
		blocks := (failCount - rl.config.BlockAfter) / rl.config.BlockAfter
		multiplier := 1 << blocks // 2^blocks
		blockDuration := rl.config.BlockTime * time.Duration(multiplier)

		// Cap at 24 hours
		maxBlock := 24 * time.Hour
		if blockDuration > maxBlock {
			blockDuration = maxBlock
		}

		rl.blocked[ip] = time.Now().Add(blockDuration)
		log.Printf("auth: IP %s blocked for %v after %d failed attempts", ip, blockDuration, failCount)
	}
}

// formatLog creates a log message for rate limiting events.
func formatLog(ip, action string, attempts int, retryAfter time.Duration) string {
	if retryAfter > 0 {
		return "auth: " + action + " ip=" + ip + " attempts=" + itoa(attempts) + " retry_after=" + retryAfter.String()
	}
	return "auth: " + action + " ip=" + ip + " attempts=" + itoa(attempts)
}

// itoa is a simple int to string conversion to avoid importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "-" + itoa(-n)
	}
	digits := make([]byte, 0, 10)
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}
	// Reverse
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}

// cleanup removes expired entries from the rate limiter.
// Should be called periodically.
func (rl *rateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	windowStart := now.Add(-rl.config.Window)

	// Clean up old attempts
	for ip, timestamps := range rl.attempts {
		validTimestamps := make([]time.Time, 0, len(timestamps))
		for _, ts := range timestamps {
			if ts.After(windowStart) {
				validTimestamps = append(validTimestamps, ts)
			}
		}
		if len(validTimestamps) == 0 {
			delete(rl.attempts, ip)
		} else {
			rl.attempts[ip] = validTimestamps
		}
	}

	// Clean up expired blocks
	for ip, expiry := range rl.blocked {
		if now.After(expiry) {
			delete(rl.blocked, ip)
		}
	}

	// Clean up old failure counts for IPs that haven't been seen recently
	// Keep failure counts for blocked IPs
	for ip := range rl.failures {
		if _, isBlocked := rl.blocked[ip]; !isBlocked {
			if _, hasAttempts := rl.attempts[ip]; !hasAttempts {
				delete(rl.failures, ip)
			}
		}
	}
}

// extractIP extracts the client IP from the request.
// It checks X-Forwarded-For and X-Real-IP headers first (for reverse proxy scenarios),
// then falls back to the remote address.
func extractIP(r *http.Request) string {
	// Check X-Forwarded-For header (may contain multiple IPs, take the first)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For can be "client, proxy1, proxy2"
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return trimSpace(xff[:i])
			}
		}
		return trimSpace(xff)
	}

	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return trimSpace(xri)
	}

	// Fall back to remote address
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr might not have a port
		return r.RemoteAddr
	}
	return ip
}

// trimSpace trims leading and trailing whitespace from a string.
func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
