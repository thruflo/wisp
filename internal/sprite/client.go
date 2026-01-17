package sprite

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	sprites "github.com/superfly/sprites-go"
)

// Client defines the interface for Sprite operations.
type Client interface {
	// Create creates a new Sprite with the given name.
	// If checkpoint is non-empty, restores from that checkpoint after creation.
	Create(ctx context.Context, name string, checkpoint string) error

	// Execute runs a command on the Sprite and returns pipes for streaming.
	// The caller is responsible for calling Wait() on the returned Cmd after processing output.
	Execute(ctx context.Context, name string, dir string, env []string, args ...string) (*Cmd, error)

	// ExecuteOutput runs a command on the Sprite and returns collected output (non-streaming).
	// This uses the SDK's Run() method which avoids pipe handling issues.
	ExecuteOutput(ctx context.Context, name string, dir string, env []string, args ...string) (stdout, stderr []byte, exitCode int, err error)

	// ExecuteOutputWithRetry runs a command with retries for transient failures (exit code 255).
	// This is useful for commands that may fail due to sprite initialization race conditions.
	ExecuteOutputWithRetry(ctx context.Context, name string, dir string, env []string, args ...string) (stdout, stderr []byte, exitCode int, err error)

	// WriteFile writes content to a file path on the Sprite.
	WriteFile(ctx context.Context, name string, path string, content []byte) error

	// ReadFile reads content from a file path on the Sprite.
	ReadFile(ctx context.Context, name string, path string) ([]byte, error)

	// Delete deletes the Sprite.
	Delete(ctx context.Context, name string) error

	// Exists checks if a Sprite exists.
	Exists(ctx context.Context, name string) (bool, error)
}

// Cmd wraps a command execution with streaming capabilities.
type Cmd struct {
	cmd     *sprites.Cmd
	Stdout  io.ReadCloser
	Stderr  io.ReadCloser
	waitErr error
}

// Wait waits for the command to complete.
func (c *Cmd) Wait() error {
	if c.cmd == nil {
		return nil // For mock
	}
	c.waitErr = c.cmd.Wait()
	return c.waitErr
}

// ExitCode returns the exit code of the command after Wait() returns.
// Returns -1 if the command hasn't completed or the exit code is unknown.
func (c *Cmd) ExitCode() int {
	if c.waitErr == nil {
		return 0
	}
	if exitErr, ok := c.waitErr.(*sprites.ExitError); ok {
		return exitErr.ExitCode()
	}
	return -1
}

// NewMockCmd creates a Cmd for testing with the given stdout/stderr readers.
// The returned Cmd has a nil underlying cmd, so Wait() returns immediately.
func NewMockCmd(stdout, stderr io.ReadCloser) *Cmd {
	return &Cmd{
		cmd:    nil,
		Stdout: stdout,
		Stderr: stderr,
	}
}

// SDKClient implements Client using the sprites-go SDK.
type SDKClient struct {
	client *sprites.Client
}

// NewSDKClient creates a new SDKClient with the given API token.
func NewSDKClient(token string) *SDKClient {
	return &SDKClient{
		client: sprites.New(token),
	}
}

// Create creates a new Sprite with the given name.
func (c *SDKClient) Create(ctx context.Context, name string, checkpoint string) error {
	_, err := c.client.CreateSprite(ctx, name, nil)
	if err != nil {
		// TEMPORARY WORKAROUND: The Sprites API has a bug where it returns HTTP 500
		// but actually creates the sprite successfully. When we get a 500 error,
		// poll to check if the sprite was actually created and is responsive.
		// See: https://github.com/superfly/sprites-go/issues/XXX (TODO: file bug)
		//
		// Remove this workaround once the Sprites API is fixed.
		if apiErr := sprites.IsAPIError(err); apiErr != nil && apiErr.StatusCode == 500 {
			if c.waitForSpriteReady(ctx, name) {
				// Sprite was created despite the 500 error
				err = nil
			}
		}
		if err != nil {
			return fmt.Errorf("failed to create sprite %s: %w", name, err)
		}
	}

	if checkpoint != "" {
		sprite := c.client.Sprite(name)
		stream, err := sprite.RestoreCheckpoint(ctx, checkpoint)
		if err != nil {
			return fmt.Errorf("failed to restore checkpoint %s: %w", checkpoint, err)
		}
		defer stream.Close()

		// Process the stream to completion
		if err := stream.ProcessAll(func(msg *sprites.StreamMessage) error {
			return nil
		}); err != nil {
			return fmt.Errorf("failed to restore checkpoint %s: %w", checkpoint, err)
		}
	}

	return nil
}

// waitForSpriteReady polls to check if a sprite exists and can execute commands.
// This is a workaround for a Sprites API bug where CreateSprite returns 500 but
// actually creates the sprite. Returns true if sprite becomes ready, false otherwise.
func (c *SDKClient) waitForSpriteReady(ctx context.Context, name string) bool {
	const (
		maxWait      = 15 * time.Second
		pollInterval = 500 * time.Millisecond
	)

	fmt.Printf("Sprites API returned 500, polling to check if sprite %q was created...\n", name)
	deadline := time.Now().Add(maxWait)
	attempt := 0
	for time.Now().Before(deadline) {
		attempt++
		// Check if context is cancelled
		select {
		case <-ctx.Done():
			fmt.Printf("Context cancelled while waiting for sprite\n")
			return false
		default:
		}

		// Check if sprite exists
		exists, err := c.Exists(ctx, name)
		if err != nil {
			if attempt == 1 || attempt%10 == 0 {
				fmt.Printf("  [%d] Exists check error: %v\n", attempt, err)
			}
			time.Sleep(pollInterval)
			continue
		}
		if !exists {
			if attempt == 1 || attempt%10 == 0 {
				fmt.Printf("  [%d] Sprite does not exist yet\n", attempt)
			}
			time.Sleep(pollInterval)
			continue
		}

		// Try a filesystem operation to verify the sprite is fully ready.
		// Using "mkdir -p /tmp" tests that the filesystem is mounted and writable,
		// which is more robust than just "true" (which only tests SSH connectivity).
		_, stderr, exitCode, err := c.ExecuteOutput(ctx, name, "", nil, "mkdir", "-p", "/tmp")
		if err == nil && exitCode == 0 {
			fmt.Printf("Sprite %q is ready (attempt %d)\n", name, attempt)
			return true
		}
		if attempt == 1 || attempt%10 == 0 {
			fmt.Printf("  [%d] Filesystem check failed: err=%v, exitCode=%d, stderr=%s\n", attempt, err, exitCode, string(stderr))
		}

		time.Sleep(pollInterval)
	}

	fmt.Printf("Sprite %q did not become ready after %v\n", name, maxWait)
	return false
}

// Execute runs a command on the Sprite and returns pipes for streaming.
func (c *SDKClient) Execute(ctx context.Context, name string, dir string, env []string, args ...string) (*Cmd, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("no command specified")
	}

	sprite := c.client.Sprite(name)
	cmd := sprite.CommandContext(ctx, args[0], args[1:]...)

	if dir != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = env
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start command: %w", err)
	}

	return &Cmd{
		cmd:    cmd,
		Stdout: stdout,
		Stderr: stderr,
	}, nil
}

// ExecuteOutput runs a command on the Sprite and returns collected output (non-streaming).
// This uses the SDK's Run() method which avoids pipe handling issues.
func (c *SDKClient) ExecuteOutput(ctx context.Context, name string, dir string, env []string, args ...string) (stdout, stderr []byte, exitCode int, err error) {
	if len(args) == 0 {
		return nil, nil, -1, fmt.Errorf("no command specified")
	}

	sprite := c.client.Sprite(name)
	cmd := sprite.CommandContext(ctx, args[0], args[1:]...)

	if dir != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = env
	}

	// Use bytes.Buffer to capture output
	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	// Run the command and wait for completion
	runErr := cmd.Run()
	stdout = []byte(stdoutBuf.String())
	stderr = []byte(stderrBuf.String())

	if runErr != nil {
		if exitErr, ok := runErr.(*sprites.ExitError); ok {
			return stdout, stderr, exitErr.ExitCode(), nil
		}
		return stdout, stderr, -1, runErr
	}

	return stdout, stderr, 0, nil
}

// ExecuteOutputWithRetry runs a command with retries for transient failures.
// Exit code 255 typically indicates SSH/connection errors that may be transient
// during sprite initialization. This uses exponential backoff with max 5 retries.
func (c *SDKClient) ExecuteOutputWithRetry(ctx context.Context, name string, dir string, env []string, args ...string) (stdout, stderr []byte, exitCode int, err error) {
	const (
		maxRetries   = 5
		initialDelay = 100 * time.Millisecond
	)

	delay := initialDelay
	for attempt := 0; attempt <= maxRetries; attempt++ {
		stdout, stderr, exitCode, err = c.ExecuteOutput(ctx, name, dir, env, args...)

		// Success or non-retryable error
		if err == nil && exitCode != 255 {
			return stdout, stderr, exitCode, err
		}

		// Don't retry on actual errors (as opposed to non-zero exit codes)
		if err != nil {
			return stdout, stderr, exitCode, err
		}

		// Exit code 255 - transient failure, retry
		if attempt < maxRetries {
			// Check context before sleeping
			select {
			case <-ctx.Done():
				return stdout, stderr, exitCode, ctx.Err()
			default:
			}

			cmdStr := ""
			if len(args) > 0 {
				cmdStr = args[0]
			}
			fmt.Printf("Command %q returned exit code 255, retrying (%d/%d)...\n", cmdStr, attempt+1, maxRetries)
			time.Sleep(delay)
			delay *= 2 // Exponential backoff
		}
	}

	return stdout, stderr, exitCode, err
}

// WriteFile writes content to a file path on the Sprite.
func (c *SDKClient) WriteFile(ctx context.Context, name string, path string, content []byte) error {
	sprite := c.client.Sprite(name)
	fs := sprite.Filesystem()

	if err := fs.WriteFileContext(ctx, path, content, 0644); err != nil {
		return fmt.Errorf("failed to write file %s: %w", path, err)
	}

	return nil
}

// ReadFile reads content from a file path on the Sprite.
func (c *SDKClient) ReadFile(ctx context.Context, name string, path string) ([]byte, error) {
	sprite := c.client.Sprite(name)
	fs := sprite.Filesystem()

	// Note: SDK's FS interface doesn't expose ReadFileContext, so we use ReadFile.
	// The context deadline is still respected via the underlying HTTP client.
	data, err := fs.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", path, err)
	}

	return data, nil
}

// Delete deletes the Sprite.
func (c *SDKClient) Delete(ctx context.Context, name string) error {
	if err := c.client.DeleteSprite(ctx, name); err != nil {
		return fmt.Errorf("failed to delete sprite %s: %w", name, err)
	}
	return nil
}

// Exists checks if a Sprite exists.
func (c *SDKClient) Exists(ctx context.Context, name string) (bool, error) {
	_, err := c.client.GetSprite(ctx, name)
	if err != nil {
		// Check if it's a "not found" error (SDK returns various error formats)
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "not found") ||
			strings.Contains(errStr, "404") ||
			strings.Contains(errStr, "failed to retrieve sprite") {
			return false, nil
		}
		return false, fmt.Errorf("failed to check sprite existence: %w", err)
	}
	return true, nil
}

// GenerateSpriteName creates a sprite name from the branch with random suffix.
// Format: wisp-<branch-slug>-<6-random-chars>
func GenerateSpriteName(repo, branch string) string {
	// Strip wisp/ prefix to avoid double-prefixing (wisp-wisp-...)
	branch = strings.TrimPrefix(branch, "wisp/")
	slug := slugify(branch)
	randomSuffix := randomHex(6)
	return "wisp-" + slug + "-" + randomSuffix
}

// slugify converts a branch name to a URL-safe slug.
// It lowercases, replaces non-alphanumeric with dashes, collapses multiple dashes,
// and truncates to 20 chars max.
func slugify(s string) string {
	// Lowercase
	s = strings.ToLower(s)

	// Replace non-alphanumeric characters with dashes
	re := regexp.MustCompile(`[^a-z0-9]+`)
	s = re.ReplaceAllString(s, "-")

	// Trim leading/trailing dashes
	s = strings.Trim(s, "-")

	// Truncate to 20 chars max, avoiding trailing dash
	if len(s) > 20 {
		s = s[:20]
		s = strings.TrimRight(s, "-")
	}

	// Handle empty result
	if s == "" {
		s = "branch"
	}

	return s
}

// randomHex generates n random hex characters.
func randomHex(n int) string {
	bytes := make([]byte, (n+1)/2)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to a fixed string if crypto/rand fails (very unlikely)
		return strings.Repeat("0", n)
	}
	return hex.EncodeToString(bytes)[:n]
}
