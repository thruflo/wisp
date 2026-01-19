package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thruflo/wisp/internal/config"
)

func TestSanitizeBranch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		branch string
		want   string
	}{
		{"simple branch", "main", "main"},
		{"branch with slash", "wisp/feature", "wisp-feature"},
		{"multiple slashes", "feat/sub/task", "feat-sub-task"},
		{"no slashes", "feature-branch", "feature-branch"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := sanitizeBranch(tt.branch)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestStore_CreateAndGetSession(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)

	startTime := time.Date(2026, 1, 16, 10, 0, 0, 0, time.UTC)
	session := &config.Session{
		Repo:       "electric-sql/electric",
		Spec:       "docs/rfc.md",
		Siblings:   []config.SiblingRepo{{Repo: "TanStack/db"}},
		Checkpoint: "checkpoint-v1",
		Branch:     "wisp/feat-auth",
		SpriteName: "wisp-abc123",
		StartedAt:  startTime,
		Status:     config.SessionStatusRunning,
	}

	// Create session
	err := store.CreateSession(session)
	require.NoError(t, err)

	// Verify session file exists
	sessionPath := filepath.Join(tmpDir, ".wisp", "sessions", "wisp-feat-auth", "session.yaml")
	_, err = os.Stat(sessionPath)
	require.NoError(t, err)

	// Get session
	got, err := store.GetSession("wisp/feat-auth")
	require.NoError(t, err)
	assert.Equal(t, session.Repo, got.Repo)
	assert.Equal(t, session.Spec, got.Spec)
	assert.Equal(t, session.Siblings, got.Siblings)
	assert.Equal(t, session.Checkpoint, got.Checkpoint)
	assert.Equal(t, session.Branch, got.Branch)
	assert.Equal(t, session.SpriteName, got.SpriteName)
	assert.Equal(t, session.StartedAt, got.StartedAt)
	assert.Equal(t, session.Status, got.Status)
}

func TestStore_GetSession_NotFound(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)

	_, err := store.GetSession("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

func TestStore_ListSessions(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)

	// Create multiple sessions
	sessions := []*config.Session{
		{
			Repo:       "org/repo1",
			Spec:       "spec1.md",
			Branch:     "wisp/feat1",
			SpriteName: "wisp-111",
			StartedAt:  time.Now(),
			Status:     config.SessionStatusRunning,
		},
		{
			Repo:       "org/repo2",
			Spec:       "spec2.md",
			Branch:     "wisp/feat2",
			SpriteName: "wisp-222",
			StartedAt:  time.Now(),
			Status:     config.SessionStatusStopped,
		},
		{
			Repo:       "org/repo3",
			Spec:       "spec3.md",
			Branch:     "wisp/feat3",
			SpriteName: "wisp-333",
			StartedAt:  time.Now(),
			Status:     config.SessionStatusCompleted,
		},
	}

	for _, s := range sessions {
		require.NoError(t, store.CreateSession(s))
	}

	// List sessions
	got, err := store.ListSessions()
	require.NoError(t, err)
	assert.Len(t, got, 3)

	// Verify all sessions are present
	branches := make(map[string]bool)
	for _, s := range got {
		branches[s.Branch] = true
	}
	assert.True(t, branches["wisp/feat1"])
	assert.True(t, branches["wisp/feat2"])
	assert.True(t, branches["wisp/feat3"])
}

func TestStore_ListSessions_Empty(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)

	// List when no sessions exist
	sessions, err := store.ListSessions()
	require.NoError(t, err)
	assert.Empty(t, sessions)
}

func TestStore_UpdateSession(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)

	// Create initial session
	session := &config.Session{
		Repo:       "org/repo",
		Spec:       "spec.md",
		Branch:     "wisp/feature",
		SpriteName: "wisp-abc",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session))

	// Update session
	err := store.UpdateSession("wisp/feature", func(s *config.Session) {
		s.Status = config.SessionStatusStopped
	})
	require.NoError(t, err)

	// Verify update
	got, err := store.GetSession("wisp/feature")
	require.NoError(t, err)
	assert.Equal(t, config.SessionStatusStopped, got.Status)
	assert.Equal(t, "org/repo", got.Repo) // Other fields unchanged
}

func TestStore_UpdateSession_NotFound(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)

	err := store.UpdateSession("nonexistent", func(s *config.Session) {
		s.Status = config.SessionStatusStopped
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

func TestStore_SaveAndLoadState(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)

	// Create session first
	session := &config.Session{
		Repo:       "org/repo",
		Spec:       "spec.md",
		Branch:     "wisp/feature",
		SpriteName: "wisp-abc",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session))

	// Save state
	state := &State{
		Status:  StatusContinue,
		Summary: "Working on authentication",
		Verification: &Verification{
			Method:  VerificationTests,
			Passed:  true,
			Details: "All tests pass",
		},
	}
	err := store.SaveState("wisp/feature", state)
	require.NoError(t, err)

	// Load state
	got, err := store.LoadState("wisp/feature")
	require.NoError(t, err)
	assert.Equal(t, state.Status, got.Status)
	assert.Equal(t, state.Summary, got.Summary)
	require.NotNil(t, got.Verification)
	assert.Equal(t, state.Verification.Method, got.Verification.Method)
	assert.Equal(t, state.Verification.Passed, got.Verification.Passed)
	assert.Equal(t, state.Verification.Details, got.Verification.Details)
}

func TestStore_LoadState_NotFound(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)

	// Create session without state
	session := &config.Session{
		Repo:       "org/repo",
		Spec:       "spec.md",
		Branch:     "wisp/feature",
		SpriteName: "wisp-abc",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session))

	// Load state returns nil when not found
	state, err := store.LoadState("wisp/feature")
	require.NoError(t, err)
	assert.Nil(t, state)
}

func TestStore_SaveAndLoadTasks(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)

	// Create session first
	session := &config.Session{
		Repo:       "org/repo",
		Spec:       "spec.md",
		Branch:     "wisp/feature",
		SpriteName: "wisp-abc",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session))

	// Save tasks
	tasks := []Task{
		{
			Category:    CategorySetup,
			Description: "Initialize project",
			Steps:       []string{"Create go.mod", "Add dependencies"},
			Passes:      true,
		},
		{
			Category:    CategoryFeature,
			Description: "Implement authentication",
			Steps:       []string{"Add login endpoint", "Add session management"},
			Passes:      false,
		},
	}
	err := store.SaveTasks("wisp/feature", tasks)
	require.NoError(t, err)

	// Load tasks
	got, err := store.LoadTasks("wisp/feature")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, tasks[0].Category, got[0].Category)
	assert.Equal(t, tasks[0].Description, got[0].Description)
	assert.Equal(t, tasks[0].Steps, got[0].Steps)
	assert.Equal(t, tasks[0].Passes, got[0].Passes)
	assert.Equal(t, tasks[1].Category, got[1].Category)
	assert.Equal(t, tasks[1].Passes, got[1].Passes)
}

func TestStore_LoadTasks_NotFound(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)

	// Create session without tasks
	session := &config.Session{
		Repo:       "org/repo",
		Spec:       "spec.md",
		Branch:     "wisp/feature",
		SpriteName: "wisp-abc",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session))

	// Load tasks returns nil when not found
	tasks, err := store.LoadTasks("wisp/feature")
	require.NoError(t, err)
	assert.Nil(t, tasks)
}

func TestStore_SaveAndLoadHistory(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)

	// Create session first
	session := &config.Session{
		Repo:       "org/repo",
		Spec:       "spec.md",
		Branch:     "wisp/feature",
		SpriteName: "wisp-abc",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session))

	// Save history
	history := []History{
		{Iteration: 1, Summary: "Initial setup", TasksCompleted: 1, Status: StatusContinue},
		{Iteration: 2, Summary: "Added auth", TasksCompleted: 2, Status: StatusContinue},
	}
	err := store.SaveHistory("wisp/feature", history)
	require.NoError(t, err)

	// Load history
	got, err := store.LoadHistory("wisp/feature")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, history[0].Iteration, got[0].Iteration)
	assert.Equal(t, history[0].Summary, got[0].Summary)
	assert.Equal(t, history[0].TasksCompleted, got[0].TasksCompleted)
	assert.Equal(t, history[1].Iteration, got[1].Iteration)
}

func TestStore_LoadHistory_NotFound(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)

	// Create session without history
	session := &config.Session{
		Repo:       "org/repo",
		Spec:       "spec.md",
		Branch:     "wisp/feature",
		SpriteName: "wisp-abc",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session))

	// Load history returns nil when not found
	history, err := store.LoadHistory("wisp/feature")
	require.NoError(t, err)
	assert.Nil(t, history)
}

func TestStore_AppendHistory(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)

	// Create session first
	session := &config.Session{
		Repo:       "org/repo",
		Spec:       "spec.md",
		Branch:     "wisp/feature",
		SpriteName: "wisp-abc",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session))

	// Append history entries one by one
	err := store.AppendHistory("wisp/feature", History{
		Iteration: 1, Summary: "First", TasksCompleted: 1, Status: StatusContinue,
	})
	require.NoError(t, err)

	err = store.AppendHistory("wisp/feature", History{
		Iteration: 2, Summary: "Second", TasksCompleted: 2, Status: StatusContinue,
	})
	require.NoError(t, err)

	err = store.AppendHistory("wisp/feature", History{
		Iteration: 3, Summary: "Third", TasksCompleted: 3, Status: StatusDone,
	})
	require.NoError(t, err)

	// Load and verify
	history, err := store.LoadHistory("wisp/feature")
	require.NoError(t, err)
	require.Len(t, history, 3)
	assert.Equal(t, 1, history[0].Iteration)
	assert.Equal(t, 2, history[1].Iteration)
	assert.Equal(t, 3, history[2].Iteration)
	assert.Equal(t, StatusDone, history[2].Status)
}

func TestStore_DeleteSession(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)

	// Create session with state, tasks, history
	session := &config.Session{
		Repo:       "org/repo",
		Spec:       "spec.md",
		Branch:     "wisp/feature",
		SpriteName: "wisp-abc",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session))
	require.NoError(t, store.SaveState("wisp/feature", &State{Status: StatusContinue}))
	require.NoError(t, store.SaveTasks("wisp/feature", []Task{{Description: "test"}}))
	require.NoError(t, store.SaveHistory("wisp/feature", []History{{Iteration: 1}}))

	// Verify exists
	assert.True(t, store.SessionExists("wisp/feature"))

	// Delete
	err := store.DeleteSession("wisp/feature")
	require.NoError(t, err)

	// Verify deleted
	assert.False(t, store.SessionExists("wisp/feature"))

	// GetSession should fail
	_, err = store.GetSession("wisp/feature")
	require.Error(t, err)
}

func TestStore_SessionExists(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)

	// Session doesn't exist
	assert.False(t, store.SessionExists("wisp/feature"))

	// Create session
	session := &config.Session{
		Repo:       "org/repo",
		Spec:       "spec.md",
		Branch:     "wisp/feature",
		SpriteName: "wisp-abc",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session))

	// Session exists
	assert.True(t, store.SessionExists("wisp/feature"))
}

func TestStore_SaveState_CreatesDirectory(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)

	// SaveState should create directory if it doesn't exist
	err := store.SaveState("wisp/new-feature", &State{Status: StatusContinue})
	require.NoError(t, err)

	// Verify file exists
	statePath := filepath.Join(tmpDir, ".wisp", "sessions", "wisp-new-feature", "state.json")
	_, err = os.Stat(statePath)
	require.NoError(t, err)
}

func TestStore_SaveTasks_CreatesDirectory(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)

	// SaveTasks should create directory if it doesn't exist
	err := store.SaveTasks("wisp/new-feature", []Task{{Description: "test"}})
	require.NoError(t, err)

	// Verify file exists
	tasksPath := filepath.Join(tmpDir, ".wisp", "sessions", "wisp-new-feature", "tasks.json")
	_, err = os.Stat(tasksPath)
	require.NoError(t, err)
}

func TestStore_SaveHistory_CreatesDirectory(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)

	// SaveHistory should create directory if it doesn't exist
	err := store.SaveHistory("wisp/new-feature", []History{{Iteration: 1}})
	require.NoError(t, err)

	// Verify file exists
	historyPath := filepath.Join(tmpDir, ".wisp", "sessions", "wisp-new-feature", "history.json")
	_, err = os.Stat(historyPath)
	require.NoError(t, err)
}
