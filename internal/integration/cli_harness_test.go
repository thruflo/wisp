//go:build e2e

// cli_harness_test.go provides a test harness for E2E testing of the wisp CLI.
//
// The CLIHarness builds the wisp binary and provides methods for executing
// CLI commands in an isolated test workspace with proper environment setup.
package integration

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// CLIHarness manages a wisp CLI binary for E2E testing.
// It builds the binary once and provides methods to execute commands
// in an isolated workspace with controlled environment variables.
type CLIHarness struct {
	// BinaryPath is the path to the built wisp binary.
	BinaryPath string

	// WorkDir is the working directory where commands will be executed.
	// This directory contains the .wisp configuration structure.
	WorkDir string

	// EnvVars contains environment variables to set for command execution.
	// These are merged with the test's default environment.
	EnvVars map[string]string

	t *testing.T
}

// CLIResult contains the output from a CLI command execution.
type CLIResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Err      error
}

// Success returns true if the command completed with exit code 0.
func (r *CLIResult) Success() bool {
	return r.ExitCode == 0 && r.Err == nil
}

// NewCLIHarness builds the wisp binary and creates a test workspace.
// The binary is built to a temporary directory and the workspace includes
// the standard .wisp directory structure.
func NewCLIHarness(t *testing.T) *CLIHarness {
	t.Helper()

	// Find project root
	projectRoot := findProjectRootForHarness(t)
	require.NotEmpty(t, projectRoot, "could not find project root (directory containing .wisp)")

	// Build binary to temp directory
	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "wisp")

	cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/wisp")
	cmd.Dir = projectRoot
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "failed to build wisp binary: %s", output)

	// Create workspace directory with .wisp structure
	workDir := filepath.Join(tmpDir, "workspace")
	require.NoError(t, os.MkdirAll(workDir, 0755))

	h := &CLIHarness{
		BinaryPath: binaryPath,
		WorkDir:    workDir,
		EnvVars:    make(map[string]string),
		t:          t,
	}

	return h
}

// SetEnv sets an environment variable for subsequent command executions.
func (h *CLIHarness) SetEnv(key, value string) {
	h.EnvVars[key] = value
}

// SetEnvMap sets multiple environment variables for subsequent command executions.
func (h *CLIHarness) SetEnvMap(env map[string]string) {
	for k, v := range env {
		h.EnvVars[k] = v
	}
}

// ClearEnv removes all custom environment variables.
func (h *CLIHarness) ClearEnv() {
	h.EnvVars = make(map[string]string)
}

// Run executes a wisp command with default timeout (30 seconds).
// Returns stdout, stderr, and error.
func (h *CLIHarness) Run(args ...string) *CLIResult {
	return h.RunWithTimeout(30*time.Second, args...)
}

// RunWithTimeout executes a wisp command with the specified timeout.
// The command is executed in the workspace directory with the configured
// environment variables.
func (h *CLIHarness) RunWithTimeout(timeout time.Duration, args ...string) *CLIResult {
	h.t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return h.RunWithContext(ctx, args...)
}

// RunWithContext executes a wisp command with the given context.
// This provides full control over cancellation and deadlines.
func (h *CLIHarness) RunWithContext(ctx context.Context, args ...string) *CLIResult {
	h.t.Helper()

	cmd := exec.CommandContext(ctx, h.BinaryPath, args...)
	cmd.Dir = h.WorkDir

	// Set up environment
	cmd.Env = h.buildEnv()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	result := &CLIResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if err != nil {
		result.Err = err
		// Try to get exit code
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = -1
		}
	}

	return result
}

// buildEnv creates the environment variable slice for command execution.
// It includes the current process environment, removes potentially interfering
// variables, and adds the configured custom variables.
func (h *CLIHarness) buildEnv() []string {
	// Start with a filtered set of the current environment
	env := []string{}
	for _, e := range os.Environ() {
		// Skip variables that might interfere with tests
		if shouldIncludeEnvVar(e) {
			env = append(env, e)
		}
	}

	// Add custom environment variables
	for k, v := range h.EnvVars {
		env = append(env, k+"="+v)
	}

	return env
}

// shouldIncludeEnvVar returns true if the environment variable should be
// passed through to the test command.
func shouldIncludeEnvVar(envVar string) bool {
	// Allow most variables through; only filter problematic ones
	// This function can be extended to filter more if needed
	return true
}

// findProjectRootForHarness walks up from the current directory to find the project root.
// The project root is identified by the presence of a .wisp directory.
func findProjectRootForHarness(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	require.NoError(t, err, "failed to get working directory")

	for {
		// Check for .wisp directory (project marker)
		wispDir := filepath.Join(dir, ".wisp")
		if info, err := os.Stat(wispDir); err == nil && info.IsDir() {
			return dir
		}

		// Also check for go.mod as fallback
		goMod := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(goMod); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root
			return ""
		}
		dir = parent
	}
}

// RequireSuccess fails the test if the command result indicates failure.
func (h *CLIHarness) RequireSuccess(result *CLIResult, msgAndArgs ...interface{}) {
	h.t.Helper()
	if !result.Success() {
		msg := "command failed"
		if len(msgAndArgs) > 0 {
			if s, ok := msgAndArgs[0].(string); ok {
				msg = s
			}
		}
		h.t.Fatalf("%s: exit=%d err=%v\nstdout: %s\nstderr: %s",
			msg, result.ExitCode, result.Err, result.Stdout, result.Stderr)
	}
}

// RequireFailure fails the test if the command result indicates success.
func (h *CLIHarness) RequireFailure(result *CLIResult, msgAndArgs ...interface{}) {
	h.t.Helper()
	if result.Success() {
		msg := "expected command to fail"
		if len(msgAndArgs) > 0 {
			if s, ok := msgAndArgs[0].(string); ok {
				msg = s
			}
		}
		h.t.Fatalf("%s: command succeeded unexpectedly\nstdout: %s\nstderr: %s",
			msg, result.Stdout, result.Stderr)
	}
}
