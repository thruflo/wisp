// Package logging provides structured logging for wisp with consistent formatting
// and context support. It wraps the standard log package to provide warn/error
// level logging with structured key-value pairs for debugging and monitoring.
package logging

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
)

// Level represents a log level.
type Level int

const (
	// LevelDebug is for verbose debugging information.
	LevelDebug Level = iota
	// LevelInfo is for general informational messages.
	LevelInfo
	// LevelWarn is for recoverable errors and warnings.
	LevelWarn
	// LevelError is for significant errors that may impact functionality.
	LevelError
)

var levelNames = map[Level]string{
	LevelDebug: "DEBUG",
	LevelInfo:  "INFO",
	LevelWarn:  "WARN",
	LevelError: "ERROR",
}

// Logger provides structured logging with context.
type Logger struct {
	mu       sync.RWMutex
	minLevel Level
	fields   map[string]interface{}
	output   *log.Logger
}

var (
	// defaultLogger is the package-level logger.
	defaultLogger = New()
)

// New creates a new Logger with default settings.
func New() *Logger {
	return &Logger{
		minLevel: LevelWarn, // Default to warn level
		fields:   make(map[string]interface{}),
		output:   log.New(os.Stderr, "", log.LstdFlags),
	}
}

// SetLevel sets the minimum log level.
func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.minLevel = level
}

// SetOutput sets the output logger.
func (l *Logger) SetOutput(output *log.Logger) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.output = output
}

// With returns a new Logger with additional context fields.
func (l *Logger) With(key string, value interface{}) *Logger {
	l.mu.RLock()
	defer l.mu.RUnlock()

	newFields := make(map[string]interface{}, len(l.fields)+1)
	for k, v := range l.fields {
		newFields[k] = v
	}
	newFields[key] = value

	return &Logger{
		minLevel: l.minLevel,
		fields:   newFields,
		output:   l.output,
	}
}

// WithFields returns a new Logger with multiple additional context fields.
func (l *Logger) WithFields(fields map[string]interface{}) *Logger {
	l.mu.RLock()
	defer l.mu.RUnlock()

	newFields := make(map[string]interface{}, len(l.fields)+len(fields))
	for k, v := range l.fields {
		newFields[k] = v
	}
	for k, v := range fields {
		newFields[k] = v
	}

	return &Logger{
		minLevel: l.minLevel,
		fields:   newFields,
		output:   l.output,
	}
}

// log writes a log entry at the given level.
func (l *Logger) log(level Level, msg string, keyVals ...interface{}) {
	l.mu.RLock()
	minLevel := l.minLevel
	output := l.output
	fields := l.fields
	l.mu.RUnlock()

	if level < minLevel {
		return
	}

	// Build the log message
	var sb strings.Builder
	sb.WriteString(levelNames[level])
	sb.WriteString(": ")
	sb.WriteString(msg)

	// Add context fields
	allFields := make(map[string]interface{}, len(fields)+len(keyVals)/2)
	for k, v := range fields {
		allFields[k] = v
	}

	// Add inline key-value pairs
	for i := 0; i+1 < len(keyVals); i += 2 {
		if key, ok := keyVals[i].(string); ok {
			allFields[key] = keyVals[i+1]
		}
	}

	// Format fields
	if len(allFields) > 0 {
		sb.WriteString(" |")
		for k, v := range allFields {
			sb.WriteString(" ")
			sb.WriteString(k)
			sb.WriteString("=")
			sb.WriteString(formatValue(v))
		}
	}

	output.Print(sb.String())
}

// formatValue formats a value for logging.
func formatValue(v interface{}) string {
	switch val := v.(type) {
	case string:
		if strings.ContainsAny(val, " \t\n") {
			return fmt.Sprintf("%q", val)
		}
		return val
	case error:
		return fmt.Sprintf("%q", val.Error())
	default:
		return fmt.Sprint(v)
	}
}

// Debug logs at debug level.
func (l *Logger) Debug(msg string, keyVals ...interface{}) {
	l.log(LevelDebug, msg, keyVals...)
}

// Info logs at info level.
func (l *Logger) Info(msg string, keyVals ...interface{}) {
	l.log(LevelInfo, msg, keyVals...)
}

// Warn logs at warn level (for recoverable errors).
func (l *Logger) Warn(msg string, keyVals ...interface{}) {
	l.log(LevelWarn, msg, keyVals...)
}

// Error logs at error level (for significant errors).
func (l *Logger) Error(msg string, keyVals ...interface{}) {
	l.log(LevelError, msg, keyVals...)
}

// Package-level functions that use the default logger.

// SetLevel sets the minimum log level for the default logger.
func SetLevel(level Level) {
	defaultLogger.SetLevel(level)
}

// SetOutput sets the output for the default logger.
func SetOutput(output *log.Logger) {
	defaultLogger.SetOutput(output)
}

// With returns a new Logger with additional context from the default logger.
func With(key string, value interface{}) *Logger {
	return defaultLogger.With(key, value)
}

// WithFields returns a new Logger with multiple additional context fields.
func WithFields(fields map[string]interface{}) *Logger {
	return defaultLogger.WithFields(fields)
}

// Debug logs at debug level using the default logger.
func Debug(msg string, keyVals ...interface{}) {
	defaultLogger.Debug(msg, keyVals...)
}

// Info logs at info level using the default logger.
func Info(msg string, keyVals ...interface{}) {
	defaultLogger.Info(msg, keyVals...)
}

// Warn logs at warn level using the default logger.
func Warn(msg string, keyVals ...interface{}) {
	defaultLogger.Warn(msg, keyVals...)
}

// Error logs at error level using the default logger.
func Error(msg string, keyVals ...interface{}) {
	defaultLogger.Error(msg, keyVals...)
}
