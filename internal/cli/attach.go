package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
	"github.com/thruflo/wisp/internal/state"
	"golang.org/x/term"
)

var attachCmd = &cobra.Command{
	Use:   "attach <branch>",
	Short: "Shell into a Sprite for a wisp session",
	Long: `Attach to the Sprite for a wisp session using 'sprite console'.

This gives you direct shell access to the Sprite environment where
Claude Code is running. Useful for debugging or manual intervention.

Example:
  wisp attach wisp/my-feature
  wisp attach feature/auth-implementation`,
	Args: cobra.ExactArgs(1),
	RunE: runAttach,
}

func init() {
	rootCmd.AddCommand(attachCmd)
}

func runAttach(cmd *cobra.Command, args []string) error {
	branch := args[0]

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Initialize storage
	store := state.NewStore(cwd)

	// Load session to get sprite name
	session, err := store.GetSession(branch)
	if err != nil {
		return fmt.Errorf("failed to load session: %w", err)
	}

	// Save terminal state before exec
	oldState, err := term.GetState(int(os.Stdin.Fd()))
	if err != nil {
		// Terminal state save failed, but we can still try to attach
		oldState = nil
	}

	fmt.Printf("Attaching to Sprite %s...\n", session.SpriteName)
	fmt.Printf("Press Ctrl+D or type 'exit' to return.\n\n")

	// Shell out to sprite console
	spriteCmd := exec.Command("sprite", "console", session.SpriteName)
	spriteCmd.Stdin = os.Stdin
	spriteCmd.Stdout = os.Stdout
	spriteCmd.Stderr = os.Stderr

	// Run the command
	runErr := spriteCmd.Run()

	// Restore terminal state on exit
	if oldState != nil {
		if restoreErr := term.Restore(int(os.Stdin.Fd()), oldState); restoreErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to restore terminal: %v\n", restoreErr)
		}
	}

	// Check if sprite command failed
	if runErr != nil {
		return fmt.Errorf("sprite console exited with error: %w", runErr)
	}

	fmt.Printf("\nDetached from Sprite.\n")
	return nil
}
