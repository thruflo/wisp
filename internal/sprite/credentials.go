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

// SetupGitConfig copies git user.name and user.email from the local machine to the Sprite.
// Config is written to /var/local/wisp/.gitconfig due to permission issues with /home/sprite.
func SetupGitConfig(ctx context.Context, client Client, spriteName string) error {
	// Get local git config values
	userName, err := getLocalGitConfig("user.name")
	if err != nil {
		return fmt.Errorf("failed to get local git user.name: %w", err)
	}

	userEmail, err := getLocalGitConfig("user.email")
	if err != nil {
		return fmt.Errorf("failed to get local git user.email: %w", err)
	}

	// Set git config on Sprite using --file to write to /var/local/wisp/.gitconfig
	// (git --global writes to $HOME which has permission issues in Firecracker VMs)
	if userName != "" {
		_, _, exitCode, err := client.ExecuteOutput(ctx, spriteName, "", nil, "git", "config", "--file", "/var/local/wisp/.gitconfig", "user.name", userName)
		if err != nil {
			return fmt.Errorf("failed to set git user.name: %w", err)
		}
		if exitCode != 0 {
			return fmt.Errorf("git config user.name failed with exit code %d", exitCode)
		}
	}

	if userEmail != "" {
		_, _, exitCode, err := client.ExecuteOutput(ctx, spriteName, "", nil, "git", "config", "--file", "/var/local/wisp/.gitconfig", "user.email", userEmail)
		if err != nil {
			return fmt.Errorf("failed to set git user.email: %w", err)
		}
		if exitCode != 0 {
			return fmt.Errorf("git config user.email failed with exit code %d", exitCode)
		}
	}

	return nil
}

// getLocalGitConfig retrieves a git config value from the local machine.
func getLocalGitConfig(key string) (string, error) {
	cmd := exec.Command("git", "config", "--get", key)
	output, err := cmd.Output()
	if err != nil {
		// git config --get returns exit code 1 if key not found, which is ok
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return "", nil
		}
		return "", err
	}
	return string(bytes.TrimSpace(output)), nil
}

// CopyClaudeCredentials copies local Claude credentials to the Sprite for Claude Max auth.
// On macOS, credentials are stored in Keychain; on Linux they're in ~/.claude/.credentials.json.
// The credentials are written to /var/local/wisp/.claude/.credentials.json on the Sprite
// (due to permission issues with /home/sprite).
func CopyClaudeCredentials(ctx context.Context, client Client, spriteName string) error {
	credentials, err := GetLocalClaudeCredentials()
	if err != nil {
		return err
	}

	// Create .claude directory on Sprite in /var/local/wisp
	_, _, exitCode, err := client.ExecuteOutput(ctx, spriteName, "", nil, "mkdir", "-p", "/var/local/wisp/.claude")
	if err != nil {
		return fmt.Errorf("failed to create .claude directory: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("mkdir .claude failed with exit code %d", exitCode)
	}

	// Write credentials to Sprite
	if err := client.WriteFile(ctx, spriteName, "/var/local/wisp/.claude/.credentials.json", credentials); err != nil {
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
