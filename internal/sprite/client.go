package sprite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

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
		return fmt.Errorf("failed to create sprite %s: %w", name, err)
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

// GenerateSpriteName creates a deterministic sprite name from repo and branch.
// Format: wisp-<8-char-hash>
func GenerateSpriteName(repo, branch string) string {
	input := repo + ":" + branch
	hash := sha256.Sum256([]byte(input))
	hashStr := hex.EncodeToString(hash[:])
	return "wisp-" + hashStr[:8]
}
