package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/loop"
	"github.com/thruflo/wisp/internal/sprite"
	"github.com/thruflo/wisp/internal/state"
	"github.com/thruflo/wisp/internal/tui"
)

var resumeCmd = &cobra.Command{
	Use:   "resume <branch>",
	Short: "Resume an existing wisp session",
	Long: `Resumes a previously stopped wisp session by creating a fresh Sprite,
restoring state from local storage, and continuing the iteration loop.

The branch argument is required and must match an existing session.

Example:
  wisp resume wisp/my-feature
  wisp resume feature/auth-implementation`,
	Args: cobra.ExactArgs(1),
	RunE: runResume,
}

func init() {
	rootCmd.AddCommand(resumeCmd)
}

func runResume(cmd *cobra.Command, args []string) error {
	branch := args[0]

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
	session, err := store.GetSession(branch)
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

	fmt.Printf("Resuming wisp session...\n")
	fmt.Printf("  Repository: %s\n", session.Repo)
	fmt.Printf("  Spec: %s\n", session.Spec)
	fmt.Printf("  Branch: %s\n", session.Branch)
	fmt.Printf("  Sprite: %s\n", session.SpriteName)

	// Determine template name (default to "default" if not stored)
	templateName := "default"

	// Setup fresh Sprite for resume
	repoPath, err := setupSpriteForResume(ctx, client, syncMgr, session, settings, env, cwd, templateName)
	if err != nil {
		return fmt.Errorf("failed to setup sprite: %w", err)
	}

	// Sync local state files to Sprite
	fmt.Printf("Syncing state to Sprite...\n")
	if err := syncMgr.SyncToSprite(ctx, session.SpriteName, session.Branch, repoPath); err != nil {
		return fmt.Errorf("failed to sync state to sprite: %w", err)
	}

	// Update session status to running
	if err := store.UpdateSession(branch, func(s *config.Session) {
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

	fmt.Printf("Resuming iteration loop...\n")

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

	if err := store.UpdateSession(branch, func(s *config.Session) {
		s.Status = finalStatus
	}); err != nil {
		fmt.Printf("Warning: failed to update session status: %v\n", err)
	}

	// Print result
	fmt.Printf("\nSession %s: %s\n", result.Reason.String(), branch)
	if result.Error != nil {
		fmt.Printf("Error: %v\n", result.Error)
	}
	fmt.Printf("Iterations: %d\n", result.Iterations)

	return nil
}

// setupSpriteForResume creates and configures a fresh Sprite for resuming a session.
// Unlike SetupSprite for start, this checks out an existing branch instead of creating one.
func setupSpriteForResume(
	ctx context.Context,
	client sprite.Client,
	syncMgr *state.SyncManager,
	session *config.Session,
	settings *config.Settings,
	env map[string]string,
	localBasePath string,
	templateName string,
) (string, error) {
	// Create Sprite (from checkpoint if specified)
	fmt.Printf("Creating Sprite %s...\n", session.SpriteName)
	if err := client.Create(ctx, session.SpriteName, session.Checkpoint); err != nil {
		return "", fmt.Errorf("failed to create sprite: %w", err)
	}

	// Parse repo org/name
	parts := strings.Split(session.Repo, "/")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid repo format %q, expected org/repo", session.Repo)
	}
	org, repo := parts[0], parts[1]

	// Repo path on Sprite
	repoPath := filepath.Join("/home/sprite", org, repo)

	// Clone primary repo
	fmt.Printf("Cloning %s...\n", session.Repo)
	if err := CloneRepo(ctx, client, session.SpriteName, session.Repo, repoPath); err != nil {
		return "", fmt.Errorf("failed to clone repo: %w", err)
	}

	// Checkout existing branch (it should exist in the remote since we pushed commits)
	fmt.Printf("Checking out branch %s...\n", session.Branch)
	if err := checkoutBranch(ctx, client, session.SpriteName, repoPath, session.Branch); err != nil {
		return "", fmt.Errorf("failed to checkout branch: %w", err)
	}

	// Clone sibling repos
	for _, sibling := range session.Siblings {
		siblingParts := strings.Split(sibling, "/")
		if len(siblingParts) != 2 {
			return "", fmt.Errorf("invalid sibling repo format %q, expected org/repo", sibling)
		}
		siblingOrg, siblingRepo := siblingParts[0], siblingParts[1]
		siblingPath := filepath.Join("/home/sprite", siblingOrg, siblingRepo)

		fmt.Printf("Cloning sibling %s...\n", sibling)
		if err := CloneRepo(ctx, client, session.SpriteName, sibling, siblingPath); err != nil {
			return "", fmt.Errorf("failed to clone sibling %s: %w", sibling, err)
		}
	}

	// Copy settings.json to ~/.claude/settings.json
	fmt.Printf("Copying settings...\n")
	if err := syncMgr.CopySettingsToSprite(ctx, session.SpriteName, settings); err != nil {
		return "", fmt.Errorf("failed to copy settings: %w", err)
	}

	// Copy templates to repo .wisp/
	templateDir := filepath.Join(localBasePath, ".wisp", "templates", templateName)
	fmt.Printf("Copying templates...\n")
	if err := syncMgr.CopyTemplatesToSprite(ctx, session.SpriteName, repoPath, templateDir); err != nil {
		return "", fmt.Errorf("failed to copy templates: %w", err)
	}

	// Inject environment variables by writing them to Sprite
	fmt.Printf("Injecting environment...\n")
	if err := InjectEnvVars(ctx, client, session.SpriteName, env); err != nil {
		return "", fmt.Errorf("failed to inject env vars: %w", err)
	}

	return repoPath, nil
}

// checkoutBranch checks out an existing branch in the repository.
// It tries to checkout a remote branch first, falling back to creating the branch
// if it doesn't exist on the remote (for branches that haven't been pushed yet).
func checkoutBranch(ctx context.Context, client sprite.Client, spriteName, repoPath, branch string) error {
	// Try to fetch the branch from remote first
	fetchCmd := fmt.Sprintf("git fetch origin %s:%s 2>/dev/null || true", branch, branch)
	cmd, err := client.Execute(ctx, spriteName, repoPath, nil, "bash", "-c", fetchCmd)
	if err != nil {
		return fmt.Errorf("failed to start fetch: %w", err)
	}
	_ = cmd.Wait() // Ignore error, branch might not exist on remote

	// Checkout the branch (create if it doesn't exist)
	checkoutCmd := fmt.Sprintf("git checkout %s 2>/dev/null || git checkout -b %s", branch, branch)
	cmd, err = client.Execute(ctx, spriteName, repoPath, nil, "bash", "-c", checkoutCmd)
	if err != nil {
		return fmt.Errorf("failed to start checkout: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("checkout failed: %w", err)
	}

	return nil
}
