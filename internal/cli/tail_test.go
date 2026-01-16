package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/state"
)

func TestTailCommand_RequiresBranchArg(t *testing.T) {
	// The command should require exactly one argument (the branch)
	assert.Equal(t, "tail <branch>", tailCmd.Use)

	// Verify args validation is set to ExactArgs(1)
	err := tailCmd.Args(tailCmd, []string{})
	assert.Error(t, err)

	err = tailCmd.Args(tailCmd, []string{"branch1", "branch2"})
	assert.Error(t, err)

	err = tailCmd.Args(tailCmd, []string{"wisp/my-feature"})
	assert.NoError(t, err)
}

func TestTailCommand_SessionNotFound(t *testing.T) {
	// Create a temp directory for the test
	tmpDir := t.TempDir()

	// Initialize store without creating any sessions
	store := state.NewStore(tmpDir)

	// Attempt to get a non-existent session
	_, err := store.GetSession("wisp/nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

func TestTailCommand_HasFollowFlag(t *testing.T) {
	// Verify the follow flag is registered
	flag := tailCmd.Flags().Lookup("follow")
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
	assert.Equal(t, "f", flag.Shorthand)
}

func TestTailCommand_HasIntervalFlag(t *testing.T) {
	// Verify the interval flag is registered
	flag := tailCmd.Flags().Lookup("interval")
	require.NotNil(t, flag)
	assert.Equal(t, "2s", flag.DefValue)
}

func TestTailCommand_ShortDescription(t *testing.T) {
	assert.Equal(t, "Show session status and watch for updates", tailCmd.Short)
}

func TestTailCommand_DisplaySessionState(t *testing.T) {
	// Create a temp directory for the test
	tmpDir := t.TempDir()

	// Create .wisp/sessions directory
	sessionsDir := filepath.Join(tmpDir, ".wisp", "sessions")
	require.NoError(t, os.MkdirAll(sessionsDir, 0o755))

	// Initialize store
	store := state.NewStore(tmpDir)

	// Create a session with state
	session := &config.Session{
		Repo:       "test-org/test-repo",
		Spec:       "docs/rfc.md",
		Branch:     "wisp/tail-test",
		SpriteName: "wisp-tail123",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session))

	// Create state files
	testState := &state.State{
		Status:  state.StatusContinue,
		Summary: "Implementing authentication module",
		Verification: &state.Verification{
			Method:  "tests",
			Passed:  true,
			Details: "5 tests passed",
		},
	}
	require.NoError(t, store.SaveState("wisp/tail-test", testState))

	testTasks := []state.Task{
		{Category: "setup", Description: "Initialize project", Passes: true},
		{Category: "feature", Description: "Add auth module", Passes: false},
		{Category: "test", Description: "Write tests", Passes: false},
	}
	require.NoError(t, store.SaveTasks("wisp/tail-test", testTasks))

	testHistory := []state.History{
		{Iteration: 1, Summary: "Project initialized", TasksCompleted: 1, Status: state.StatusContinue},
	}
	require.NoError(t, store.SaveHistory("wisp/tail-test", testHistory))

	// Load and verify data is available for display
	loaded, err := store.GetSession("wisp/tail-test")
	require.NoError(t, err)
	assert.Equal(t, "wisp/tail-test", loaded.Branch)
	assert.Equal(t, "wisp-tail123", loaded.SpriteName)

	loadedState, err := store.LoadState("wisp/tail-test")
	require.NoError(t, err)
	assert.Equal(t, state.StatusContinue, loadedState.Status)
	assert.Equal(t, "Implementing authentication module", loadedState.Summary)

	loadedTasks, err := store.LoadTasks("wisp/tail-test")
	require.NoError(t, err)
	assert.Len(t, loadedTasks, 3)

	// Count completed tasks
	completed := 0
	for _, task := range loadedTasks {
		if task.Passes {
			completed++
		}
	}
	assert.Equal(t, 1, completed)

	loadedHistory, err := store.LoadHistory("wisp/tail-test")
	require.NoError(t, err)
	assert.Len(t, loadedHistory, 1)
}

func TestTailCommand_FormatSessionStatus(t *testing.T) {
	tests := []struct {
		status   string
		expected string
	}{
		{config.SessionStatusRunning, "RUNNING"},
		{config.SessionStatusStopped, "STOPPED"},
		{config.SessionStatusCompleted, "COMPLETED"},
		{"unknown", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			result := formatSessionStatus(tt.status)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTailCommand_RepeatStr(t *testing.T) {
	tests := []struct {
		s        string
		n        int
		expected string
	}{
		{"=", 0, ""},
		{"=", 1, "="},
		{"=", 5, "====="},
		{"-", 3, "---"},
		{"ab", 2, "abab"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := repeatStr(tt.s, tt.n)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTailCommand_StateWithQuestion(t *testing.T) {
	// Create a temp directory for the test
	tmpDir := t.TempDir()

	// Create .wisp/sessions directory
	sessionsDir := filepath.Join(tmpDir, ".wisp", "sessions")
	require.NoError(t, os.MkdirAll(sessionsDir, 0o755))

	// Initialize store
	store := state.NewStore(tmpDir)

	// Create a session
	session := &config.Session{
		Repo:       "test-org/test-repo",
		Spec:       "docs/rfc.md",
		Branch:     "wisp/question-test",
		SpriteName: "wisp-question",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session))

	// Create state with NEEDS_INPUT
	testState := &state.State{
		Status:   state.StatusNeedsInput,
		Summary:  "Paused for user input",
		Question: "Should I use JWT or session-based auth?",
	}
	require.NoError(t, store.SaveState("wisp/question-test", testState))

	// Verify question is available
	loadedState, err := store.LoadState("wisp/question-test")
	require.NoError(t, err)
	assert.Equal(t, state.StatusNeedsInput, loadedState.Status)
	assert.Equal(t, "Should I use JWT or session-based auth?", loadedState.Question)
}

func TestTailCommand_StateWithError(t *testing.T) {
	// Create a temp directory for the test
	tmpDir := t.TempDir()

	// Create .wisp/sessions directory
	sessionsDir := filepath.Join(tmpDir, ".wisp", "sessions")
	require.NoError(t, os.MkdirAll(sessionsDir, 0o755))

	// Initialize store
	store := state.NewStore(tmpDir)

	// Create a session
	session := &config.Session{
		Repo:       "test-org/test-repo",
		Spec:       "docs/rfc.md",
		Branch:     "wisp/error-test",
		SpriteName: "wisp-error",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusStopped,
	}
	require.NoError(t, store.CreateSession(session))

	// Create state with BLOCKED
	testState := &state.State{
		Status:  state.StatusBlocked,
		Summary: "Failed to compile",
		Error:   "Missing dependency: github.com/some/package",
	}
	require.NoError(t, store.SaveState("wisp/error-test", testState))

	// Verify error is available
	loadedState, err := store.LoadState("wisp/error-test")
	require.NoError(t, err)
	assert.Equal(t, state.StatusBlocked, loadedState.Status)
	assert.Equal(t, "Missing dependency: github.com/some/package", loadedState.Error)
}

func TestTailCommand_HistoryDisplay(t *testing.T) {
	// Create a temp directory for the test
	tmpDir := t.TempDir()

	// Create .wisp/sessions directory
	sessionsDir := filepath.Join(tmpDir, ".wisp", "sessions")
	require.NoError(t, os.MkdirAll(sessionsDir, 0o755))

	// Initialize store
	store := state.NewStore(tmpDir)

	// Create a session
	session := &config.Session{
		Repo:       "test-org/test-repo",
		Spec:       "docs/rfc.md",
		Branch:     "wisp/history-test",
		SpriteName: "wisp-history",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session))

	// Create history with multiple entries (more than 5 to test truncation)
	history := []state.History{
		{Iteration: 1, Summary: "Task 1", TasksCompleted: 1, Status: state.StatusContinue},
		{Iteration: 2, Summary: "Task 2", TasksCompleted: 2, Status: state.StatusContinue},
		{Iteration: 3, Summary: "Task 3", TasksCompleted: 3, Status: state.StatusContinue},
		{Iteration: 4, Summary: "Task 4", TasksCompleted: 4, Status: state.StatusContinue},
		{Iteration: 5, Summary: "Task 5", TasksCompleted: 5, Status: state.StatusContinue},
		{Iteration: 6, Summary: "Task 6", TasksCompleted: 6, Status: state.StatusContinue},
		{Iteration: 7, Summary: "Task 7", TasksCompleted: 7, Status: state.StatusDone},
	}
	require.NoError(t, store.SaveHistory("wisp/history-test", history))

	// Verify history is available and shows recent entries
	loadedHistory, err := store.LoadHistory("wisp/history-test")
	require.NoError(t, err)
	assert.Len(t, loadedHistory, 7)

	// The tail should show last 5 entries (indices 2-6)
	start := 0
	if len(loadedHistory) > 5 {
		start = len(loadedHistory) - 5
	}
	assert.Equal(t, 2, start)
	assert.Equal(t, 3, loadedHistory[start].Iteration)
	assert.Equal(t, 7, loadedHistory[len(loadedHistory)-1].Iteration)
}
