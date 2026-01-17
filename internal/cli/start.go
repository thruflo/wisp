package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/loop"
	"github.com/thruflo/wisp/internal/sprite"
	"github.com/thruflo/wisp/internal/state"
	"github.com/thruflo/wisp/internal/tui"
)

var (
	startRepo        string
	startSpec        string
	startSiblingRepo []string
	startBranch      string
	startTemplate    string
	startCheckpoint  string
	startHeadless    bool
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

Example:
  wisp start --repo org/repo --spec docs/rfc.md
  wisp start --repo org/repo --spec docs/rfc.md --branch feature/my-feature
  wisp start --repo org/repo --spec docs/rfc.md --sibling-repos org/other-repo`,
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

	// Generate branch name if not provided
	branch := startBranch
	if branch == "" {
		branch = generateBranchName(startSpec)
	}

	// Generate sprite name
	spriteName := sprite.GenerateSpriteName(startRepo, branch)

	// Create session
	session := &config.Session{
		Repo:       startRepo,
		Spec:       startSpec,
		Siblings:   startSiblingRepo,
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
	if err := syncMgr.SyncFromSprite(ctx, spriteName, branch, repoPath); err != nil {
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
// It creates the Sprite, clones repositories, creates a branch, copies settings
// and templates, and injects environment variables.
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
	// Create Sprite
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

	// Create and checkout branch
	fmt.Printf("Creating branch %s...\n", session.Branch)
	if err := CreateBranch(ctx, client, session.SpriteName, repoPath, session.Branch); err != nil {
		return "", fmt.Errorf("failed to create branch: %w", err)
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
	templateDir := filepath.Join(localBasePath, ".wisp", "templates", "default")
	fmt.Printf("Copying templates...\n")
	if err := syncMgr.CopyTemplatesToSprite(ctx, session.SpriteName, repoPath, templateDir); err != nil {
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

	return repoPath, nil
}

// CloneRepo clones a GitHub repository to the specified path on a Sprite.
// Exported for testing.
func CloneRepo(ctx context.Context, client sprite.Client, spriteName, repo, destPath string) error {
	// Ensure parent directory exists
	parentDir := filepath.Dir(destPath)
	mkdirCmd := fmt.Sprintf("mkdir -p %s", parentDir)
	cmd, err := client.Execute(ctx, spriteName, "", nil, "bash", "-c", mkdirCmd)
	if err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	// Clone repository
	cloneURL := fmt.Sprintf("https://github.com/%s.git", repo)
	cloneCmd := fmt.Sprintf("git clone %s %s", cloneURL, destPath)
	cmd, err = client.Execute(ctx, spriteName, "", nil, "bash", "-c", cloneCmd)
	if err != nil {
		return fmt.Errorf("failed to start git clone: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git clone failed: %w", err)
	}

	return nil
}

// CreateBranch creates and checks out a new branch in a repository on a Sprite.
// Exported for testing.
func CreateBranch(ctx context.Context, client sprite.Client, spriteName, repoPath, branch string) error {
	// Create and checkout branch (or checkout if it exists)
	branchCmd := fmt.Sprintf("git checkout -B %s", branch)
	cmd, err := client.Execute(ctx, spriteName, repoPath, nil, "bash", "-c", branchCmd)
	if err != nil {
		return fmt.Errorf("failed to start branch creation: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("branch creation failed: %w", err)
	}

	return nil
}

// InjectEnvVars writes environment variables to a Sprite's shell profile (.bashrc).
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

	// Write to .bashrc
	content := strings.Join(exports, "\n") + "\n"
	if err := client.WriteFile(ctx, spriteName, "/home/sprite/.bashrc", []byte(content)); err != nil {
		return fmt.Errorf("failed to write .bashrc: %w", err)
	}

	return nil
}

// RunCreateTasksPrompt runs Claude with the create-tasks prompt to generate tasks.json.
// Exported for testing.
func RunCreateTasksPrompt(ctx context.Context, client sprite.Client, session *config.Session, repoPath string) error {
	wispPath := filepath.Join(repoPath, ".wisp")
	createTasksPath := filepath.Join(wispPath, "create-tasks.md")
	contextPath := filepath.Join(wispPath, "context.md")
	specPath := filepath.Join(repoPath, session.Spec)

	// Build Claude command
	// The create-tasks prompt tells Claude to read the RFC and generate tasks
	args := []string{
		"bash", "-c",
		fmt.Sprintf(
			"source ~/.bashrc && claude -p \"$(cat %s)\n\nRFC path: %s\" --append-system-prompt-file %s --dangerously-skip-permissions --output-format stream-json --max-turns 50",
			createTasksPath, specPath, contextPath,
		),
	}

	cmd, err := client.Execute(ctx, session.SpriteName, repoPath, nil, args...)
	if err != nil {
		return fmt.Errorf("failed to start claude: %w", err)
	}

	// Wait for completion (we don't stream this output)
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("claude create-tasks failed: %w", err)
	}

	return nil
}
