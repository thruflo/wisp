package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/state"
)

var (
	tailFollow   bool
	tailInterval time.Duration
)

var tailCmd = &cobra.Command{
	Use:   "tail <branch>",
	Short: "Show session status and watch for updates",
	Long: `Show the current session status and optionally watch for updates.

This is a read-only view of the session's progress. It displays:
- Session metadata (branch, status, sprite)
- Task progress (completed/total)
- Current state summary
- Iteration history

With --follow, it watches for state changes and updates the display.

Example:
  wisp tail wisp/my-feature
  wisp tail wisp/my-feature --follow
  wisp tail wisp/my-feature -f --interval 5s`,
	Args: cobra.ExactArgs(1),
	RunE: runTail,
}

func init() {
	tailCmd.Flags().BoolVarP(&tailFollow, "follow", "f", false, "Watch for updates")
	tailCmd.Flags().DurationVar(&tailInterval, "interval", 2*time.Second, "Poll interval for --follow")
	rootCmd.AddCommand(tailCmd)
}

func runTail(cmd *cobra.Command, args []string) error {
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

	// Display initial state
	displaySessionState(store, session)

	// If not following, we're done
	if !tailFollow {
		return nil
	}

	fmt.Printf("\n--- Following session (Ctrl+C to stop) ---\n\n")

	// Set up signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	// Track previous state for change detection
	var prevState *state.State
	prevState, _ = store.LoadState(branch)
	var prevTaskCount int
	if tasks, err := store.LoadTasks(branch); err == nil {
		for _, t := range tasks {
			if t.Passes {
				prevTaskCount++
			}
		}
	}

	// Watch loop
	ticker := time.NewTicker(tailInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-sigCh:
			fmt.Printf("\nStopped.\n")
			return nil
		case <-ticker.C:
			// Check for state changes
			currentState, _ := store.LoadState(branch)
			var currentTaskCount int
			if tasks, err := store.LoadTasks(branch); err == nil {
				for _, t := range tasks {
					if t.Passes {
						currentTaskCount++
					}
				}
			}

			// Check if anything changed
			changed := false
			if currentState != nil {
				if prevState == nil {
					changed = true
				} else if currentState.Status != prevState.Status ||
					currentState.Summary != prevState.Summary ||
					currentTaskCount != prevTaskCount {
					changed = true
				}
			}

			if changed {
				// Reload session in case status changed
				session, _ = store.GetSession(branch)

				// Clear screen and redisplay
				fmt.Print("\033[2J\033[H") // Clear screen, cursor to home
				displaySessionState(store, session)
				fmt.Printf("\n--- Following session (Ctrl+C to stop) ---\n")

				prevState = currentState
				prevTaskCount = currentTaskCount
			}
		}
	}
}

// displaySessionState prints a formatted view of the session state.
func displaySessionState(store *state.Store, session *config.Session) {
	// Header
	fmt.Printf("Session: %s\n", session.Branch)
	fmt.Printf("==========%s\n", repeatStr("=", len(session.Branch)))
	fmt.Printf("\n")

	// Metadata
	fmt.Printf("Repository:  %s\n", session.Repo)
	fmt.Printf("Spec:        %s\n", session.Spec)
	fmt.Printf("Sprite:      %s\n", session.SpriteName)
	fmt.Printf("Status:      %s\n", formatSessionStatus(session.Status))
	fmt.Printf("Started:     %s\n", session.StartedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("\n")

	// Load state
	st, _ := store.LoadState(session.Branch)
	tasks, _ := store.LoadTasks(session.Branch)
	history, _ := store.LoadHistory(session.Branch)

	// Task progress
	completed := 0
	for _, t := range tasks {
		if t.Passes {
			completed++
		}
	}
	fmt.Printf("Tasks:       %d/%d complete\n", completed, len(tasks))

	// Current iteration
	iteration := 0
	if len(history) > 0 {
		iteration = history[len(history)-1].Iteration
	}
	fmt.Printf("Iteration:   %d\n", iteration)
	fmt.Printf("\n")

	// Current state
	if st != nil {
		fmt.Printf("State: %s\n", st.Status)
		if st.Summary != "" {
			fmt.Printf("Summary: %s\n", st.Summary)
		}
		if st.Question != "" {
			fmt.Printf("Question: %s\n", st.Question)
		}
		if st.Error != "" {
			fmt.Printf("Error: %s\n", st.Error)
		}
		if st.Verification != nil {
			fmt.Printf("Verification: %s (%v) - %s\n",
				st.Verification.Method,
				st.Verification.Passed,
				st.Verification.Details)
		}
		fmt.Printf("\n")
	}

	// Recent history
	if len(history) > 0 {
		fmt.Printf("Recent History:\n")
		start := 0
		if len(history) > 5 {
			start = len(history) - 5
		}
		for _, h := range history[start:] {
			statusIcon := "."
			switch h.Status {
			case state.StatusContinue:
				statusIcon = ">"
			case state.StatusDone:
				statusIcon = "+"
			case state.StatusNeedsInput:
				statusIcon = "?"
			case state.StatusBlocked:
				statusIcon = "!"
			}
			summary := h.Summary
			if len(summary) > 60 {
				summary = summary[:57] + "..."
			}
			fmt.Printf("  %s [%d] tasks:%d - %s\n", statusIcon, h.Iteration, h.TasksCompleted, summary)
		}
	}
}

// formatSessionStatus returns a formatted status string.
func formatSessionStatus(status string) string {
	switch status {
	case config.SessionStatusRunning:
		return "RUNNING"
	case config.SessionStatusStopped:
		return "STOPPED"
	case config.SessionStatusCompleted:
		return "COMPLETED"
	default:
		return status
	}
}

// repeatStr repeats a string n times.
func repeatStr(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}
