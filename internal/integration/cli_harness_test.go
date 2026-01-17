//go:build e2e

// cli_harness_test.go provides a test harness for E2E testing of the wisp CLI.
//
// The CLIHarness builds the wisp binary and provides methods for executing
// CLI commands in an isolated test workspace with proper environment setup.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/thruflo/wisp/internal/config"
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

	// Setup the test workspace with .wisp configuration
	setupTestWorkspace(t, workDir, projectRoot)

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

// setupTestWorkspace creates a .wisp directory structure for CLI E2E testing.
// It copies configuration and templates from the project root, with test-appropriate
// settings. Credentials are read from environment variables.
func setupTestWorkspace(t *testing.T, workDir, projectRoot string) {
	t.Helper()

	wispDir := filepath.Join(workDir, ".wisp")

	// Create directory structure
	dirs := []string{
		wispDir,
		filepath.Join(wispDir, "sessions"),
		filepath.Join(wispDir, "templates", "default"),
	}
	for _, dir := range dirs {
		require.NoError(t, os.MkdirAll(dir, 0755))
	}

	// Write config.yaml with test-appropriate settings (lower limits)
	testConfig := `# Wisp test configuration
# Lower limits for faster E2E tests

limits:
  # Maximum number of Claude iterations before stopping
  max_iterations: 5

  # Maximum budget in USD per session
  max_budget_usd: 2.00

  # Maximum wall-clock time in hours
  max_duration_hours: 0.5

  # Number of iterations without task progress before marking BLOCKED
  no_progress_threshold: 2
`
	require.NoError(t, os.WriteFile(
		filepath.Join(wispDir, "config.yaml"),
		[]byte(testConfig),
		0644,
	))

	// Write settings.json with test-appropriate permissions
	settings := config.Settings{
		Permissions: config.Permissions{
			Deny: []string{
				"Read(~/.ssh/**)",
				"Edit(~/.ssh/**)",
				"Read(~/.aws/**)",
				"Edit(~/.aws/**)",
			},
		},
		MCPServers: map[string]config.MCPServer{},
	}
	settingsData, err := json.MarshalIndent(settings, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(
		filepath.Join(wispDir, "settings.json"),
		settingsData,
		0644,
	))

	// Copy templates from project root
	srcTemplateDir := filepath.Join(projectRoot, ".wisp", "templates", "default")
	dstTemplateDir := filepath.Join(wispDir, "templates", "default")
	copyTemplates(t, srcTemplateDir, dstTemplateDir)

	// Write .sprite.env with credentials from environment
	writeTestSpriteEnv(t, wispDir)
}

// copyTemplates copies all template files from src to dst directory.
// Falls back to minimal templates if the source doesn't exist.
func copyTemplates(t *testing.T, src, dst string) {
	t.Helper()

	// Check if source templates exist
	if _, err := os.Stat(src); os.IsNotExist(err) {
		// Write minimal fallback templates
		t.Log("source templates not found, using minimal templates")
		writeMinimalTemplates(t, dst)
		return
	}

	// Read source directory
	entries, err := os.ReadDir(src)
	require.NoError(t, err, "failed to read template directory")

	for _, entry := range entries {
		if entry.IsDir() {
			continue // Skip subdirectories
		}

		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if err := copyFile(srcPath, dstPath); err != nil {
			// Log and continue with other templates
			t.Logf("failed to copy template %s: %v", entry.Name(), err)
			continue
		}
	}
}

// copyFile copies a single file from src to dst.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create dest: %w", err)
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("copy content: %w", err)
	}

	return nil
}

// writeMinimalTemplates writes minimal template files for testing.
// Used as a fallback when project templates aren't available.
func writeMinimalTemplates(t *testing.T, templateDir string) {
	t.Helper()

	templates := map[string]string{
		"context.md":      "# Context\nYou are an autonomous coding agent.\n",
		"create-tasks.md": "# Create Tasks\nRead the RFC and generate tasks.\n",
		"update-tasks.md": "# Update Tasks\nUpdate the task list based on progress.\n",
		"review-tasks.md": "# Review Tasks\nReview completed tasks.\n",
		"iterate.md":      "# Iterate\nComplete the next incomplete task.\n",
	}

	for name, content := range templates {
		path := filepath.Join(templateDir, name)
		require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	}
}

// writeTestSpriteEnv writes .sprite.env with credentials from environment variables.
// If credentials aren't available, writes placeholder values.
func writeTestSpriteEnv(t *testing.T, wispDir string) {
	t.Helper()

	spriteToken := os.Getenv("SPRITE_TOKEN")
	githubToken := os.Getenv("GITHUB_TOKEN")

	// Also try loading from project's .sprite.env if env vars aren't set
	if spriteToken == "" || githubToken == "" {
		projectRoot := findProjectRootForHarness(t)
		if projectRoot != "" {
			envVars, err := config.LoadEnvFile(projectRoot)
			if err == nil {
				if spriteToken == "" {
					spriteToken = envVars["SPRITE_TOKEN"]
				}
				if githubToken == "" {
					githubToken = envVars["GITHUB_TOKEN"]
				}
			}
		}
	}

	// Use placeholder if still not available (tests requiring real credentials will skip)
	if spriteToken == "" {
		spriteToken = "test-sprite-token"
		t.Log("SPRITE_TOKEN not found, using placeholder")
	}
	if githubToken == "" {
		githubToken = "test-github-token"
		t.Log("GITHUB_TOKEN not found, using placeholder")
	}

	envContent := fmt.Sprintf("GITHUB_TOKEN=%q\nSPRITE_TOKEN=%q\n", githubToken, spriteToken)
	require.NoError(t, os.WriteFile(
		filepath.Join(wispDir, ".sprite.env"),
		[]byte(envContent),
		0644,
	))
}
