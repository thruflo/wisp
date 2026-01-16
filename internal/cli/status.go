package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/state"
)

// SessionReader abstracts session storage for testability.
type SessionReader interface {
	ListSessions() ([]*config.Session, error)
	GetSession(branch string) (*config.Session, error)
	LoadState(branch string) (*state.State, error)
	LoadTasks(branch string) ([]state.Task, error)
	LoadHistory(branch string) ([]state.History, error)
}

// statusStore is the session reader used by the status command.
// It can be overridden in tests.
var statusStore SessionReader

var statusCmd = &cobra.Command{
	Use:   "status [branch]",
	Short: "Show session status",
	Long: `Shows the status of wisp sessions.

Without arguments, lists all sessions with their branch, status, and task progress.
With a branch argument, shows detailed information for that session.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) error {
	store := statusStore
	if store == nil {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		store = state.NewStore(cwd)
	}

	if len(args) == 0 {
		return listSessions(store)
	}

	return showSession(store, args[0])
}

func listSessions(store SessionReader) error {
	sessions, err := store.ListSessions()
	if err != nil {
		return fmt.Errorf("failed to list sessions: %w", err)
	}

	if len(sessions) == 0 {
		fmt.Println("No sessions found.")
		return nil
	}

	// Calculate column widths
	branchWidth := len("BRANCH")
	statusWidth := len("STATUS")
	for _, s := range sessions {
		if len(s.Branch) > branchWidth {
			branchWidth = len(s.Branch)
		}
		if len(s.Status) > statusWidth {
			statusWidth = len(s.Status)
		}
	}

	// Print header
	fmt.Printf("%-*s  %-*s  %s\n", branchWidth, "BRANCH", statusWidth, "STATUS", "TASKS")
	fmt.Printf("%s  %s  %s\n", strings.Repeat("-", branchWidth), strings.Repeat("-", statusWidth), "-----")

	// Print sessions
	for _, s := range sessions {
		tasks, err := store.LoadTasks(s.Branch)
		if err != nil {
			return fmt.Errorf("failed to load tasks for %s: %w", s.Branch, err)
		}

		completed, total := countTasks(tasks)
		taskProgress := fmt.Sprintf("%d/%d", completed, total)

		fmt.Printf("%-*s  %-*s  %s\n", branchWidth, s.Branch, statusWidth, s.Status, taskProgress)
	}

	return nil
}

func showSession(store SessionReader, branch string) error {
	session, err := store.GetSession(branch)
	if err != nil {
		return fmt.Errorf("failed to get session: %w", err)
	}

	st, err := store.LoadState(branch)
	if err != nil {
		return fmt.Errorf("failed to load state: %w", err)
	}

	tasks, err := store.LoadTasks(branch)
	if err != nil {
		return fmt.Errorf("failed to load tasks: %w", err)
	}

	history, err := store.LoadHistory(branch)
	if err != nil {
		return fmt.Errorf("failed to load history: %w", err)
	}

	// Calculate task progress
	completed, total := countTasks(tasks)

	// Get iteration count from history
	iterationCount := 0
	if len(history) > 0 {
		iterationCount = history[len(history)-1].Iteration
	}

	// Calculate elapsed time
	elapsed := time.Since(session.StartedAt)

	// Format output with aligned labels
	fmt.Println("Session Details")
	fmt.Println("===============")
	fmt.Println()

	printField("Repo", session.Repo)
	printField("Spec", session.Spec)
	printField("Branch", session.Branch)
	printField("Sprite", session.SpriteName)
	printField("Started", formatTime(session.StartedAt))
	printField("Elapsed", formatDuration(elapsed))
	printField("Status", session.Status)
	fmt.Println()

	fmt.Println("Progress")
	fmt.Println("--------")
	printField("Tasks", fmt.Sprintf("%d/%d completed", completed, total))
	printField("Iterations", fmt.Sprintf("%d", iterationCount))

	if st != nil && st.Summary != "" {
		printField("Last Summary", st.Summary)
	}

	if st != nil && st.Status != "" {
		printField("Agent Status", st.Status)
		if st.Status == state.StatusNeedsInput && st.Question != "" {
			printField("Question", st.Question)
		}
		if st.Status == state.StatusBlocked && st.Error != "" {
			printField("Error", st.Error)
		}
	}

	return nil
}

func countTasks(tasks []state.Task) (completed, total int) {
	for _, t := range tasks {
		total++
		if t.Passes {
			completed++
		}
	}
	return completed, total
}

func printField(label, value string) {
	fmt.Printf("  %-14s %s\n", label+":", value)
}

func formatTime(t time.Time) string {
	return t.Format("2006-01-02 15:04:05")
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)

	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second

	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
