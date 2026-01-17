package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/sprite"
	"github.com/thruflo/wisp/internal/state"
)

var (
	stopTeardown bool
)

var stopCmd = &cobra.Command{
	Use:   "stop <branch>",
	Short: "Stop a wisp session and sync state",
	Long: `Stop a wisp session, synchronizing state from the Sprite to local storage
before optionally tearing down the Sprite.

This command:
1. Syncs state files (state.json, tasks.json, history.json) from Sprite to local
2. Updates the session status to 'stopped'
3. Optionally tears down the Sprite (with --teardown flag)

Use 'wisp resume' to continue the session later.

Example:
  wisp stop wisp/my-feature
  wisp stop wisp/my-feature --teardown`,
	Args: cobra.ExactArgs(1),
	RunE: runStop,
}

func init() {
	stopCmd.Flags().BoolVar(&stopTeardown, "teardown", false, "Delete the Sprite after stopping")
	rootCmd.AddCommand(stopCmd)
}

func runStop(cmd *cobra.Command, args []string) error {
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

	// Load session
	session, err := store.GetSession(branch)
	if err != nil {
		return fmt.Errorf("failed to load session: %w", err)
	}

	fmt.Printf("Stopping wisp session...\n")
	fmt.Printf("  Branch: %s\n", session.Branch)
	fmt.Printf("  Sprite: %s\n", session.SpriteName)

	// Try to get sprite token from env
	env, _ := config.LoadEnvFile(cwd)
	spriteToken := env["SPRITE_TOKEN"]
	if spriteToken == "" {
		spriteToken = os.Getenv("SPRITE_TOKEN")
	}

	// If we have a sprite token, try to sync state from Sprite
	if spriteToken != "" {
		client := sprite.NewSDKClient(spriteToken)

		// Check if Sprite exists
		exists, err := client.Exists(ctx, session.SpriteName)
		if err != nil {
			fmt.Printf("Warning: failed to check Sprite status: %v\n", err)
		} else if exists {
			// Sync state from Sprite to local
			fmt.Printf("Syncing state from Sprite...\n")
			syncMgr := state.NewSyncManager(client, store)
			if err := syncMgr.SyncFromSprite(ctx, session.SpriteName, session.Branch); err != nil {
				fmt.Printf("Warning: failed to sync state: %v\n", err)
			} else {
				fmt.Printf("State synced successfully.\n")
			}

			// Teardown Sprite if requested
			if stopTeardown {
				fmt.Printf("Tearing down Sprite...\n")
				if err := client.Delete(ctx, session.SpriteName); err != nil {
					fmt.Printf("Warning: failed to teardown Sprite: %v\n", err)
				} else {
					fmt.Printf("Sprite teardown complete.\n")
				}
			}
		} else {
			fmt.Printf("Sprite not found (may have already been torn down).\n")
		}
	} else {
		fmt.Printf("Warning: SPRITE_TOKEN not found, skipping state sync.\n")
	}

	// Update session status to stopped
	if err := store.UpdateSession(branch, func(s *config.Session) {
		s.Status = config.SessionStatusStopped
	}); err != nil {
		return fmt.Errorf("failed to update session status: %w", err)
	}

	fmt.Printf("Session stopped.\n")
	fmt.Printf("\nUse 'wisp resume %s' to continue.\n", branch)

	return nil
}
