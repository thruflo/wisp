package spriteloop

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ClaudeExecutor defines the interface for executing Claude commands.
// This abstraction allows for testing with mock executors.
type ClaudeExecutor interface {
	// Execute runs Claude with the given arguments in the specified directory.
	// The eventCallback is called for each line of output.
	// The commandCallback is called periodically to check for user commands
	// and should return an error to stop execution (e.g., errUserKill).
	Execute(ctx context.Context, dir string, args []string, eventCallback func(string), commandCallback func() error) error
}

// LocalExecutor executes Claude commands locally on the Sprite.
type LocalExecutor struct {
	// HomeDir is the HOME directory to set for Claude (for credentials).
	// Defaults to /var/local/wisp if empty.
	HomeDir string
}

// NewLocalExecutor creates a new LocalExecutor with default settings.
func NewLocalExecutor() *LocalExecutor {
	return &LocalExecutor{
		HomeDir: "/var/local/wisp",
	}
}

// Execute runs Claude locally with the given arguments.
func (e *LocalExecutor) Execute(ctx context.Context, dir string, args []string, eventCallback func(string), commandCallback func() error) error {
	if len(args) == 0 {
		return fmt.Errorf("no command specified")
	}

	// Build the bash command that sets up the environment and runs Claude
	claudeCmd := strings.Join(args, " ")
	homeDir := e.HomeDir
	if homeDir == "" {
		homeDir = "/var/local/wisp"
	}

	// Build bash command that:
	// 1. Sets HOME so Claude finds credentials
	// 2. Sources .bashrc for other env vars (GITHUB_TOKEN, etc)
	// 3. Runs the claude command
	bashCmd := fmt.Sprintf("export HOME=%s && source ~/.bashrc && %s", homeDir, claudeCmd)

	cmd := exec.CommandContext(ctx, "bash", "-c", bashCmd)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), fmt.Sprintf("HOME=%s", homeDir))

	// Create pipes for stdout and stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start Claude: %w", err)
	}

	// Stream stdout and stderr
	errCh := make(chan error, 2)

	// Stream stdout
	go func() {
		scanner := bufio.NewScanner(stdout)
		buf := make([]byte, 64*1024)
		scanner.Buffer(buf, 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if eventCallback != nil {
				eventCallback(line)
			}

			// Check for commands periodically
			if commandCallback != nil {
				if err := commandCallback(); err != nil {
					// User action - kill the process
					cmd.Process.Kill()
					errCh <- err
					return
				}
			}
		}
		errCh <- scanner.Err()
	}()

	// Stream stderr (to eventCallback as well)
	go func() {
		scanner := bufio.NewScanner(stderr)
		buf := make([]byte, 64*1024)
		scanner.Buffer(buf, 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if eventCallback != nil {
				eventCallback(line)
			}
		}
		errCh <- scanner.Err()
	}()

	// Wait for streaming to complete
	streamErr1 := <-errCh
	streamErr2 := <-errCh

	// Wait for command to complete
	waitErr := cmd.Wait()

	// Check for user action errors first
	if streamErr1 != nil && (streamErr1 == errUserKill || streamErr1 == errUserBackground) {
		return streamErr1
	}
	if streamErr2 != nil && (streamErr2 == errUserKill || streamErr2 == errUserBackground) {
		return streamErr2
	}

	// Check exit code - non-zero might be okay if state.json exists
	if waitErr != nil {
		// ExitError means the command ran but returned non-zero
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			// Check if state.json exists (command succeeded despite non-zero exit)
			statePath := filepath.Join("/var/local/wisp/session", "state.json")
			if _, err := os.Stat(statePath); err == nil {
				// State file exists, command was successful
				return nil
			}
			return fmt.Errorf("Claude exited with code %d", exitErr.ExitCode())
		}
		return fmt.Errorf("Claude failed: %w", waitErr)
	}

	return nil
}

// MockExecutor is a test double for ClaudeExecutor.
type MockExecutor struct {
	// ExecuteFunc is called when Execute is invoked.
	// If nil, Execute returns nil.
	ExecuteFunc func(ctx context.Context, dir string, args []string, eventCallback func(string), commandCallback func() error) error
}

// Execute calls the mock function if set.
func (m *MockExecutor) Execute(ctx context.Context, dir string, args []string, eventCallback func(string), commandCallback func() error) error {
	if m.ExecuteFunc != nil {
		return m.ExecuteFunc(ctx, dir, args, eventCallback, commandCallback)
	}
	return nil
}
