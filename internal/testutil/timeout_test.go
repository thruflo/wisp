package testutil

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestContextWithTestDeadline_WithFallback(t *testing.T) {
	// This test verifies that the context always has a deadline set
	// When running under `go test`, there may be a test deadline set,
	// which is fine - the helper should respect either the test deadline or the fallback
	fallback := 100 * time.Millisecond
	ctx, cancel := ContextWithTestDeadline(t, fallback)
	defer cancel()

	// Context should have a deadline
	deadline, ok := ctx.Deadline()
	assert.True(t, ok, "context should have deadline")

	// Deadline should be in the future
	remaining := time.Until(deadline)
	assert.Greater(t, remaining.Seconds(), 0.0, "deadline should be in the future")
}

func TestContextWithTestDeadlineBuffer_WithFallback(t *testing.T) {
	// This test verifies that the context always has a deadline set
	fallback := 200 * time.Millisecond
	buffer := 50 * time.Millisecond

	ctx, cancel := ContextWithTestDeadlineBuffer(t, fallback, buffer)
	defer cancel()

	// Context should have a deadline
	deadline, ok := ctx.Deadline()
	assert.True(t, ok, "context should have deadline")

	// Deadline should be in the future
	remaining := time.Until(deadline)
	assert.Greater(t, remaining.Seconds(), 0.0, "deadline should be in the future")
}

func TestContextWithTimeout(t *testing.T) {
	timeout := 150 * time.Millisecond
	ctx, cancel := ContextWithTimeout(t, timeout)
	defer cancel()

	// Context should have a deadline
	deadline, ok := ctx.Deadline()
	assert.True(t, ok, "context should have deadline")

	// Deadline should be approximately timeout from now
	remaining := time.Until(deadline)
	assert.InDelta(t, timeout.Seconds(), remaining.Seconds(), 0.1, "deadline should be ~timeout from now")
}

func TestSpriteOperationContext(t *testing.T) {
	ctx, cancel := SpriteOperationContext(t)
	defer cancel()

	// Context should have a deadline
	deadline, ok := ctx.Deadline()
	assert.True(t, ok, "context should have deadline")

	// Deadline should be approximately DefaultSpriteTimeout from now (unless test has deadline)
	remaining := time.Until(deadline)
	assert.Greater(t, remaining.Seconds(), 0.0, "deadline should be in the future")
}

func TestClaudeExecutionContext(t *testing.T) {
	ctx, cancel := ClaudeExecutionContext(t)
	defer cancel()

	// Context should have a deadline
	deadline, ok := ctx.Deadline()
	assert.True(t, ok, "context should have deadline")

	// Deadline should be in the future
	remaining := time.Until(deadline)
	assert.Greater(t, remaining.Seconds(), 0.0, "deadline should be in the future")
}

func TestShortOperationContext(t *testing.T) {
	ctx, cancel := ShortOperationContext(t)
	defer cancel()

	// Context should have a deadline
	deadline, ok := ctx.Deadline()
	assert.True(t, ok, "context should have deadline")

	// Deadline should be in the future
	remaining := time.Until(deadline)
	assert.Greater(t, remaining.Seconds(), 0.0, "deadline should be in the future")
}

func TestContextCancellation(t *testing.T) {
	// Verify cancel function works properly
	ctx, cancel := ContextWithTimeout(t, 1*time.Minute)

	// Context should not be done yet
	select {
	case <-ctx.Done():
		t.Fatal("context should not be done before cancel")
	default:
		// expected
	}

	// Cancel the context
	cancel()

	// Context should be done now
	select {
	case <-ctx.Done():
		// expected
	default:
		t.Fatal("context should be done after cancel")
	}
}
