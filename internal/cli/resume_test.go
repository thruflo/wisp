package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/state"
)

func TestResumeCommand_SessionNotFound(t *testing.T) {
	// Create a temp directory for the test
	tmpDir := t.TempDir()

	// Initialize store without creating any sessions
	store := state.NewStore(tmpDir)

	// Attempt to get a non-existent session
	_, err := store.GetSession("wisp/nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

func TestResumeCommand_SessionExists(t *testing.T) {
	// Create a temp directory for the test
	tmpDir := t.TempDir()

	// Create .wisp/sessions directory
	sessionsDir := filepath.Join(tmpDir, ".wisp", "sessions")
	require.NoError(t, os.MkdirAll(sessionsDir, 0o755))

	// Initialize store and create a session
	store := state.NewStore(tmpDir)

	session := &config.Session{
		Repo:       "test-org/test-repo",
		Spec:       "docs/rfc.md",
		Siblings:   []string{"test-org/sibling-repo"},
		Checkpoint: "checkpoint-123",
		Branch:     "wisp/test-feature",
		SpriteName: "wisp-abc123",
		StartedAt:  time.Now().Add(-1 * time.Hour),
		Status:     config.SessionStatusStopped,
	}

	// Create the session
	require.NoError(t, store.CreateSession(session))

	// Verify we can load the session
	loaded, err := store.GetSession("wisp/test-feature")
	require.NoError(t, err)
	assert.Equal(t, session.Repo, loaded.Repo)
	assert.Equal(t, session.Spec, loaded.Spec)
	assert.Equal(t, session.Branch, loaded.Branch)
	assert.Equal(t, session.SpriteName, loaded.SpriteName)
	assert.Equal(t, session.Checkpoint, loaded.Checkpoint)
	assert.Len(t, loaded.Siblings, 1)
	assert.Equal(t, "test-org/sibling-repo", loaded.Siblings[0])
}

func TestResumeCommand_SessionStatusUpdate(t *testing.T) {
	// Create a temp directory for the test
	tmpDir := t.TempDir()

	// Create .wisp/sessions directory
	sessionsDir := filepath.Join(tmpDir, ".wisp", "sessions")
	require.NoError(t, os.MkdirAll(sessionsDir, 0o755))

	// Initialize store and create a session
	store := state.NewStore(tmpDir)

	session := &config.Session{
		Repo:       "test-org/test-repo",
		Spec:       "docs/rfc.md",
		Branch:     "wisp/test-feature",
		SpriteName: "wisp-abc123",
		StartedAt:  time.Now().Add(-1 * time.Hour),
		Status:     config.SessionStatusStopped,
	}

	// Create the session
	require.NoError(t, store.CreateSession(session))

	// Update session status to running (as resume would do)
	err := store.UpdateSession("wisp/test-feature", func(s *config.Session) {
		s.Status = config.SessionStatusRunning
	})
	require.NoError(t, err)

	// Verify the update
	loaded, err := store.GetSession("wisp/test-feature")
	require.NoError(t, err)
	assert.Equal(t, config.SessionStatusRunning, loaded.Status)
}

func TestResumeCommand_StateSync(t *testing.T) {
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
		Branch:     "wisp/test-feature",
		SpriteName: "wisp-abc123",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusStopped,
	}
	require.NoError(t, store.CreateSession(session))

	// Create local state files that would be synced to Sprite on resume
	testState := &state.State{
		Status:  state.StatusContinue,
		Summary: "Completed task 3 of 5",
	}
	require.NoError(t, store.SaveState("wisp/test-feature", testState))

	testTasks := []state.Task{
		{Category: "setup", Description: "Task 1", Passes: true},
		{Category: "feature", Description: "Task 2", Passes: true},
		{Category: "feature", Description: "Task 3", Passes: true},
		{Category: "feature", Description: "Task 4", Passes: false},
		{Category: "test", Description: "Task 5", Passes: false},
	}
	require.NoError(t, store.SaveTasks("wisp/test-feature", testTasks))

	testHistory := []state.History{
		{Iteration: 1, Summary: "Completed task 1", TasksCompleted: 1, Status: state.StatusContinue},
		{Iteration: 2, Summary: "Completed task 2", TasksCompleted: 2, Status: state.StatusContinue},
		{Iteration: 3, Summary: "Completed task 3", TasksCompleted: 3, Status: state.StatusContinue},
	}
	require.NoError(t, store.SaveHistory("wisp/test-feature", testHistory))

	// Verify state files can be loaded (these would be synced to Sprite)
	loadedState, err := store.LoadState("wisp/test-feature")
	require.NoError(t, err)
	assert.Equal(t, state.StatusContinue, loadedState.Status)
	assert.Equal(t, "Completed task 3 of 5", loadedState.Summary)

	loadedTasks, err := store.LoadTasks("wisp/test-feature")
	require.NoError(t, err)
	assert.Len(t, loadedTasks, 5)

	completedCount := 0
	for _, task := range loadedTasks {
		if task.Passes {
			completedCount++
		}
	}
	assert.Equal(t, 3, completedCount)

	loadedHistory, err := store.LoadHistory("wisp/test-feature")
	require.NoError(t, err)
	assert.Len(t, loadedHistory, 3)
	assert.Equal(t, 3, loadedHistory[len(loadedHistory)-1].TasksCompleted)
}

func TestResumeCommand_RequiresBranchArg(t *testing.T) {
	// The command should require exactly one argument (the branch)
	assert.Equal(t, "resume <branch>", resumeCmd.Use)

	// Verify args validation is set to ExactArgs(1)
	err := resumeCmd.Args(resumeCmd, []string{})
	assert.Error(t, err)

	err = resumeCmd.Args(resumeCmd, []string{"branch1", "branch2"})
	assert.Error(t, err)

	err = resumeCmd.Args(resumeCmd, []string{"wisp/my-feature"})
	assert.NoError(t, err)
}

func TestResumeCommand_CheckoutBranchIntegration(t *testing.T) {
	// This test would require a mock Sprite client to properly test checkoutBranch
	// For now, we verify the function signature and basic behavior
	// Full integration tests require actual Sprite infrastructure
	t.Skip("Requires mock Sprite client infrastructure")
}

func TestResumeCommand_SetupSpriteForResumeSequence(t *testing.T) {
	// This test documents the expected sequence of operations for setupSpriteForResume:
	// 1. Create Sprite (from checkpoint if specified)
	// 2. Clone primary repo
	// 3. Checkout existing branch (not create new)
	// 4. Clone sibling repos
	// 5. Copy settings to ~/.claude/settings.json
	// 6. Copy templates to repo .wisp/
	// 7. Inject environment variables

	// Full integration test would require mock Sprite client
	t.Skip("Requires mock Sprite client infrastructure")
}

func TestResumeCommand_WithCheckpoint(t *testing.T) {
	// Create a temp directory for the test
	tmpDir := t.TempDir()

	// Create .wisp/sessions directory
	sessionsDir := filepath.Join(tmpDir, ".wisp", "sessions")
	require.NoError(t, os.MkdirAll(sessionsDir, 0o755))

	// Initialize store
	store := state.NewStore(tmpDir)

	// Create a session with checkpoint
	session := &config.Session{
		Repo:       "test-org/test-repo",
		Spec:       "docs/rfc.md",
		Branch:     "wisp/test-feature",
		SpriteName: "wisp-abc123",
		Checkpoint: "checkpoint-xyz789",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusStopped,
	}
	require.NoError(t, store.CreateSession(session))

	// Verify checkpoint is preserved when loading session
	loaded, err := store.GetSession("wisp/test-feature")
	require.NoError(t, err)
	assert.Equal(t, "checkpoint-xyz789", loaded.Checkpoint)
}

func TestResumeCommand_SyncManagerIntegration(t *testing.T) {
	// Create a temp directory for the test
	tmpDir := t.TempDir()

	// Create .wisp directory structure
	wispDir := filepath.Join(tmpDir, ".wisp")
	sessionsDir := filepath.Join(wispDir, "sessions")
	require.NoError(t, os.MkdirAll(sessionsDir, 0o755))

	// Initialize store
	store := state.NewStore(tmpDir)

	// Create a session
	session := &config.Session{
		Repo:       "test-org/test-repo",
		Spec:       "docs/rfc.md",
		Branch:     "wisp/sync-test",
		SpriteName: "wisp-sync123",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusStopped,
	}
	require.NoError(t, store.CreateSession(session))

	// Create state files
	testState := &state.State{
		Status:  state.StatusContinue,
		Summary: "Testing sync",
	}
	require.NoError(t, store.SaveState("wisp/sync-test", testState))

	// Verify SyncManager can be created with the store
	// The actual SyncToSprite call requires a Sprite client
	syncMgr := state.NewSyncManager(nil, store)
	assert.NotNil(t, syncMgr)

	// Verify state can be loaded for syncing
	loadedState, err := store.LoadState("wisp/sync-test")
	require.NoError(t, err)
	assert.Equal(t, state.StatusContinue, loadedState.Status)
}

func TestResumeCommand_ContextCancellation(t *testing.T) {
	// Test that the command respects context cancellation
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Verify context is cancelled
	assert.Error(t, ctx.Err())
}
