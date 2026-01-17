package testutil

import (
	"context"
	"testing"
	"time"
)

// Default timeouts for sprite operations.
const (
	// DefaultSpriteTimeout is the default timeout for sprite operations like
	// create, delete, file read/write, and command execution.
	DefaultSpriteTimeout = 2 * time.Minute

	// DefaultClaudeTimeout is the default timeout for Claude execution.
	DefaultClaudeTimeout = 5 * time.Minute

	// DefaultTestBuffer is the buffer time subtracted from test deadline
	// to allow for cleanup operations before the test times out.
	DefaultTestBuffer = 10 * time.Second
)

// ContextWithTestDeadline creates a context that respects the test's deadline.
// It subtracts a buffer from the test deadline to allow time for cleanup.
// If the test has no deadline, it falls back to the provided fallback duration.
//
// Usage:
//
//	func TestSomething(t *testing.T) {
//	    ctx, cancel := testutil.ContextWithTestDeadline(t, 5*time.Minute)
//	    defer cancel()
//	    // ... test code using ctx
//	}
func ContextWithTestDeadline(t *testing.T, fallback time.Duration) (context.Context, context.CancelFunc) {
	t.Helper()
	return ContextWithTestDeadlineBuffer(t, fallback, DefaultTestBuffer)
}

// ContextWithTestDeadlineBuffer creates a context that respects the test's deadline
// with a custom buffer. The buffer is subtracted from the test deadline to allow
// time for cleanup operations before the test times out.
//
// If the test has no deadline, it uses the fallback duration.
// If the calculated deadline (test deadline minus buffer) is in the past,
// it uses the fallback instead.
func ContextWithTestDeadlineBuffer(t *testing.T, fallback, buffer time.Duration) (context.Context, context.CancelFunc) {
	t.Helper()

	if deadline, ok := t.Deadline(); ok {
		adjustedDeadline := deadline.Add(-buffer)
		// Only use adjusted deadline if it's still in the future
		if time.Until(adjustedDeadline) > 0 {
			t.Logf("Using test deadline: %v (buffer: %v)", time.Until(adjustedDeadline).Round(time.Second), buffer)
			return context.WithDeadline(context.Background(), adjustedDeadline)
		}
	}

	t.Logf("Using fallback timeout: %v", fallback)
	return context.WithTimeout(context.Background(), fallback)
}

// ContextWithTimeout creates a context with the specified timeout.
// This is a convenience wrapper that logs the timeout for debugging.
func ContextWithTimeout(t *testing.T, timeout time.Duration) (context.Context, context.CancelFunc) {
	t.Helper()
	t.Logf("Context timeout: %v", timeout)
	return context.WithTimeout(context.Background(), timeout)
}

// SpriteOperationContext creates a context with a standard timeout for sprite
// operations (create, delete, file I/O, command execution). It respects the
// test deadline if one is set, otherwise uses DefaultSpriteTimeout.
func SpriteOperationContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return ContextWithTestDeadline(t, DefaultSpriteTimeout)
}

// ClaudeExecutionContext creates a context with a timeout suitable for Claude
// execution. It respects the test deadline if one is set, otherwise uses
// DefaultClaudeTimeout.
func ClaudeExecutionContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return ContextWithTestDeadline(t, DefaultClaudeTimeout)
}

// ShortOperationContext creates a context with a short timeout (30 seconds)
// for quick operations like existence checks or simple file reads.
// It respects the test deadline if one is set.
func ShortOperationContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return ContextWithTestDeadline(t, 30*time.Second)
}
