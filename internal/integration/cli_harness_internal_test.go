//go:build e2e

// cli_harness_internal_test.go contains tests for the CLIHarness itself.
// These tests verify that the harness is working correctly before using it
// for actual E2E tests.
package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCLIHarness_BuildsBinary verifies that NewCLIHarness successfully builds
// the wisp binary.
func TestCLIHarness_BuildsBinary(t *testing.T) {
	h := NewCLIHarness(t)

	// Binary should exist and be executable
	info, err := os.Stat(h.BinaryPath)
	require.NoError(t, err, "binary should exist")
	assert.NotZero(t, info.Size(), "binary should not be empty")

	// Binary should be executable (mode includes execute bit)
	mode := info.Mode()
	assert.True(t, mode&0111 != 0, "binary should be executable")
}

// TestCLIHarness_WorkDirCreated verifies that the workspace directory is created.
func TestCLIHarness_WorkDirCreated(t *testing.T) {
	h := NewCLIHarness(t)

	info, err := os.Stat(h.WorkDir)
	require.NoError(t, err, "workspace should exist")
	assert.True(t, info.IsDir(), "workspace should be a directory")
}

// TestCLIHarness_RunVersion verifies that a simple command can be executed.
// Using --version or --help is a good basic test.
func TestCLIHarness_RunVersion(t *testing.T) {
	h := NewCLIHarness(t)

	// Run help command (should always succeed)
	result := h.Run("--help")

	// Help should succeed
	assert.Equal(t, 0, result.ExitCode, "help should succeed")
	assert.NoError(t, result.Err, "help should not error")
	assert.True(t, result.Success(), "Success() should return true")

	// Output should contain expected help text
	assert.Contains(t, result.Stdout, "wisp", "help should mention wisp")
}

// TestCLIHarness_SetEnv verifies environment variable handling.
func TestCLIHarness_SetEnv(t *testing.T) {
	h := NewCLIHarness(t)

	// Set a custom env var
	h.SetEnv("TEST_VAR", "test_value")
	assert.Equal(t, "test_value", h.EnvVars["TEST_VAR"])

	// Set multiple via map
	h.SetEnvMap(map[string]string{
		"VAR_ONE": "one",
		"VAR_TWO": "two",
	})
	assert.Equal(t, "one", h.EnvVars["VAR_ONE"])
	assert.Equal(t, "two", h.EnvVars["VAR_TWO"])

	// Clear should reset
	h.ClearEnv()
	assert.Empty(t, h.EnvVars)
}

// TestCLIHarness_RunWithTimeout verifies timeout handling.
func TestCLIHarness_RunWithTimeout(t *testing.T) {
	h := NewCLIHarness(t)

	// A quick command should complete within timeout
	result := h.RunWithTimeout(5*time.Second, "--help")
	assert.True(t, result.Success(), "quick command should succeed within timeout")
}

// TestCLIHarness_RunWithContext verifies context cancellation.
func TestCLIHarness_RunWithContext(t *testing.T) {
	h := NewCLIHarness(t)

	// Create an already-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := h.RunWithContext(ctx, "--help")

	// Command should fail due to cancelled context
	assert.False(t, result.Success(), "command with cancelled context should fail")
	assert.Error(t, result.Err, "should have an error")
}

// TestCLIHarness_FailingCommand verifies handling of commands that fail.
func TestCLIHarness_FailingCommand(t *testing.T) {
	h := NewCLIHarness(t)

	// Run with an invalid subcommand
	result := h.Run("nonexistent-command")

	// Should fail
	assert.False(t, result.Success(), "invalid command should fail")
	assert.NotEqual(t, 0, result.ExitCode, "exit code should be non-zero")
	assert.NotEmpty(t, result.Stderr, "stderr should have error message")
}

// TestCLIResult_Success verifies the Success method.
func TestCLIResult_Success(t *testing.T) {
	tests := []struct {
		name     string
		result   CLIResult
		expected bool
	}{
		{
			name:     "exit 0 no error",
			result:   CLIResult{ExitCode: 0, Err: nil},
			expected: true,
		},
		{
			name:     "exit 1 no error",
			result:   CLIResult{ExitCode: 1, Err: nil},
			expected: false,
		},
		{
			name:     "exit 0 with error",
			result:   CLIResult{ExitCode: 0, Err: context.DeadlineExceeded},
			expected: false,
		},
		{
			name:     "exit 1 with error",
			result:   CLIResult{ExitCode: 1, Err: context.Canceled},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.result.Success())
		})
	}
}

// TestCLIHarness_BuildEnv verifies environment building.
func TestCLIHarness_BuildEnv(t *testing.T) {
	h := NewCLIHarness(t)

	// Set some custom vars
	h.SetEnv("CUSTOM_VAR", "custom_value")

	env := h.buildEnv()

	// Should contain PATH (from current env)
	hasPath := false
	hasCustom := false
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			hasPath = true
		}
		if e == "CUSTOM_VAR=custom_value" {
			hasCustom = true
		}
	}

	assert.True(t, hasPath, "should include PATH from environment")
	assert.True(t, hasCustom, "should include custom env var")
}

// TestFindProjectRootForHarness verifies project root detection.
func TestFindProjectRootForHarness(t *testing.T) {
	root := findProjectRootForHarness(t)

	// Should find the project root
	require.NotEmpty(t, root, "should find project root")

	// Project root should contain .wisp or go.mod
	wispDir := filepath.Join(root, ".wisp")
	goMod := filepath.Join(root, "go.mod")

	_, wispErr := os.Stat(wispDir)
	_, goModErr := os.Stat(goMod)

	assert.True(t, wispErr == nil || goModErr == nil,
		"project root should contain .wisp or go.mod")
}
