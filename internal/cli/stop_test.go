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

func TestStopCommand_RequiresBranchArg(t *testing.T) {
	// The command should require exactly one argument (the branch)
	assert.Equal(t, "stop <branch>", stopCmd.Use)

	// Verify args validation is set to ExactArgs(1)
	err := stopCmd.Args(stopCmd, []string{})
	assert.Error(t, err)

	err = stopCmd.Args(stopCmd, []string{"branch1", "branch2"})
	assert.Error(t, err)

	err = stopCmd.Args(stopCmd, []string{"wisp/my-feature"})
	assert.NoError(t, err)
}

func TestStopCommand_SessionNotFound(t *testing.T) {
	// Create a temp directory for the test
	tmpDir := t.TempDir()

	// Initialize store without creating any sessions
	store := state.NewStore(tmpDir)

	// Attempt to get a non-existent session
	_, err := store.GetSession("wisp/nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

func TestStopCommand_UpdatesSessionStatus(t *testing.T) {
	// Create a temp directory for the test
	tmpDir := t.TempDir()

	// Create .wisp/sessions directory
	sessionsDir := filepath.Join(tmpDir, ".wisp", "sessions")
	require.NoError(t, os.MkdirAll(sessionsDir, 0o755))

	// Initialize store
	store := state.NewStore(tmpDir)

	// Create a running session
	session := &config.Session{
		Repo:       "test-org/test-repo",
		Spec:       "docs/rfc.md",
		Branch:     "wisp/stop-test",
		SpriteName: "wisp-stop123",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session))

	// Verify initial status
	loaded, err := store.GetSession("wisp/stop-test")
	require.NoError(t, err)
	assert.Equal(t, config.SessionStatusRunning, loaded.Status)

	// Update status to stopped (simulating what stop command does)
	err = store.UpdateSession("wisp/stop-test", func(s *config.Session) {
		s.Status = config.SessionStatusStopped
	})
	require.NoError(t, err)

	// Verify updated status
	loaded, err = store.GetSession("wisp/stop-test")
	require.NoError(t, err)
	assert.Equal(t, config.SessionStatusStopped, loaded.Status)
}

func TestStopCommand_HasTeardownFlag(t *testing.T) {
	// Verify the teardown flag is registered
	flag := stopCmd.Flags().Lookup("teardown")
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
}

func TestStopCommand_ShortDescription(t *testing.T) {
	assert.Equal(t, "Stop a wisp session and sync state", stopCmd.Short)
}

func TestStopCommand_StateSyncSequence(t *testing.T) {
	// Document expected behavior:
	// 1. Load session from local storage
	// 2. Check if Sprite exists
	// 3. If exists, sync state files from Sprite to local
	// 4. Update session status to stopped
	// 5. Optionally teardown Sprite if --teardown flag is set

	// Full test requires actual Sprite infrastructure
	t.Skip("Requires actual Sprite infrastructure for full sync test")
}

func TestStopCommand_StateFilesPreserved(t *testing.T) {
	// Create a temp directory for the test
	tmpDir := t.TempDir()

	// Create .wisp/sessions directory
	sessionsDir := filepath.Join(tmpDir, ".wisp", "sessions")
	require.NoError(t, os.MkdirAll(sessionsDir, 0o755))

	// Initialize store
	store := state.NewStore(tmpDir)

	// Create a session with state files
	session := &config.Session{
		Repo:       "test-org/test-repo",
		Spec:       "docs/rfc.md",
		Branch:     "wisp/state-test",
		SpriteName: "wisp-state123",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session))

	// Create state files
	testState := &state.State{
		Status:  state.StatusContinue,
		Summary: "Working on task 3",
	}
	require.NoError(t, store.SaveState("wisp/state-test", testState))

	testTasks := []state.Task{
		{Category: "setup", Description: "Task 1", Passes: true},
		{Category: "feature", Description: "Task 2", Passes: true},
		{Category: "feature", Description: "Task 3", Passes: false},
	}
	require.NoError(t, store.SaveTasks("wisp/state-test", testTasks))

	testHistory := []state.History{
		{Iteration: 1, Summary: "Task 1 complete", TasksCompleted: 1, Status: state.StatusContinue},
		{Iteration: 2, Summary: "Task 2 complete", TasksCompleted: 2, Status: state.StatusContinue},
	}
	require.NoError(t, store.SaveHistory("wisp/state-test", testHistory))

	// Update status to stopped
	err := store.UpdateSession("wisp/state-test", func(s *config.Session) {
		s.Status = config.SessionStatusStopped
	})
	require.NoError(t, err)

	// Verify state files are preserved after stopping
	loadedState, err := store.LoadState("wisp/state-test")
	require.NoError(t, err)
	assert.Equal(t, state.StatusContinue, loadedState.Status)
	assert.Equal(t, "Working on task 3", loadedState.Summary)

	loadedTasks, err := store.LoadTasks("wisp/state-test")
	require.NoError(t, err)
	assert.Len(t, loadedTasks, 3)
	assert.True(t, loadedTasks[0].Passes)
	assert.True(t, loadedTasks[1].Passes)
	assert.False(t, loadedTasks[2].Passes)

	loadedHistory, err := store.LoadHistory("wisp/state-test")
	require.NoError(t, err)
	assert.Len(t, loadedHistory, 2)
	assert.Equal(t, 2, loadedHistory[len(loadedHistory)-1].TasksCompleted)
}

func TestStopCommand_RepoPathCalculation(t *testing.T) {
	// Test that repo path is correctly calculated from session.Repo
	tests := []struct {
		repo     string
		expected string
	}{
		{"org/repo", "/home/sprite/org/repo"},
		{"electric-sql/electric", "/home/sprite/electric-sql/electric"},
		{"TanStack/db", "/home/sprite/TanStack/db"},
	}

	for _, tt := range tests {
		t.Run(tt.repo, func(t *testing.T) {
			// Parse repo
			parts := filepath.SplitList(tt.repo)
			if len(parts) == 0 {
				// Split by /
				var org, name string
				for i, c := range tt.repo {
					if c == '/' {
						org = tt.repo[:i]
						name = tt.repo[i+1:]
						break
					}
				}
				result := filepath.Join("/home/sprite", org, name)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}
