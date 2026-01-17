package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/loop"
	"github.com/thruflo/wisp/internal/sprite"
	"github.com/thruflo/wisp/internal/state"
	"github.com/thruflo/wisp/internal/tui"
)

var reviewBranch string

var reviewCmd = &cobra.Command{
	Use:   "review <feedback.md>",
	Short: "Generate tasks from PR feedback",
	Long: `Generates new tasks to address PR review feedback.

This command reads a feedback file (e.g., PR review comments), copies it to the
Sprite, and runs the review-tasks prompt to generate tasks that address the feedback.

The feedback file path is required as an argument.
The --branch flag is required to identify the session.

Example:
  wisp review feedback.md --branch wisp/my-feature
  wisp review pr-comments.md --branch feature/auth`,
	Args: cobra.ExactArgs(1),
	RunE: runReview,
}

func init() {
	reviewCmd.Flags().StringVarP(&reviewBranch, "branch", "b", "", "branch name of existing session (required)")

	reviewCmd.MarkFlagRequired("branch")

	rootCmd.AddCommand(reviewCmd)
}

func runReview(cmd *cobra.Command, args []string) error {
	feedbackPath := args[0]

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Read feedback content from local path
	feedbackFullPath := filepath.Join(cwd, feedbackPath)
	feedbackContent, err := os.ReadFile(feedbackFullPath)
	if err != nil {
		return fmt.Errorf("failed to read feedback from %s: %w", feedbackPath, err)
	}

	// Initialize storage
	store := state.NewStore(cwd)

	// Load existing session
	session, err := store.GetSession(reviewBranch)
	if err != nil {
		return fmt.Errorf("failed to load session: %w", err)
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

	fmt.Printf("Processing PR feedback for wisp session...\n")
	fmt.Printf("  Repository: %s\n", session.Repo)
	fmt.Printf("  Feedback: %s\n", feedbackPath)
	fmt.Printf("  Branch: %s\n", session.Branch)
	fmt.Printf("  Sprite: %s\n", session.SpriteName)

	// Determine template name (default to "default" if not stored)
	templateName := "default"

	// Setup fresh Sprite for resume (same as resume command)
	repoPath, err := setupSpriteForResume(ctx, client, syncMgr, session, settings, env, cwd, templateName)
	if err != nil {
		return fmt.Errorf("failed to setup sprite: %w", err)
	}

	// Sync local state files to Sprite
	fmt.Printf("Syncing state to Sprite...\n")
	if err := syncMgr.SyncToSprite(ctx, session.SpriteName, session.Branch, repoPath); err != nil {
		return fmt.Errorf("failed to sync state to sprite: %w", err)
	}

	// Copy feedback to Sprite
	fmt.Printf("Copying feedback to Sprite...\n")
	feedbackSpritePath := filepath.Join(repoPath, ".wisp", "feedback.md")
	if err := client.WriteFile(ctx, session.SpriteName, feedbackSpritePath, feedbackContent); err != nil {
		return fmt.Errorf("failed to write feedback to Sprite: %w", err)
	}

	// Run review-tasks prompt
	fmt.Printf("Running review-tasks prompt...\n")
	if err := runReviewTasksPrompt(ctx, client, session, repoPath, feedbackSpritePath); err != nil {
		return fmt.Errorf("failed to run review-tasks prompt: %w", err)
	}

	// Sync state from Sprite to local
	if err := syncMgr.SyncFromSprite(ctx, session.SpriteName, session.Branch, repoPath); err != nil {
		fmt.Printf("Warning: failed to sync state from Sprite: %v\n", err)
	}

	// Update session status to running
	if err := store.UpdateSession(reviewBranch, func(s *config.Session) {
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

	if err := store.UpdateSession(reviewBranch, func(s *config.Session) {
		s.Status = finalStatus
	}); err != nil {
		fmt.Printf("Warning: failed to update session status: %v\n", err)
	}

	// Print result
	fmt.Printf("\nSession %s: %s\n", result.Reason.String(), reviewBranch)
	if result.Error != nil {
		fmt.Printf("Error: %v\n", result.Error)
	}
	fmt.Printf("Iterations: %d\n", result.Iterations)

	return nil
}

// runReviewTasksPrompt runs Claude with the review-tasks prompt.
func runReviewTasksPrompt(ctx context.Context, client sprite.Client, session *config.Session, repoPath, feedbackPath string) error {
	reviewTasksPath := filepath.Join(sprite.TemplatesDir, "review-tasks.md")
	contextPath := filepath.Join(sprite.TemplatesDir, "context.md")

	return sprite.RunTasksPrompt(ctx, client, session.SpriteName, repoPath,
		reviewTasksPath, "Feedback path: "+feedbackPath, contextPath, 50)
}
