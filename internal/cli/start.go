package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/thruflo/wisp/internal/auth"
	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/loop"
	"github.com/thruflo/wisp/internal/server"
	"github.com/thruflo/wisp/internal/sprite"
	"github.com/thruflo/wisp/internal/state"
	"github.com/thruflo/wisp/internal/stream"
	"github.com/thruflo/wisp/internal/tui"
)

var (
	startRepo         string
	startSpec         string
	startSiblingRepo  []string
	startBranch       string
	startTemplate     string
	startCheckpoint   string
	startHeadless     bool
	startContinue     bool
	startServer       bool
	startServerPort   int
	startSetPassword  bool
)

// SpriteRunner paths and settings.
const (
	// SpriteRunnerPort is the HTTP port wisp-sprite listens on.
	SpriteRunnerPort = 8374

	// SpriteRunnerBinaryPath is the path where the wisp-sprite binary is uploaded on the Sprite.
	SpriteRunnerBinaryPath = "/var/local/wisp/bin/wisp-sprite"

	// SpriteRunnerPIDPath is the path to the PID file for the running wisp-sprite process.
	SpriteRunnerPIDPath = "/var/local/wisp/wisp-sprite.pid"

	// SpriteRunnerLogPath is the path to the log file for wisp-sprite output.
	SpriteRunnerLogPath = "/var/local/wisp/wisp-sprite.log"

	// LocalSpriteRunnerPath is the path to the local wisp-sprite binary to upload.
	// This is built by `make build-sprite`.
	LocalSpriteRunnerPath = "bin/wisp-sprite"
)

// HeadlessResult is the JSON output format for headless mode.
// It contains the loop result and session information for testing/CI.
type HeadlessResult struct {
	Reason     string `json:"reason"`               // Exit reason (e.g., "completed", "max iterations")
	Iterations int    `json:"iterations"`           // Number of iterations run
	Branch     string `json:"branch"`               // Session branch name
	SpriteName string `json:"sprite_name"`          // Sprite name
	Error      string `json:"error,omitempty"`      // Error message if any
	Status     string `json:"status,omitempty"`     // Final state status (DONE, CONTINUE, etc.)
	Summary    string `json:"summary,omitempty"`    // Final state summary
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a new wisp session",
	Long: `Creates a new Sprite, clones repositories, generates tasks from the RFC,
and begins the iteration loop.

The --repo and --spec flags are required. The spec path should be relative
to the repository root and point to the RFC/specification document.

Remote Access:
  Use --server to start a web server alongside the TUI for monitoring and
  interacting with the session from any device (phone, tablet, another computer).
  On first use, you'll be prompted to set a password. Use --port to customize
  the server port (default: 8374). Use --password to change the password.

Example:
  wisp start --repo org/repo --spec docs/rfc.md
  wisp start --repo org/repo --spec docs/rfc.md --branch feature/my-feature
  wisp start --repo org/repo --spec docs/rfc.md --sibling-repos org/other-repo
  wisp start --repo org/repo --spec docs/rfc.md --server
  wisp start --repo org/repo --spec docs/rfc.md --server --port 9000`,
	RunE: runStart,
}

func init() {
	startCmd.Flags().StringVarP(&startRepo, "repo", "r", "", "primary repository (org/repo format, required)")
	startCmd.Flags().StringVarP(&startSpec, "spec", "s", "", "path to RFC/spec file relative to repo root (required)")
	startCmd.Flags().StringSliceVar(&startSiblingRepo, "sibling-repos", nil, "sibling repositories to clone")
	startCmd.Flags().StringVarP(&startBranch, "branch", "b", "", "branch name (default: wisp/<slug-from-spec>)")
	startCmd.Flags().StringVarP(&startTemplate, "template", "t", "default", "template name to use")
	startCmd.Flags().StringVarP(&startCheckpoint, "checkpoint", "c", "", "checkpoint ID to restore from")
	startCmd.Flags().BoolVar(&startHeadless, "headless", false, "run without TUI, print JSON result to stdout (for testing/CI)")
	startCmd.Flags().BoolVar(&startContinue, "continue", false, "continue on existing branch instead of creating new")
	startCmd.Flags().BoolVar(&startServer, "server", false, "start web server alongside TUI for remote access")
	startCmd.Flags().IntVar(&startServerPort, "port", config.DefaultServerPort, "web server port (requires --server)")
	startCmd.Flags().BoolVar(&startSetPassword, "password", false, "prompt to set/change web server password")

	startCmd.MarkFlagRequired("repo")
	startCmd.MarkFlagRequired("spec")

	rootCmd.AddCommand(startCmd)
}

func runStart(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Parse repo and ref from --repo flag (supports org/repo@ref syntax)
	repo, ref := config.ParseRepoRef(startRepo)

	// Validate flag combinations
	if startContinue && startBranch == "" {
		return fmt.Errorf("--continue requires --branch")
	}
	if startContinue && ref != "" {
		return fmt.Errorf("--continue and @ref are mutually exclusive")
	}

	// Load configuration
	cfg, err := config.LoadConfig(cwd)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Handle server mode and password setup
	if startServer || startSetPassword {
		if err := handleServerPassword(cwd, cfg, startServer, startSetPassword, startServerPort); err != nil {
			return err
		}
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

	// Generate branch name if not provided
	branch := startBranch
	if branch == "" {
		branch = generateBranchName(startSpec)
	}

	// Generate sprite name
	spriteName := sprite.GenerateSpriteName(repo, branch)

	// Convert sibling repo strings to SiblingRepo structs (with @ref parsing)
	var siblings []config.SiblingRepo
	for _, s := range startSiblingRepo {
		sibRepo, sibRef := config.ParseRepoRef(s)
		siblings = append(siblings, config.SiblingRepo{Repo: sibRepo, Ref: sibRef})
	}

	// Create session
	session := &config.Session{
		Repo:       repo,
		Ref:        ref,
		Spec:       startSpec,
		Continue:   startContinue,
		Siblings:   siblings,
		Checkpoint: startCheckpoint,
		Branch:     branch,
		SpriteName: spriteName,
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}

	// Initialize storage
	store := state.NewStore(cwd)

	// Check if session already exists
	if store.SessionExists(branch) {
		return fmt.Errorf("session already exists for branch %q; use 'wisp resume %s' to continue", branch, branch)
	}

	// Create Sprite client
	client := sprite.NewSDKClient(spriteToken)

	// Create sync manager
	syncMgr := state.NewSyncManager(client, store)

	// Setup context with cancellation
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	fmt.Printf("Starting wisp session...\n")
	fmt.Printf("  Repository: %s\n", startRepo)
	fmt.Printf("  Spec: %s\n", startSpec)
	fmt.Printf("  Branch: %s\n", branch)
	fmt.Printf("  Sprite: %s\n", spriteName)

	// Setup Sprite
	repoPath, err := SetupSprite(ctx, client, syncMgr, session, settings, env, cwd)
	if err != nil {
		return fmt.Errorf("failed to setup sprite: %w", err)
	}

	// Create local session record
	if err := store.CreateSession(session); err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	// Run create-tasks prompt
	fmt.Printf("Generating tasks from RFC...\n")
	if err := RunCreateTasksPrompt(ctx, client, session, repoPath); err != nil {
		return fmt.Errorf("failed to generate tasks: %w", err)
	}

	// Sync initial state from Sprite to local
	if err := syncMgr.SyncFromSprite(ctx, spriteName, branch); err != nil {
		// Non-fatal, tasks might not exist yet
		fmt.Printf("Warning: failed to sync initial state: %v\n", err)
	}

	// Get template directory
	templateDir := filepath.Join(cwd, ".wisp", "templates", startTemplate)

	// Headless mode: run loop without TUI, output JSON result
	if startHeadless {
		return runHeadless(ctx, client, syncMgr, store, cfg, session, repoPath, templateDir)
	}

	// Create TUI
	t := tui.NewTUI(os.Stdout)

	// Create web server if enabled
	var srv *server.Server
	if startServer {
		var err error
		srv, err = server.NewServerFromConfig(cfg.Server)
		if err != nil {
			return fmt.Errorf("failed to create web server: %w", err)
		}

		// Start server in background
		serverErrCh := make(chan error, 1)
		go func() {
			serverErrCh <- srv.Start(ctx)
		}()

		// Give the server a moment to start and check for errors
		select {
		case err := <-serverErrCh:
			return fmt.Errorf("web server failed to start: %w", err)
		case <-time.After(100 * time.Millisecond):
			// Server started successfully
		}

		fmt.Printf("Web server running at http://localhost:%d\n", cfg.Server.Port)

		// Ensure server is stopped when we exit
		defer func() {
			if err := srv.Stop(); err != nil {
				fmt.Printf("Warning: failed to stop web server: %v\n", err)
			}
		}()
	}

	// Create and run loop
	l := loop.NewLoopWithOptions(loop.LoopOptions{
		Client:      client,
		SyncManager: syncMgr,
		Store:       store,
		Config:      cfg,
		Session:     session,
		TUI:         t,
		Server:      srv,
		RepoPath:    repoPath,
		TemplateDir: templateDir,
	})

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

// runHeadless runs the iteration loop without a TUI and outputs JSON result to stdout.
// This is used for testing and CI environments where interactive TUI is not available.
func runHeadless(
	ctx context.Context,
	client sprite.Client,
	syncMgr *state.SyncManager,
	store *state.Store,
	cfg *config.Config,
	session *config.Session,
	repoPath string,
	templateDir string,
) error {
	// Create a no-op TUI for the loop (required by Loop but not used in headless mode)
	t := tui.NewNopTUI()

	// Create and run loop
	l := loop.NewLoop(client, syncMgr, store, cfg, session, t, repoPath, templateDir)

	// Run loop
	result := l.Run(ctx)

	// Update session status based on result
	finalStatus := config.SessionStatusStopped
	switch result.Reason {
	case loop.ExitReasonDone:
		finalStatus = config.SessionStatusCompleted
	case loop.ExitReasonNeedsInput, loop.ExitReasonBlocked:
		finalStatus = config.SessionStatusStopped
	}

	if err := store.UpdateSession(session.Branch, func(s *config.Session) {
		s.Status = finalStatus
	}); err != nil {
		// Don't fail the whole command, just log
		fmt.Fprintf(os.Stderr, "Warning: failed to update session status: %v\n", err)
	}

	// Build headless result
	headlessResult := HeadlessResult{
		Reason:     result.Reason.String(),
		Iterations: result.Iterations,
		Branch:     session.Branch,
		SpriteName: session.SpriteName,
	}

	if result.Error != nil {
		headlessResult.Error = result.Error.Error()
	}

	if result.State != nil {
		headlessResult.Status = result.State.Status
		headlessResult.Summary = result.State.Summary
	}

	// Output JSON to stdout
	output, err := json.MarshalIndent(headlessResult, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal headless result: %w", err)
	}
	fmt.Println(string(output))

	return nil
}

// generateBranchName creates a branch name from the spec path.
// Format: wisp/<slug> where slug is derived from the spec filename.
func generateBranchName(specPath string) string {
	// Get the filename without extension
	base := filepath.Base(specPath)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)

	// Convert to slug
	slug := slugify(name)

	return "wisp/" + slug
}

// slugify converts a string to a URL-safe slug.
func slugify(s string) string {
	// Convert to lowercase
	s = strings.ToLower(s)

	// Replace spaces and underscores with hyphens
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")

	// Remove non-alphanumeric characters except hyphens
	reg := regexp.MustCompile(`[^a-z0-9-]`)
	s = reg.ReplaceAllString(s, "")

	// Collapse multiple hyphens
	reg = regexp.MustCompile(`-+`)
	s = reg.ReplaceAllString(s, "-")

	// Trim leading/trailing hyphens
	s = strings.Trim(s, "-")

	return s
}

// SetupSprite creates and configures a Sprite for a session.
// If a sprite already exists and is healthy (repo is cloned), it reuses it.
// Otherwise it creates a fresh sprite.
// Returns the repository path on the Sprite.
// Exported for testing.
func SetupSprite(
	ctx context.Context,
	client sprite.Client,
	syncMgr *state.SyncManager,
	session *config.Session,
	settings *config.Settings,
	env map[string]string,
	localBasePath string,
) (string, error) {
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
		// Sprite exists - check if it's healthy by verifying repo path exists
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
	}

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
	githubToken := env["GITHUB_TOKEN"]
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

	// Handle branch checkout based on session mode
	if session.Continue {
		// Continue mode: fetch and checkout existing branch
		fmt.Printf("Fetching and checking out existing branch %s...\n", session.Branch)
		if err := fetchAndCheckoutBranch(ctx, client, session.SpriteName, repoPath, session.Branch); err != nil {
			return "", fmt.Errorf("failed to checkout existing branch: %w", err)
		}
	} else if session.Ref != "" {
		// Ref mode: checkout base ref, then create new branch from it
		fmt.Printf("Checking out base ref %s...\n", session.Ref)
		if err := checkoutRef(ctx, client, session.SpriteName, repoPath, session.Ref); err != nil {
			return "", fmt.Errorf("failed to checkout ref: %w", err)
		}
		fmt.Printf("Creating branch %s...\n", session.Branch)
		if err := CreateBranch(ctx, client, session.SpriteName, repoPath, session.Branch); err != nil {
			return "", fmt.Errorf("failed to create branch: %w", err)
		}
	} else {
		// Default mode: create new branch from default branch
		fmt.Printf("Creating branch %s...\n", session.Branch)
		if err := CreateBranch(ctx, client, session.SpriteName, repoPath, session.Branch); err != nil {
			return "", fmt.Errorf("failed to create branch: %w", err)
		}
	}

	// Copy spec file from local to Sprite
	fmt.Printf("Copying spec file %s...\n", session.Spec)
	if err := CopySpecFile(ctx, client, session.SpriteName, localBasePath, session.Spec); err != nil {
		return "", fmt.Errorf("failed to copy spec file: %w", err)
	}

	// Clone sibling repos (with optional ref checkout)
	for _, sibling := range session.Siblings {
		siblingParts := strings.Split(sibling.Repo, "/")
		if len(siblingParts) != 2 {
			return "", fmt.Errorf("invalid sibling repo format %q, expected org/repo", sibling.Repo)
		}
		siblingOrg, siblingRepo := siblingParts[0], siblingParts[1]
		siblingPath := filepath.Join(sprite.ReposDir, siblingOrg, siblingRepo)

		if sibling.Ref != "" {
			fmt.Printf("Cloning sibling %s@%s...\n", sibling.Repo, sibling.Ref)
		} else {
			fmt.Printf("Cloning sibling %s...\n", sibling.Repo)
		}
		if err := CloneRepo(ctx, client, session.SpriteName, sibling.Repo, siblingPath, githubToken, sibling.Ref); err != nil {
			return "", fmt.Errorf("failed to clone sibling %s: %w", sibling.Repo, err)
		}
	}

	// Copy settings.json to ~/.claude/settings.json
	fmt.Printf("Copying settings...\n")
	if err := syncMgr.CopySettingsToSprite(ctx, session.SpriteName, settings); err != nil {
		return "", fmt.Errorf("failed to copy settings: %w", err)
	}

	// Copy templates to /var/local/wisp/templates/
	templateDir := filepath.Join(localBasePath, ".wisp", "templates", "default")
	fmt.Printf("Copying templates...\n")
	if err := syncMgr.CopyTemplatesToSprite(ctx, session.SpriteName, templateDir); err != nil {
		return "", fmt.Errorf("failed to copy templates: %w", err)
	}

	// Inject environment variables by writing them to Sprite
	fmt.Printf("Injecting environment...\n")
	if err := InjectEnvVars(ctx, client, session.SpriteName, env); err != nil {
		return "", fmt.Errorf("failed to inject env vars: %w", err)
	}

	// Copy Claude credentials for Claude Max authentication
	fmt.Printf("Copying Claude credentials...\n")
	if err := sprite.CopyClaudeCredentials(ctx, client, session.SpriteName); err != nil {
		return "", fmt.Errorf("failed to copy Claude credentials: %w", err)
	}

	// Upload wisp-sprite binary
	fmt.Printf("Uploading wisp-sprite binary...\n")
	if err := UploadSpriteRunner(ctx, client, session.SpriteName, localBasePath); err != nil {
		return "", fmt.Errorf("failed to upload wisp-sprite: %w", err)
	}

	// Start wisp-sprite (it will be started by the caller after task generation)
	// Note: We don't start it here because tasks need to be generated first

	return repoPath, nil
}

// CloneRepo clones a GitHub repository to the specified path on a Sprite.
// If githubToken is provided, it's embedded in the clone URL for authentication.
// If ref is provided, the specified ref (branch, tag, or commit) is checked out after cloning.
// Exported for testing.
func CloneRepo(ctx context.Context, client sprite.Client, spriteName, repo, destPath, githubToken, ref string) error {
	// Remove destination if it exists (handles stale state from previous runs)
	_, _, _, _ = client.ExecuteOutput(ctx, spriteName, "", nil, "rm", "-rf", destPath)

	// Ensure parent directory exists (use retry to handle transient sprite failures)
	parentDir := filepath.Dir(destPath)
	_, _, exitCode, err := client.ExecuteOutputWithRetry(ctx, spriteName, "", nil, "mkdir", "-p", parentDir)
	if err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("mkdir failed with exit code %d", exitCode)
	}

	// Clone repository - embed token in URL if provided
	var cloneURL string
	if githubToken != "" {
		cloneURL = fmt.Sprintf("https://x-access-token:%s@github.com/%s.git", githubToken, repo)
	} else {
		cloneURL = fmt.Sprintf("https://github.com/%s.git", repo)
	}
	_, stderr, exitCode, err := client.ExecuteOutput(ctx, spriteName, "", nil, "git", "clone", cloneURL, destPath)
	if err != nil {
		return fmt.Errorf("failed to run git clone: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("git clone failed with exit code %d: %s", exitCode, string(stderr))
	}

	// If ref is specified, fetch and checkout the ref
	if ref != "" {
		// Fetch the ref (might be a remote branch, tag, or commit)
		_, _, _, _ = client.ExecuteOutput(ctx, spriteName, destPath, nil, "git", "fetch", "origin", ref)

		// Checkout the ref
		_, stderr, exitCode, err := client.ExecuteOutput(ctx, spriteName, destPath, nil, "git", "checkout", ref)
		if err != nil {
			return fmt.Errorf("failed to checkout ref %s: %w", ref, err)
		}
		if exitCode != 0 {
			return fmt.Errorf("git checkout %s failed with exit code %d: %s", ref, exitCode, string(stderr))
		}
	}

	return nil
}

// CreateBranch creates and checks out a new branch in a repository on a Sprite.
// Exported for testing.
func CreateBranch(ctx context.Context, client sprite.Client, spriteName, repoPath, branch string) error {
	_, stderr, exitCode, err := client.ExecuteOutput(ctx, spriteName, repoPath, nil, "git", "checkout", "-B", branch)
	if err != nil {
		return fmt.Errorf("failed to run git checkout: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("git checkout failed with exit code %d: %s", exitCode, string(stderr))
	}

	return nil
}

// fetchAndCheckoutBranch fetches and checks out an existing remote branch.
// Used when continuing work on an existing branch (--continue mode).
func fetchAndCheckoutBranch(ctx context.Context, client sprite.Client, spriteName, repoPath, branch string) error {
	// Fetch the branch from remote
	_, stderr, exitCode, err := client.ExecuteOutput(ctx, spriteName, repoPath, nil, "git", "fetch", "origin", branch)
	if err != nil {
		return fmt.Errorf("failed to fetch branch %s: %w", branch, err)
	}
	if exitCode != 0 {
		return fmt.Errorf("git fetch %s failed with exit code %d: %s", branch, exitCode, string(stderr))
	}

	// Checkout the branch (tracking remote)
	_, stderr, exitCode, err = client.ExecuteOutput(ctx, spriteName, repoPath, nil, "git", "checkout", "-B", branch, "origin/"+branch)
	if err != nil {
		return fmt.Errorf("failed to checkout branch %s: %w", branch, err)
	}
	if exitCode != 0 {
		return fmt.Errorf("git checkout %s failed with exit code %d: %s", branch, exitCode, string(stderr))
	}

	return nil
}

// checkoutRef checks out a specific ref (branch, tag, or commit) in a repository.
// Used when creating a new branch from a specific base ref.
func checkoutRef(ctx context.Context, client sprite.Client, spriteName, repoPath, ref string) error {
	// Fetch the ref first (might be a remote branch, tag, or commit)
	_, _, _, _ = client.ExecuteOutput(ctx, spriteName, repoPath, nil, "git", "fetch", "origin", ref)

	// Checkout the ref
	_, stderr, exitCode, err := client.ExecuteOutput(ctx, spriteName, repoPath, nil, "git", "checkout", ref)
	if err != nil {
		return fmt.Errorf("failed to checkout ref %s: %w", ref, err)
	}
	if exitCode != 0 {
		return fmt.Errorf("git checkout %s failed with exit code %d: %s", ref, exitCode, string(stderr))
	}

	return nil
}

// RemoteSpecPath is the known location for the spec file on the Sprite.
var RemoteSpecPath = filepath.Join(sprite.SessionDir, "spec.md")

// CopySpecFile copies the local spec file to a known location on the Sprite.
// Exported for testing.
func CopySpecFile(ctx context.Context, client sprite.Client, spriteName, localBasePath, specPath string) error {
	// Read local spec file
	localSpecPath := filepath.Join(localBasePath, specPath)
	content, err := os.ReadFile(localSpecPath)
	if err != nil {
		return fmt.Errorf("failed to read local spec file %s: %w", localSpecPath, err)
	}

	// Write to known location
	if err := client.WriteFile(ctx, spriteName, RemoteSpecPath, content); err != nil {
		return fmt.Errorf("failed to write spec file to sprite: %w", err)
	}

	return nil
}

// InjectEnvVars writes environment variables to a Sprite's shell profile (.bashrc).
// The file is written to /var/local/wisp/.bashrc (our HOME directory on Sprites).
// Exported for testing.
func InjectEnvVars(ctx context.Context, client sprite.Client, spriteName string, env map[string]string) error {
	if len(env) == 0 {
		return nil
	}

	// Build export commands
	var exports []string
	for key, value := range env {
		// Escape single quotes in value
		escapedValue := strings.ReplaceAll(value, "'", "'\\''")
		exports = append(exports, fmt.Sprintf("export %s='%s'", key, escapedValue))
	}

	// Write to .bashrc in our HOME directory
	bashrcPath := filepath.Join(sprite.WispHome, ".bashrc")
	content := strings.Join(exports, "\n") + "\n"
	if err := client.WriteFile(ctx, spriteName, bashrcPath, []byte(content)); err != nil {
		return fmt.Errorf("failed to write .bashrc: %w", err)
	}

	return nil
}

// RunCreateTasksPrompt runs Claude with the create-tasks prompt to generate tasks.json.
// Exported for testing.
func RunCreateTasksPrompt(ctx context.Context, client sprite.Client, session *config.Session, repoPath string) error {
	createTasksPath := filepath.Join(sprite.TemplatesDir, "create-tasks.md")
	contextPath := filepath.Join(sprite.TemplatesDir, "context.md")

	return sprite.RunTasksPrompt(ctx, client, session.SpriteName, repoPath,
		createTasksPath, "RFC path: "+RemoteSpecPath, contextPath, 50)
}

// handleServerPassword handles password setup for the web server.
// It prompts for a password if needed and saves the hash to config.
func handleServerPassword(basePath string, cfg *config.Config, serverEnabled, setPassword bool, port int) error {
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

// UploadSpriteRunner uploads the wisp-sprite binary to the Sprite.
// The binary must have been built with `make build-sprite` prior to calling this.
// The binary is uploaded to /var/local/wisp/bin/wisp-sprite and made executable.
// localBasePath should be the base path for the local wisp installation (where .wisp/ is located).
func UploadSpriteRunner(ctx context.Context, client sprite.Client, spriteName, localBasePath string) error {
	// Read local binary
	binaryPath := filepath.Join(localBasePath, LocalSpriteRunnerPath)
	content, err := os.ReadFile(binaryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("wisp-sprite binary not found at %s - run 'make build-sprite' first", binaryPath)
		}
		return fmt.Errorf("failed to read wisp-sprite binary: %w", err)
	}

	// Ensure parent directory exists
	binDir := filepath.Dir(SpriteRunnerBinaryPath)
	_, _, exitCode, err := client.ExecuteOutput(ctx, spriteName, "", nil, "mkdir", "-p", binDir)
	if err != nil {
		return fmt.Errorf("failed to create bin directory: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("mkdir failed with exit code %d", exitCode)
	}

	// Write binary to Sprite
	if err := client.WriteFile(ctx, spriteName, SpriteRunnerBinaryPath, content); err != nil {
		return fmt.Errorf("failed to upload wisp-sprite binary: %w", err)
	}

	// Make binary executable
	_, stderr, exitCode, err := client.ExecuteOutput(ctx, spriteName, "", nil, "chmod", "+x", SpriteRunnerBinaryPath)
	if err != nil {
		return fmt.Errorf("failed to make wisp-sprite executable: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("chmod failed with exit code %d: %s", exitCode, string(stderr))
	}

	return nil
}

// StartSpriteRunner starts the wisp-sprite binary on the Sprite using nohup.
// The process is started in the background and will survive SSH disconnection.
// The session ID is passed via the -session-id flag.
// token is an optional authentication token for the HTTP server.
// repoPath is the working directory for Claude execution.
func StartSpriteRunner(ctx context.Context, client sprite.Client, spriteName, sessionID, repoPath, token string) error {
	// Check if wisp-sprite is already running by checking for PID file
	_, _, exitCode, _ := client.ExecuteOutput(ctx, spriteName, "", nil, "test", "-f", SpriteRunnerPIDPath)
	if exitCode == 0 {
		// PID file exists - check if process is actually running
		_, _, exitCode, _ := client.ExecuteOutput(ctx, spriteName, "", nil,
			"sh", "-c", fmt.Sprintf("kill -0 $(cat %s) 2>/dev/null", SpriteRunnerPIDPath))
		if exitCode == 0 {
			// Process is still running, no need to start again
			return nil
		}
		// Process not running, remove stale PID file
		client.ExecuteOutput(ctx, spriteName, "", nil, "rm", "-f", SpriteRunnerPIDPath)
	}

	// Build command arguments
	args := []string{
		SpriteRunnerBinaryPath,
		"-port", fmt.Sprintf("%d", SpriteRunnerPort),
		"-session-id", sessionID,
		"-work-dir", repoPath,
	}
	if token != "" {
		args = append(args, "-token", token)
	}

	// Build the nohup command that:
	// 1. Redirects stdout/stderr to log file
	// 2. Writes PID to file
	// 3. Runs in background
	cmdStr := fmt.Sprintf(
		"nohup %s > %s 2>&1 & echo $! > %s",
		strings.Join(args, " "),
		SpriteRunnerLogPath,
		SpriteRunnerPIDPath,
	)

	_, stderr, exitCode, err := client.ExecuteOutput(ctx, spriteName, "", nil, "sh", "-c", cmdStr)
	if err != nil {
		return fmt.Errorf("failed to start wisp-sprite: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("failed to start wisp-sprite (exit %d): %s", exitCode, string(stderr))
	}

	return nil
}

// WaitForSpriteRunner waits for the wisp-sprite HTTP server to become ready.
// It polls the /health endpoint until it returns successfully or the timeout is reached.
// Returns the URL of the stream server on success.
func WaitForSpriteRunner(ctx context.Context, client sprite.Client, spriteName string, timeout time.Duration) (string, error) {
	const pollInterval = 500 * time.Millisecond
	deadline := time.Now().Add(timeout)

	// Build the health check URL using the Sprite's internal IP
	// We'll use curl from within the Sprite to check the local server
	healthCheckCmd := fmt.Sprintf("curl -s -o /dev/null -w '%%{http_code}' http://localhost:%d/health", SpriteRunnerPort)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		stdout, _, exitCode, err := client.ExecuteOutput(ctx, spriteName, "", nil, "sh", "-c", healthCheckCmd)
		if err == nil && exitCode == 0 && strings.TrimSpace(string(stdout)) == "200" {
			// Server is ready
			// Return the stream URL that clients can connect to
			// Note: In production, this would use the Sprite's external IP or a tunnel
			// For now, return localhost URL that can be used with SSH port forwarding
			streamURL := fmt.Sprintf("http://localhost:%d", SpriteRunnerPort)
			return streamURL, nil
		}

		// Wait before next poll
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(pollInterval):
			// Continue polling
		}
	}

	// Timeout - try to get logs for debugging
	logs, _, _, _ := client.ExecuteOutput(ctx, spriteName, "", nil, "tail", "-20", SpriteRunnerLogPath)
	return "", fmt.Errorf("wisp-sprite did not become ready within %v\nLogs:\n%s", timeout, string(logs))
}

// ConnectToSpriteStream creates a stream client connected to the Sprite's stream server.
// The connection is made via HTTP to the Sprite's stream server.
// token is an optional authentication token.
func ConnectToSpriteStream(ctx context.Context, client sprite.Client, spriteName, token string) (*stream.StreamClient, error) {
	// Wait for the sprite runner to be ready
	streamURL, err := WaitForSpriteRunner(ctx, client, spriteName, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("wisp-sprite not ready: %w", err)
	}

	// Create stream client with authentication if provided
	var opts []stream.ClientOption
	if token != "" {
		opts = append(opts, stream.WithAuthToken(token))
	}

	// Set up HTTP client with custom transport for connection to Sprite
	// In production, this would use a tunnel or direct connection
	httpClient := &http.Client{
		Timeout: 0, // No timeout for streaming connections
	}
	opts = append(opts, stream.WithHTTPClient(httpClient))

	streamClient := stream.NewStreamClient(streamURL, opts...)

	// Test connection
	if err := streamClient.Connect(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect to stream server: %w", err)
	}

	return streamClient, nil
}
