package logging

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoggerLevels(t *testing.T) {
	tests := []struct {
		name      string
		minLevel  Level
		logLevel  Level
		shouldLog bool
	}{
		{"debug allowed at debug", LevelDebug, LevelDebug, true},
		{"info allowed at debug", LevelDebug, LevelInfo, true},
		{"warn allowed at debug", LevelDebug, LevelWarn, true},
		{"error allowed at debug", LevelDebug, LevelError, true},
		{"debug blocked at info", LevelInfo, LevelDebug, false},
		{"info allowed at info", LevelInfo, LevelInfo, true},
		{"warn allowed at info", LevelInfo, LevelWarn, true},
		{"error allowed at info", LevelInfo, LevelError, true},
		{"debug blocked at warn", LevelWarn, LevelDebug, false},
		{"info blocked at warn", LevelWarn, LevelInfo, false},
		{"warn allowed at warn", LevelWarn, LevelWarn, true},
		{"error allowed at warn", LevelWarn, LevelError, true},
		{"debug blocked at error", LevelError, LevelDebug, false},
		{"info blocked at error", LevelError, LevelInfo, false},
		{"warn blocked at error", LevelError, LevelWarn, false},
		{"error allowed at error", LevelError, LevelError, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := New()
			logger.SetLevel(tt.minLevel)
			logger.SetOutput(log.New(&buf, "", 0))

			switch tt.logLevel {
			case LevelDebug:
				logger.Debug("test message")
			case LevelInfo:
				logger.Info("test message")
			case LevelWarn:
				logger.Warn("test message")
			case LevelError:
				logger.Error("test message")
			}

			if tt.shouldLog {
				assert.NotEmpty(t, buf.String(), "expected log output")
				assert.Contains(t, buf.String(), "test message")
			} else {
				assert.Empty(t, buf.String(), "expected no log output")
			}
		})
	}
}

func TestLoggerWith(t *testing.T) {
	var buf bytes.Buffer
	logger := New()
	logger.SetLevel(LevelDebug)
	logger.SetOutput(log.New(&buf, "", 0))

	childLogger := logger.With("session", "abc123")
	childLogger.Warn("something happened")

	output := buf.String()
	assert.Contains(t, output, "WARN: something happened")
	assert.Contains(t, output, "session=abc123")
}

func TestLoggerWithFields(t *testing.T) {
	var buf bytes.Buffer
	logger := New()
	logger.SetLevel(LevelDebug)
	logger.SetOutput(log.New(&buf, "", 0))

	childLogger := logger.WithFields(map[string]interface{}{
		"session": "abc123",
		"branch":  "feature-x",
	})
	childLogger.Error("error occurred")

	output := buf.String()
	assert.Contains(t, output, "ERROR: error occurred")
	assert.Contains(t, output, "session=abc123")
	assert.Contains(t, output, "branch=feature-x")
}

func TestLoggerInlineKeyVals(t *testing.T) {
	var buf bytes.Buffer
	logger := New()
	logger.SetLevel(LevelDebug)
	logger.SetOutput(log.New(&buf, "", 0))

	logger.Warn("failed to sync", "error", errors.New("timeout"), "retry", 3)

	output := buf.String()
	assert.Contains(t, output, "WARN: failed to sync")
	assert.Contains(t, output, "error=\"timeout\"")
	assert.Contains(t, output, "retry=3")
}

func TestLoggerChainingPreservesFields(t *testing.T) {
	var buf bytes.Buffer
	logger := New()
	logger.SetLevel(LevelDebug)
	logger.SetOutput(log.New(&buf, "", 0))

	sessionLogger := logger.With("session", "abc123")
	opLogger := sessionLogger.With("operation", "sync")
	opLogger.Info("starting")

	output := buf.String()
	assert.Contains(t, output, "session=abc123")
	assert.Contains(t, output, "operation=sync")
}

func TestLoggerOriginalUnmodified(t *testing.T) {
	var buf bytes.Buffer
	logger := New()
	logger.SetLevel(LevelDebug)
	logger.SetOutput(log.New(&buf, "", 0))

	_ = logger.With("session", "abc123")
	logger.Info("original logger")

	output := buf.String()
	assert.NotContains(t, output, "session=abc123")
}

func TestFormatValue(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected string
	}{
		{"simple string", "hello", "hello"},
		{"string with spaces", "hello world", `"hello world"`},
		{"string with newline", "hello\nworld", `"hello\nworld"`},
		{"integer", 42, "42"},
		{"error", errors.New("oops"), `"oops"`},
		{"bool", true, "true"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatValue(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDefaultLogger(t *testing.T) {
	// Test that package-level functions work
	var buf bytes.Buffer
	SetOutput(log.New(&buf, "", 0))
	SetLevel(LevelWarn)

	// Debug should be filtered out
	Debug("debug message")
	assert.Empty(t, buf.String())

	// Warn should be logged
	Warn("warn message")
	assert.Contains(t, buf.String(), "WARN: warn message")

	buf.Reset()

	// With should return a child logger
	childLogger := With("component", "test")
	childLogger.Error("error message")
	assert.Contains(t, buf.String(), "component=test")
}

func TestLevelNames(t *testing.T) {
	tests := []struct {
		level Level
		name  string
	}{
		{LevelDebug, "DEBUG"},
		{LevelInfo, "INFO"},
		{LevelWarn, "WARN"},
		{LevelError, "ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := New()
			logger.SetLevel(LevelDebug)
			logger.SetOutput(log.New(&buf, "", 0))

			switch tt.level {
			case LevelDebug:
				logger.Debug("test")
			case LevelInfo:
				logger.Info("test")
			case LevelWarn:
				logger.Warn("test")
			case LevelError:
				logger.Error("test")
			}

			assert.True(t, strings.HasPrefix(buf.String(), tt.name+":"))
		})
	}
}
