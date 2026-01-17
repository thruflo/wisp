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
	abandonForce bool
	abandonAll   bool
)

var abandonCmd = &cobra.Command{
	Use:   "abandon [branch]",
	Short: "Abandon a session completely",
	Long: `Abandon a wisp session, removing both the remote Sprite and local session data.

This is the opposite of 'start' - use it to completely remove a session
that you no longer need.

Examples:
  wisp abandon wisp/my-feature
  wisp abandon                     # works if only one session exists
  wisp abandon --force             # skip confirmation
  wisp abandon --all               # abandon all sessions
  wisp abandon --all --force       # abandon all without confirmation`,
	Args: cobra.MaximumNArgs(1),
	RunE: runAbandon,
}

func init() {
	abandonCmd.Flags().BoolVar(&abandonForce, "force", false,
		"Skip confirmation prompt")
	abandonCmd.Flags().BoolVar(&abandonAll, "all", false,
		"Abandon all sessions")
	rootCmd.AddCommand(abandonCmd)
}

func runAbandon(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	store := state.NewStore(cwd)

	// Handle --all flag
	if abandonAll {
		if len(args) > 0 {
			return fmt.Errorf("cannot specify branch with --all flag")
		}
		return runAbandonAll(ctx, cwd, store)
	}

	// Resolve branch (auto-select if single session)
	branch, err := resolveBranch(store, args)
	if err != nil {
		return err
	}

	// Load session
	session, err := store.GetSession(branch)
	if err != nil {
		return fmt.Errorf("session not found: %s", branch)
	}

	// Confirmation prompt (unless --force)
	if !abandonForce {
		fmt.Printf("This will permanently abandon session '%s':\n", branch)
		fmt.Printf("  - Delete remote Sprite: %s\n", session.SpriteName)
		fmt.Printf("  - Delete local session data\n")
		fmt.Printf("\nType 'yes' to confirm: ")

		var response string
		fmt.Scanln(&response)
		if response != "yes" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	return abandonSession(ctx, cwd, store, session)
}

func runAbandonAll(ctx context.Context, cwd string, store *state.Store) error {
	sessions, err := store.ListSessions()
	if err != nil {
		return fmt.Errorf("failed to list sessions: %w", err)
	}

	if len(sessions) == 0 {
		fmt.Println("No sessions found.")
		return nil
	}

	// Confirmation prompt (unless --force)
	if !abandonForce {
		fmt.Printf("This will permanently abandon %d session(s):\n", len(sessions))
		for _, s := range sessions {
			fmt.Printf("  - %s (Sprite: %s)\n", s.Branch, s.SpriteName)
		}
		fmt.Printf("\nType 'yes' to confirm: ")

		var response string
		fmt.Scanln(&response)
		if response != "yes" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	// Abandon each session
	for _, session := range sessions {
		fmt.Printf("\nAbandoning session '%s'...\n", session.Branch)
		if err := abandonSession(ctx, cwd, store, session); err != nil {
			fmt.Printf("Warning: failed to abandon session '%s': %v\n", session.Branch, err)
		}
	}

	fmt.Printf("\nAll sessions abandoned.\n")
	return nil
}

func abandonSession(ctx context.Context, cwd string, store *state.Store, session *config.Session) error {
	// Delete sprite (if token available)
	env, _ := config.LoadEnvFile(cwd)
	spriteToken := env["SPRITE_TOKEN"]
	if spriteToken == "" {
		spriteToken = os.Getenv("SPRITE_TOKEN")
	}

	if spriteToken != "" && session.SpriteName != "" {
		client := sprite.NewSDKClient(spriteToken)
		exists, _ := client.Exists(ctx, session.SpriteName)
		if exists {
			fmt.Printf("Deleting Sprite '%s'...\n", session.SpriteName)
			if err := client.Delete(ctx, session.SpriteName); err != nil {
				fmt.Printf("Warning: failed to delete Sprite: %v\n", err)
			} else {
				fmt.Println("Sprite deleted.")
			}
		} else {
			fmt.Println("Sprite already deleted or doesn't exist.")
		}
	} else if session.SpriteName != "" {
		fmt.Println("Warning: SPRITE_TOKEN not found, skipping Sprite deletion.")
	}

	// Delete local session
	fmt.Printf("Deleting local session data...\n")
	if err := store.DeleteSession(session.Branch); err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}
	fmt.Println("Local session data deleted.")

	fmt.Printf("Session '%s' abandoned.\n", session.Branch)
	return nil
}
