package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/loop"
	"github.com/thruflo/wisp/internal/server"
	"github.com/thruflo/wisp/internal/sprite"
	"github.com/thruflo/wisp/internal/state"
	"github.com/thruflo/wisp/internal/stream"
	"github.com/thruflo/wisp/internal/tui"
)

var (
	resumeServer      bool
	resumeServerPort  int
	resumeSetPassword bool
)

var resumeCmd = &cobra.Command{
	Use:   "resume <branch>",
	Short: "Resume an existing wisp session",
	Long: `Resumes a previously stopped wisp session by creating a fresh Sprite,
restoring state from local storage, and continuing the iteration loop.

The branch argument is required and must match an existing session.

Remote Access:
  Use --server to start a web server alongside the TUI for monitoring and
  interacting with the session from any device (phone, tablet, another computer).
  On first use, you'll be prompted to set a password. Use --port to customize
  the server port (default: 8374). Use --password to change the password.

Example:
  wisp resume wisp/my-feature
  wisp resume feature/auth-implementation
  wisp resume wisp/my-feature --server
  wisp resume wisp/my-feature --server --port 9000`,
	Args: cobra.ExactArgs(1),
	RunE: runResume,
}

func init() {
	resumeCmd.Flags().BoolVar(&resumeServer, "server", false, "start web server alongside TUI for remote access")
	resumeCmd.Flags().IntVar(&resumeServerPort, "port", config.DefaultServerPort, "web server port (requires --server)")
	resumeCmd.Flags().BoolVar(&resumeSetPassword, "password", false, "prompt to set/change web server password")

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

	// Handle server mode and password setup
	if resumeServer || resumeSetPassword {
		if err := HandleServerPassword(cwd, cfg, resumeServer, resumeSetPassword, resumeServerPort); err != nil {
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

	// Setup Sprite for resume (reuses existing sprite if available)
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

	// Check if state was initialized (tasks.json exists locally).
	// If not, the previous start crashed before completing - regenerate tasks.
	if !store.HasInitializedState(branch) {
		fmt.Printf("No tasks found, generating tasks from RFC...\n")
		if err := RunCreateTasksPrompt(ctx, client, session, repoPath); err != nil {
			return fmt.Errorf("failed to generate tasks: %w", err)
		}
		// Sync generated state from Sprite to local
		if err := syncMgr.SyncFromSprite(ctx, session.SpriteName, branch); err != nil {
			fmt.Printf("Warning: failed to sync initial state: %v\n", err)
		}
	}

	// Sync local state files to Sprite
	fmt.Printf("Syncing state to Sprite...\n")
	if err := syncMgr.SyncToSprite(ctx, session.SpriteName, session.Branch); err != nil {
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

	// Create web server if enabled
	var srv *server.Server
	if resumeServer {
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

// IsSpriteRunnerRunning checks if the wisp-sprite process is running on the Sprite.
// It checks for the PID file and verifies the process is alive.
func IsSpriteRunnerRunning(ctx context.Context, client sprite.Client, spriteName string) (bool, error) {
	// Check if PID file exists
	_, _, exitCode, err := client.ExecuteOutput(ctx, spriteName, "", nil, "test", "-f", SpriteRunnerPIDPath)
	if err != nil {
		return false, fmt.Errorf("failed to check PID file: %w", err)
	}
	if exitCode != 0 {
		// PID file doesn't exist
		return false, nil
	}

	// PID file exists - check if process is actually running
	_, _, exitCode, err = client.ExecuteOutput(ctx, spriteName, "", nil,
		"sh", "-c", fmt.Sprintf("kill -0 $(cat %s) 2>/dev/null", SpriteRunnerPIDPath))
	if err != nil {
		return false, fmt.Errorf("failed to check process: %w", err)
	}

	return exitCode == 0, nil
}

// ConnectOrRestartSpriteRunner connects to an existing wisp-sprite process or restarts it.
// Returns a stream client connected to the running process.
// If the process is not running, it uploads the binary (if needed), starts it, and waits for it to be ready.
func ConnectOrRestartSpriteRunner(
	ctx context.Context,
	client sprite.Client,
	session *config.Session,
	repoPath string,
	localBasePath string,
	token string,
) (*stream.StreamClient, error) {
	// Check if wisp-sprite is running
	running, err := IsSpriteRunnerRunning(ctx, client, session.SpriteName)
	if err != nil {
		return nil, fmt.Errorf("failed to check if wisp-sprite is running: %w", err)
	}

	if running {
		fmt.Printf("Connecting to existing wisp-sprite process...\n")
	} else {
		fmt.Printf("wisp-sprite not running, restarting...\n")

		// Check if binary exists, upload if not
		_, _, exitCode, _ := client.ExecuteOutput(ctx, session.SpriteName, "", nil, "test", "-x", SpriteRunnerBinaryPath)
		if exitCode != 0 {
			fmt.Printf("Uploading wisp-sprite binary...\n")
			if err := UploadSpriteRunner(ctx, client, session.SpriteName, localBasePath); err != nil {
				return nil, fmt.Errorf("failed to upload wisp-sprite: %w", err)
			}
		}

		// Start wisp-sprite
		if err := StartSpriteRunner(ctx, client, session.SpriteName, session.Branch, repoPath, token); err != nil {
			return nil, fmt.Errorf("failed to start wisp-sprite: %w", err)
		}
	}

	// Connect to the stream server
	streamClient, err := ConnectToSpriteStream(ctx, client, session.SpriteName, token)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to stream: %w", err)
	}

	return streamClient, nil
}
