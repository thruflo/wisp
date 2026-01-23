// Package cli provides command-line interface commands for wisp.
package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/thruflo/wisp/internal/auth"
	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/sprite"
	"github.com/thruflo/wisp/internal/state"
)

// SpriteSetupMode determines how the Sprite should handle branch setup.
type SpriteSetupMode int

const (
	// SpriteSetupModeStart creates a new branch from the default branch or specified ref.
	SpriteSetupModeStart SpriteSetupMode = iota
	// SpriteSetupModeResume checks out an existing branch.
	SpriteSetupModeResume
)

// SpriteSetupConfig contains the configuration for setting up a Sprite.
type SpriteSetupConfig struct {
	// Mode determines whether this is a new session or resuming an existing one.
	Mode SpriteSetupMode

	// Client is the Sprite API client.
	Client sprite.Client

	// SyncManager handles state synchronization with the Sprite.
	SyncManager *state.SyncManager

	// Session contains the session configuration.
	Session *config.Session

	// Settings contains Claude settings to copy to the Sprite.
	Settings *config.Settings

	// Env contains environment variables to inject on the Sprite.
	Env map[string]string

	// LocalBasePath is the local working directory (where .wisp/ is located).
	LocalBasePath string

	// TemplateName is the name of the template to use (e.g., "default").
	TemplateName string
}

// SetupSpriteWithConfig creates and configures a Sprite for a session.
// It handles both new sessions (SpriteSetupModeStart) and resumed sessions (SpriteSetupModeResume).
// Returns the repository path on the Sprite.
func SetupSpriteWithConfig(ctx context.Context, cfg SpriteSetupConfig) (string, error) {
	session := cfg.Session
	client := cfg.Client

	// Parse repo org/name (needed for repo path)
	parts := strings.Split(session.Repo, "/")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid repo format %q, expected org/repo", session.Repo)
	}
	org, repo := parts[0], parts[1]
	// Clone to /var/local/wisp/repos/{org}/{repo}
	repoPath := filepath.Join(sprite.ReposDir, org, repo)

	// Check if sprite already exists
	exists, err := client.Exists(ctx, session.SpriteName)
	if err != nil {
		return "", fmt.Errorf("failed to check sprite existence: %w", err)
	}

	if exists {
		return handleExistingSprite(ctx, cfg, repoPath)
	}

	return createFreshSprite(ctx, cfg, repoPath)
}

// handleExistingSprite handles the case where a Sprite already exists.
// For resume mode, it syncs state and updates files.
// For start mode, it checks health and recreates if broken.
func handleExistingSprite(ctx context.Context, cfg SpriteSetupConfig, repoPath string) (string, error) {
	session := cfg.Session
	client := cfg.Client
	syncMgr := cfg.SyncManager

	if cfg.Mode == SpriteSetupModeResume {
		// Resume mode: reuse existing sprite, sync state
		fmt.Printf("Resuming on existing Sprite %s...\n", session.SpriteName)

		// Sync local state to sprite
		if err := syncMgr.SyncToSprite(ctx, session.SpriteName, session.Branch); err != nil {
			// State sync failed - sprite may be in bad state, warn but continue
			fmt.Printf("Warning: failed to sync state to sprite: %v\n", err)
		}

		// Ensure spec file is present (may have been updated locally)
		if err := CopySpecFile(ctx, client, session.SpriteName, cfg.LocalBasePath, session.Spec); err != nil {
			fmt.Printf("Warning: failed to copy spec file: %v\n", err)
		}

		// Ensure templates are present (may have been updated locally)
		templateDir := filepath.Join(cfg.LocalBasePath, ".wisp", "templates", cfg.TemplateName)
		if err := syncMgr.CopyTemplatesToSprite(ctx, session.SpriteName, templateDir); err != nil {
			fmt.Printf("Warning: failed to copy templates: %v\n", err)
		}

		// Ensure environment variables are present at the correct location
		if err := InjectEnvVars(ctx, client, session.SpriteName, cfg.Env); err != nil {
			fmt.Printf("Warning: failed to inject env vars: %v\n", err)
		}

		// Ensure Claude credentials are present (may have been refreshed locally)
		if err := sprite.CopyClaudeCredentials(ctx, client, session.SpriteName); err != nil {
			fmt.Printf("Warning: failed to copy Claude credentials: %v\n", err)
		}

		return repoPath, nil
	}

	// Start mode: check if sprite is healthy by verifying repo path exists
	fmt.Printf("Found existing Sprite %s, checking health...\n", session.SpriteName)

	// Check if repo directory exists on sprite
	_, _, exitCode, err := client.ExecuteOutput(ctx, session.SpriteName, "", nil, "test", "-d", repoPath)
	if err == nil && exitCode == 0 {
		// Repo exists - sprite is healthy, reuse it
		fmt.Printf("Sprite is healthy, resuming on existing Sprite...\n")
		return repoPath, nil
	}

	// Repo doesn't exist or check failed - sprite is broken, delete and recreate
	fmt.Printf("Sprite appears broken, recreating...\n")
	if err := client.Delete(ctx, session.SpriteName); err != nil {
		return "", fmt.Errorf("failed to delete broken sprite: %w", err)
	}

	// Fall through to create fresh sprite
	return createFreshSprite(ctx, cfg, repoPath)
}

// createFreshSprite creates a new Sprite and sets it up from scratch.
func createFreshSprite(ctx context.Context, cfg SpriteSetupConfig, repoPath string) (string, error) {
	session := cfg.Session
	client := cfg.Client
	syncMgr := cfg.SyncManager

	// Create Sprite
	fmt.Printf("Creating Sprite %s...\n", session.SpriteName)
	if err := client.Create(ctx, session.SpriteName, session.Checkpoint); err != nil {
		return "", fmt.Errorf("failed to create sprite: %w", err)
	}

	// Create directory structure: /var/local/wisp/{session,templates,repos}
	fmt.Printf("Creating directories...\n")
	if err := syncMgr.EnsureDirectoriesOnSprite(ctx, session.SpriteName); err != nil {
		return "", fmt.Errorf("failed to create directories: %w", err)
	}

	// Get GitHub token for cloning
	githubToken := cfg.Env["GITHUB_TOKEN"]
	if githubToken == "" {
		githubToken = os.Getenv("GITHUB_TOKEN")
	}

	// Setup git config
	fmt.Printf("Setting up git config...\n")
	if err := sprite.SetupGitConfig(ctx, client, session.SpriteName); err != nil {
		return "", fmt.Errorf("failed to setup git config: %w", err)
	}

	// Clone primary repo (token embedded in URL for auth)
	fmt.Printf("Cloning %s...\n", session.Repo)
	if err := CloneRepo(ctx, client, session.SpriteName, session.Repo, repoPath, githubToken, ""); err != nil {
		return "", fmt.Errorf("failed to clone repo: %w", err)
	}

	// Handle branch based on mode
	if err := setupBranch(ctx, cfg, repoPath); err != nil {
		return "", err
	}

	// Copy spec file from local to Sprite
	fmt.Printf("Copying spec file %s...\n", session.Spec)
	if err := CopySpecFile(ctx, client, session.SpriteName, cfg.LocalBasePath, session.Spec); err != nil {
		return "", fmt.Errorf("failed to copy spec file: %w", err)
	}

	// Clone sibling repos (with optional ref checkout)
	if err := cloneSiblingRepos(ctx, cfg, githubToken); err != nil {
		return "", err
	}

	// Copy settings.json to ~/.claude/settings.json
	fmt.Printf("Copying settings...\n")
	if err := syncMgr.CopySettingsToSprite(ctx, session.SpriteName, cfg.Settings); err != nil {
		return "", fmt.Errorf("failed to copy settings: %w", err)
	}

	// Copy templates to /var/local/wisp/templates/
	templateDir := filepath.Join(cfg.LocalBasePath, ".wisp", "templates", cfg.TemplateName)
	fmt.Printf("Copying templates...\n")
	if err := syncMgr.CopyTemplatesToSprite(ctx, session.SpriteName, templateDir); err != nil {
		return "", fmt.Errorf("failed to copy templates: %w", err)
	}

	// Inject environment variables by writing them to Sprite
	fmt.Printf("Injecting environment...\n")
	if err := InjectEnvVars(ctx, client, session.SpriteName, cfg.Env); err != nil {
		return "", fmt.Errorf("failed to inject env vars: %w", err)
	}

	// Copy Claude credentials for Claude Max authentication
	fmt.Printf("Copying Claude credentials...\n")
	if err := sprite.CopyClaudeCredentials(ctx, client, session.SpriteName); err != nil {
		return "", fmt.Errorf("failed to copy Claude credentials: %w", err)
	}

	// Upload wisp-sprite binary (only for start mode, resume uses existing binary or will upload later)
	if cfg.Mode == SpriteSetupModeStart {
		fmt.Printf("Uploading wisp-sprite binary...\n")
		if err := UploadSpriteRunner(ctx, client, session.SpriteName, cfg.LocalBasePath); err != nil {
			return "", fmt.Errorf("failed to upload wisp-sprite: %w", err)
		}
	}

	return repoPath, nil
}

// setupBranch handles branch creation/checkout based on the setup mode and session configuration.
func setupBranch(ctx context.Context, cfg SpriteSetupConfig, repoPath string) error {
	session := cfg.Session
	client := cfg.Client

	if cfg.Mode == SpriteSetupModeResume {
		// Resume mode: checkout existing branch
		fmt.Printf("Checking out branch %s...\n", session.Branch)
		return checkoutBranch(ctx, client, session.SpriteName, repoPath, session.Branch)
	}

	// Start mode: handle branch based on session configuration
	if session.Continue {
		// Continue mode: fetch and checkout existing branch
		fmt.Printf("Fetching and checking out existing branch %s...\n", session.Branch)
		return fetchAndCheckoutBranch(ctx, client, session.SpriteName, repoPath, session.Branch)
	}

	if session.Ref != "" {
		// Ref mode: checkout base ref, then create new branch from it
		fmt.Printf("Checking out base ref %s...\n", session.Ref)
		if err := checkoutRef(ctx, client, session.SpriteName, repoPath, session.Ref); err != nil {
			return fmt.Errorf("failed to checkout ref: %w", err)
		}
		fmt.Printf("Creating branch %s...\n", session.Branch)
		return CreateBranch(ctx, client, session.SpriteName, repoPath, session.Branch)
	}

	// Default mode: create new branch from default branch
	fmt.Printf("Creating branch %s...\n", session.Branch)
	return CreateBranch(ctx, client, session.SpriteName, repoPath, session.Branch)
}

// checkoutBranch checks out an existing branch in the repository.
// It tries to checkout a remote branch first, falling back to creating the branch
// if it doesn't exist on the remote (for branches that haven't been pushed yet).
func checkoutBranch(ctx context.Context, client sprite.Client, spriteName, repoPath, branch string) error {
	// Try to fetch the branch from remote first (ignore errors, branch might not exist)
	fetchCmd := fmt.Sprintf("git fetch origin %s:%s 2>/dev/null || true", branch, branch)
	_, _, _, _ = client.ExecuteOutput(ctx, spriteName, repoPath, nil, "bash", "-c", fetchCmd)

	// Checkout the branch (create if it doesn't exist)
	checkoutCmd := fmt.Sprintf("git checkout %s 2>/dev/null || git checkout -b %s", branch, branch)
	_, stderr, exitCode, err := client.ExecuteOutput(ctx, spriteName, repoPath, nil, "bash", "-c", checkoutCmd)
	if err != nil {
		return fmt.Errorf("failed to run checkout: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("checkout failed with exit code %d: %s", exitCode, string(stderr))
	}

	return nil
}

// cloneSiblingRepos clones all sibling repositories specified in the session.
func cloneSiblingRepos(ctx context.Context, cfg SpriteSetupConfig, githubToken string) error {
	session := cfg.Session
	client := cfg.Client

	for _, sibling := range session.Siblings {
		siblingParts := strings.Split(sibling.Repo, "/")
		if len(siblingParts) != 2 {
			return fmt.Errorf("invalid sibling repo format %q, expected org/repo", sibling.Repo)
		}
		siblingOrg, siblingRepo := siblingParts[0], siblingParts[1]
		siblingPath := filepath.Join(sprite.ReposDir, siblingOrg, siblingRepo)

		if sibling.Ref != "" {
			fmt.Printf("Cloning sibling %s@%s...\n", sibling.Repo, sibling.Ref)
		} else {
			fmt.Printf("Cloning sibling %s...\n", sibling.Repo)
		}
		if err := CloneRepo(ctx, client, session.SpriteName, sibling.Repo, siblingPath, githubToken, sibling.Ref); err != nil {
			return fmt.Errorf("failed to clone sibling %s: %w", sibling.Repo, err)
		}
	}

	return nil
}

// HandleServerPassword handles password setup for the web server.
// It prompts for a password if needed and saves the hash to config.
// This is shared between start and resume commands.
func HandleServerPassword(basePath string, cfg *config.Config, serverEnabled, setPassword bool, port int) error {
	// Initialize server config if not present
	if cfg.Server == nil {
		cfg.Server = config.DefaultServerConfig()
	}

	// Update port from flag
	cfg.Server.Port = port

	// Check if we need to prompt for password
	needsPassword := false

	if setPassword {
		// User explicitly wants to set/change password
		needsPassword = true
	} else if serverEnabled && cfg.Server.PasswordHash == "" {
		// Server mode enabled but no password configured
		needsPassword = true
	}

	if needsPassword {
		password, err := auth.PromptAndConfirmPassword()
		if err != nil {
			return fmt.Errorf("password setup failed: %w", err)
		}

		hash, err := auth.HashPassword(password)
		if err != nil {
			return fmt.Errorf("failed to hash password: %w", err)
		}

		cfg.Server.PasswordHash = hash

		// Save the updated config
		if err := config.SaveConfig(basePath, cfg); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Println("Password saved to config.")
	}

	return nil
}
