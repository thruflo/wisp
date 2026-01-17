package sprite

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// WispHome is the base directory for wisp data on the Sprite.
// Used instead of /home/sprite due to Firecracker VM filesystem permission issues.
const WispHome = "/var/local/wisp"

// Directory structure under WispHome:
var (
	// SessionDir contains session state files (spec.md, tasks.json, state.json, etc.)
	SessionDir = filepath.Join(WispHome, "session")

	// TemplatesDir contains prompt templates (iterate.md, create-tasks.md, etc.)
	TemplatesDir = filepath.Join(WispHome, "templates")

	// ReposDir contains cloned repositories
	ReposDir = filepath.Join(WispHome, "repos")

	// ClaudeDir contains Claude Code credentials and settings
	ClaudeDir = filepath.Join(WispHome, ".claude")
)

// ClaudeCommand builds a bash command to run Claude with proper environment setup.
// It sets HOME to WispHome so Claude finds credentials at /var/local/wisp/.claude/.
// The returned args are suitable for passing to client.Execute or client.ExecuteOutput.
func ClaudeCommand(claudeArgs []string) []string {
	// Join claude args, properly quoting them
	claudeCmd := strings.Join(claudeArgs, " ")

	// Build bash command that:
	// 1. Sets HOME so Claude finds credentials in /var/local/wisp/.claude/
	// 2. Sources .bashrc for other env vars (GITHUB_TOKEN, etc)
	// 3. Runs the claude command
	bashCmd := fmt.Sprintf("export HOME=%s && source ~/.bashrc && %s", WispHome, claudeCmd)

	return []string{"bash", "-c", bashCmd}
}

// ClaudePromptCommand builds a bash command to run Claude with a prompt file.
// This is a convenience wrapper for the common pattern of running Claude with:
//   - A prompt from a file (using cat)
//   - An appended context/instruction
//   - A system prompt file
//   - Standard flags for non-interactive execution
func ClaudePromptCommand(promptPath, appendText, systemPromptPath string, maxTurns int) []string {
	claudeArgs := []string{
		"claude",
		"-p", fmt.Sprintf("\"$(cat %s)\\n\\n%s\"", promptPath, appendText),
		"--append-system-prompt-file", systemPromptPath,
		"--dangerously-skip-permissions",
		"--verbose",
		"--output-format", "stream-json",
		"--max-turns", fmt.Sprintf("%d", maxTurns),
	}
	return ClaudeCommand(claudeArgs)
}

// TasksFilePath is the standard location for tasks.json on the Sprite.
var TasksFilePath = filepath.Join(SessionDir, "tasks.json")

// RunTasksPrompt executes a Claude prompt on a Sprite and verifies success by checking
// that the tasks.json file exists and contains valid JSON.
//
// This handles the case where Claude CLI returns non-zero exit codes (e.g., 255) even
// when the operation succeeded. Instead of relying on exit code, we verify that the
// expected output file was created/updated.
//
// Parameters:
//   - promptPath: path to the prompt template on the Sprite
//   - appendText: text to append to the prompt (e.g., "RFC path: /path/to/spec.md")
//   - contextPath: path to the context/system prompt file on the Sprite
//   - maxTurns: maximum number of Claude turns
func RunTasksPrompt(ctx context.Context, client Client, spriteName, repoPath, promptPath, appendText, contextPath string, maxTurns int) error {
	args := ClaudePromptCommand(promptPath, appendText, contextPath, maxTurns)

	// Run Claude command - we ignore exit code and verify success via output file
	stdout, stderr, exitCode, err := client.ExecuteOutput(ctx, spriteName, repoPath, nil, args...)
	if err != nil {
		return fmt.Errorf("failed to run claude: %w", err)
	}

	// Verify success by checking if tasks.json was created/updated
	// Non-zero exit codes are okay if the output file exists and is valid
	content, readErr := client.ReadFile(ctx, spriteName, TasksFilePath)
	if readErr != nil {
		return fmt.Errorf("claude prompt failed (exit %d) - tasks.json not found: %w\nstdout: %s\nstderr: %s",
			exitCode, readErr, string(stdout), string(stderr))
	}

	if len(content) == 0 {
		return fmt.Errorf("claude prompt failed (exit %d) - tasks.json is empty\nstdout: %s\nstderr: %s",
			exitCode, string(stdout), string(stderr))
	}

	// Validate JSON structure (should be an array of tasks)
	var tasks []interface{}
	if err := json.Unmarshal(content, &tasks); err != nil {
		return fmt.Errorf("claude prompt failed (exit %d) - tasks.json contains invalid JSON: %w\nstdout: %s\nstderr: %s",
			exitCode, err, string(stdout), string(stderr))
	}

	return nil
}
