package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/logging"
	"github.com/thruflo/wisp/internal/loop"
	"github.com/thruflo/wisp/internal/sprite"
	"github.com/thruflo/wisp/internal/state"
	"github.com/thruflo/wisp/internal/tui"
)

var (
	updateSpec   string
	updateBranch string
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update tasks after RFC changes",
	Long: `Updates the task list to reconcile with RFC changes.

This command reads the current RFC from the Sprite, compares it with the updated
local RFC, generates a diff, and runs the update-tasks prompt to reconcile the
task list with the changes.

Both --spec and --branch flags are required.

Example:
  wisp update --spec docs/rfc.md --branch wisp/my-feature
  wisp update --spec docs/updated-rfc.md --branch feature/auth`,
	RunE: runUpdate,
}

func init() {
	updateCmd.Flags().StringVarP(&updateSpec, "spec", "s", "", "path to updated RFC/spec file (required)")
	updateCmd.Flags().StringVarP(&updateBranch, "branch", "b", "", "branch name of existing session (required)")

	updateCmd.MarkFlagRequired("spec")
	updateCmd.MarkFlagRequired("branch")

	rootCmd.AddCommand(updateCmd)
}

func runUpdate(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Initialize storage
	store := state.NewStore(cwd)

	// Load existing session
	session, err := store.GetSession(updateBranch)
	if err != nil {
		return fmt.Errorf("failed to load session: %w", err)
	}

	// Read new RFC content from local path
	newRFCPath := filepath.Join(cwd, updateSpec)
	newRFCContent, err := os.ReadFile(newRFCPath)
	if err != nil {
		return fmt.Errorf("failed to read new RFC from %s: %w", updateSpec, err)
	}

	// Load configuration
	cfg, err := config.LoadConfig(cwd)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Load settings
	settings, err := config.LoadSettings(cwd)
	if err != nil {
		return fmt.Errorf("failed to load settings: %w", err)
	}

	// Load environment variables
	env, err := config.LoadEnvFile(cwd)
	if err != nil {
		return fmt.Errorf("failed to load env file: %w", err)
	}

	// Validate we have required env vars
	spriteToken := env["SPRITE_TOKEN"]
	if spriteToken == "" {
		spriteToken = os.Getenv("SPRITE_TOKEN")
	}
	if spriteToken == "" {
		return fmt.Errorf("SPRITE_TOKEN not found in .wisp/.sprite.env or environment")
	}

	// Create Sprite client
	client := sprite.NewSDKClient(spriteToken)

	// Create sync manager
	syncMgr := state.NewSyncManager(client, store)

	// Setup context with cancellation
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	fmt.Printf("Updating wisp session...\n")
	fmt.Printf("  Repository: %s\n", session.Repo)
	fmt.Printf("  Old spec: %s\n", session.Spec)
	fmt.Printf("  New spec: %s\n", updateSpec)
	fmt.Printf("  Branch: %s\n", session.Branch)
	fmt.Printf("  Sprite: %s\n", session.SpriteName)

	// Determine template name (default to "default" if not stored)
	templateName := "default"

	// Setup fresh Sprite for resume (same as resume command)
	repoPath, err := SetupSpriteWithConfig(ctx, SpriteSetupConfig{
		Mode:          SpriteSetupModeResume,
		Client:        client,
		SyncManager:   syncMgr,
		Session:       session,
		Settings:      settings,
		Env:           env,
		LocalBasePath: cwd,
		TemplateName:  templateName,
	})
	if err != nil {
		return fmt.Errorf("failed to setup sprite: %w", err)
	}

	// Sync local state files to Sprite
	fmt.Printf("Syncing state to Sprite...\n")
	if err := syncMgr.SyncToSprite(ctx, session.SpriteName, session.Branch); err != nil {
		return fmt.Errorf("failed to sync state to sprite: %w", err)
	}

	// Read old RFC content from Sprite
	fmt.Printf("Reading current RFC from Sprite...\n")
	oldRFCPath := filepath.Join(repoPath, session.Spec)
	oldRFCContent, err := client.ReadFile(ctx, session.SpriteName, oldRFCPath)
	if err != nil {
		return fmt.Errorf("failed to read old RFC from Sprite: %w", err)
	}

	// Generate diff between old and new RFC
	fmt.Printf("Generating RFC diff...\n")
	diff := GenerateDiff(string(oldRFCContent), string(newRFCContent))

	// Write diff to Sprite for Claude to read
	diffPath := filepath.Join(repoPath, ".wisp", "rfc-diff.md")
	if err := client.WriteFile(ctx, session.SpriteName, diffPath, []byte(diff)); err != nil {
		return fmt.Errorf("failed to write diff to Sprite: %w", err)
	}

	// Copy new RFC to Sprite (overwriting old one)
	fmt.Printf("Updating RFC on Sprite...\n")
	if err := client.WriteFile(ctx, session.SpriteName, oldRFCPath, newRFCContent); err != nil {
		return fmt.Errorf("failed to write new RFC to Sprite: %w", err)
	}

	// Run update-tasks prompt
	fmt.Printf("Running update-tasks prompt...\n")
	if err := runUpdateTasksPrompt(ctx, client, session, repoPath, diffPath); err != nil {
		return fmt.Errorf("failed to run update-tasks prompt: %w", err)
	}

	// Sync state from Sprite to local
	if err := syncMgr.SyncFromSprite(ctx, session.SpriteName, session.Branch); err != nil {
		fmt.Printf("Warning: failed to sync state from Sprite: %v\n", err)
		logging.Warn("failed to sync state from sprite", "error", err, "sprite", session.SpriteName, "branch", session.Branch)
	}

	// Update session spec if it changed
	if updateSpec != session.Spec {
		if err := store.UpdateSession(updateBranch, func(s *config.Session) {
			s.Spec = updateSpec
		}); err != nil {
			fmt.Printf("Warning: failed to update session spec: %v\n", err)
			logging.Warn("failed to update session spec", "error", err, "branch", session.Branch, "spec", updateSpec)
		}
	}

	// Update session status to running
	if err := store.UpdateSession(updateBranch, func(s *config.Session) {
		s.Status = config.SessionStatusRunning
	}); err != nil {
		return fmt.Errorf("failed to update session status: %w", err)
	}

	// Create TUI
	t := tui.NewTUI(os.Stdout)

	// Get template directory
	templateDir := filepath.Join(cwd, ".wisp", "templates", templateName)

	// Create and run loop
	l := loop.NewLoop(client, syncMgr, store, cfg, session, t, repoPath, templateDir)

	fmt.Printf("Starting iteration loop...\n")

	// Run TUI in background
	tuiCtx, tuiCancel := context.WithCancel(ctx)
	defer tuiCancel()

	tuiErrCh := make(chan error, 1)
	go func() {
		tuiErrCh <- t.Run(tuiCtx)
	}()

	// Run loop
	result := l.Run(ctx)

	// Cancel TUI
	tuiCancel()
	<-tuiErrCh // Wait for TUI to exit

	// Update session status based on result
	finalStatus := config.SessionStatusStopped
	switch result.Reason {
	case loop.ExitReasonDone:
		finalStatus = config.SessionStatusCompleted
	case loop.ExitReasonNeedsInput, loop.ExitReasonBlocked:
		finalStatus = config.SessionStatusStopped
	}

	if err := store.UpdateSession(updateBranch, func(s *config.Session) {
		s.Status = finalStatus
	}); err != nil {
		fmt.Printf("Warning: failed to update session status: %v\n", err)
		logging.Warn("failed to update session status", "error", err, "branch", session.Branch, "status", finalStatus)
	}

	// Print result
	fmt.Printf("\nSession %s: %s\n", result.Reason.String(), updateBranch)
	if result.Error != nil {
		fmt.Printf("Error: %v\n", result.Error)
	}
	fmt.Printf("Iterations: %d\n", result.Iterations)

	return nil
}

// GenerateDiff generates a simple line-by-line diff between two strings.
// Returns a human-readable diff format suitable for Claude to understand.
func GenerateDiff(old, new string) string {
	oldLines := strings.Split(old, "\n")
	newLines := strings.Split(new, "\n")

	// Build a simple unified-style diff
	var result strings.Builder
	result.WriteString("# RFC Changes\n\n")
	result.WriteString("The following changes have been made to the RFC:\n\n")

	// Find differences using a simple LCS-based approach
	diffs := computeLineDiff(oldLines, newLines)

	if len(diffs) == 0 {
		result.WriteString("No changes detected.\n")
		return result.String()
	}

	result.WriteString("```diff\n")
	for _, d := range diffs {
		result.WriteString(d)
		result.WriteString("\n")
	}
	result.WriteString("```\n")

	return result.String()
}

// computeLineDiff computes a simple line-based diff.
// Uses a modified patience diff algorithm for better readability.
func computeLineDiff(old, new []string) []string {
	// Build a map of old lines to their positions
	oldMap := make(map[string][]int)
	for i, line := range old {
		oldMap[line] = append(oldMap[line], i)
	}

	// Track which old lines have been matched
	matched := make([]bool, len(old))

	// Track new lines that are additions
	isAddition := make([]bool, len(new))

	// Simple matching: try to match each new line to an old line
	newMatched := make([]int, len(new)) // index in old, or -1 if no match
	for i := range newMatched {
		newMatched[i] = -1
	}

	// First pass: exact matches in order
	oldIdx := 0
	for newIdx, newLine := range new {
		// Look for this line in remaining old lines
		for oldIdx < len(old) {
			if old[oldIdx] == newLine && !matched[oldIdx] {
				matched[oldIdx] = true
				newMatched[newIdx] = oldIdx
				oldIdx++
				break
			}
			oldIdx++
		}
		if oldIdx >= len(old) {
			oldIdx = 0
		}
	}

	// Second pass: mark unmatched new lines as additions
	for i, m := range newMatched {
		if m == -1 {
			isAddition[i] = true
		}
	}

	// Build diff output
	var diffs []string
	var contextLines []string
	inDiff := false

	// Interleave removals and additions with context
	oldPos := 0
	newPos := 0

	for newPos < len(new) || oldPos < len(old) {
		// Handle removed lines (in old but not matched)
		for oldPos < len(old) && !matched[oldPos] {
			if !inDiff && len(contextLines) > 0 {
				// Add context before diff
				for _, cl := range contextLines[max(0, len(contextLines)-3):] {
					diffs = append(diffs, " "+cl)
				}
				contextLines = nil
			}
			inDiff = true
			diffs = append(diffs, "-"+old[oldPos])
			oldPos++
		}

		// Handle added lines
		for newPos < len(new) && isAddition[newPos] {
			if !inDiff && len(contextLines) > 0 {
				// Add context before diff
				for _, cl := range contextLines[max(0, len(contextLines)-3):] {
					diffs = append(diffs, " "+cl)
				}
				contextLines = nil
			}
			inDiff = true
			diffs = append(diffs, "+"+new[newPos])
			newPos++
		}

		// Handle context (matching lines)
		if newPos < len(new) && newMatched[newPos] != -1 {
			if inDiff {
				// Add trailing context
				contextCount := 0
				for newPos < len(new) && newMatched[newPos] != -1 && contextCount < 3 {
					diffs = append(diffs, " "+new[newPos])
					if newMatched[newPos] == oldPos {
						oldPos++
					}
					newPos++
					contextCount++
				}
				if newPos < len(new) && (newMatched[newPos] == -1 || (oldPos < len(old) && !matched[oldPos])) {
					diffs = append(diffs, "...")
				}
				inDiff = false
			} else {
				contextLines = append(contextLines, new[newPos])
				if newMatched[newPos] == oldPos {
					oldPos++
				}
				newPos++
			}
		} else if newPos >= len(new) && oldPos < len(old) {
			// Remaining old lines are deletions
			continue
		} else {
			break
		}
	}

	return diffs
}

// runUpdateTasksPrompt runs Claude with the update-tasks prompt.
func runUpdateTasksPrompt(ctx context.Context, client sprite.Client, session *config.Session, repoPath, diffPath string) error {
	updateTasksPath := filepath.Join(sprite.TemplatesDir, "update-tasks.md")
	contextPath := filepath.Join(sprite.TemplatesDir, "context.md")

	return sprite.RunTasksPrompt(ctx, client, session.SpriteName, repoPath,
		updateTasksPath, "RFC diff path: "+diffPath, contextPath, 50)
}
