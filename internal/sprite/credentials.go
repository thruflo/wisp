package sprite

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// CopyClaudeCredentials copies local Claude credentials to the Sprite for Claude Max auth.
// On macOS, credentials are stored in Keychain; on Linux they're in ~/.claude/.credentials.json.
// The credentials are written to /home/sprite/.claude/.credentials.json on the Sprite.
func CopyClaudeCredentials(ctx context.Context, client Client, spriteName string) error {
	credentials, err := GetLocalClaudeCredentials()
	if err != nil {
		return err
	}

	// Create .claude directory on Sprite
	cmd, err := client.Execute(ctx, spriteName, "", nil, "mkdir", "-p", "/home/sprite/.claude")
	if err != nil {
		return fmt.Errorf("failed to create .claude directory: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("failed to create .claude directory: %w", err)
	}

	// Write credentials to Sprite
	if err := client.WriteFile(ctx, spriteName, "/home/sprite/.claude/.credentials.json", credentials); err != nil {
		return fmt.Errorf("failed to write credentials to sprite: %w", err)
	}

	return nil
}

// GetLocalClaudeCredentials retrieves Claude credentials from the local machine.
// On macOS, credentials are stored in Keychain; on Linux they're in ~/.claude/.credentials.json.
func GetLocalClaudeCredentials() ([]byte, error) {
	var credentials []byte

	// Try to get credentials from macOS Keychain first
	if runtime.GOOS == "darwin" {
		cmd := exec.Command("security", "find-generic-password", "-s", "Claude Code-credentials", "-w")
		output, err := cmd.Output()
		if err == nil && len(output) > 0 {
			credentials = bytes.TrimSpace(output)
		}
	}

	// Fall back to file-based credentials
	if credentials == nil {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}

		// Try ~/.claude/.credentials.json (standard location)
		credPath := filepath.Join(homeDir, ".claude", ".credentials.json")
		if data, err := os.ReadFile(credPath); err == nil {
			credentials = data
		}

		// Try ~/.config/claude/.credentials.json (older location)
		if credentials == nil {
			credPath = filepath.Join(homeDir, ".config", "claude", ".credentials.json")
			if data, err := os.ReadFile(credPath); err == nil {
				credentials = data
			}
		}
	}

	if credentials == nil {
		return nil, fmt.Errorf("Claude credentials not found; run 'claude login' first")
	}

	return credentials, nil
}
