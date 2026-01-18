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

func TestDoneCommand_AcceptsOptionalBranchArg(t *testing.T) {
	// The command should accept 0 or 1 argument
	assert.Equal(t, "done [branch]", doneCmd.Use)

	// Verify args validation allows 0 or 1 arg
	err := doneCmd.Args(doneCmd, []string{})
	assert.NoError(t, err)

	err = doneCmd.Args(doneCmd, []string{"wisp/my-feature"})
	assert.NoError(t, err)

	err = doneCmd.Args(doneCmd, []string{"branch1", "branch2"})
	assert.Error(t, err)
}

func TestDoneCommand_ShortDescription(t *testing.T) {
	assert.Equal(t, "Complete a wisp session and create a PR", doneCmd.Short)
}

func TestResolveBranch_ExplicitBranch(t *testing.T) {
	tmpDir := t.TempDir()
	store := state.NewStore(tmpDir)

	branch, err := resolveBranch(store, []string{"wisp/my-feature"})
	assert.NoError(t, err)
	assert.Equal(t, "wisp/my-feature", branch)
}

func TestResolveBranch_SingleSession(t *testing.T) {
	tmpDir := t.TempDir()

	// Create .wisp/sessions directory
	sessionsDir := filepath.Join(tmpDir, ".wisp", "sessions")
	require.NoError(t, os.MkdirAll(sessionsDir, 0o755))

	store := state.NewStore(tmpDir)

	// Create a single session
	session := &config.Session{
		Repo:       "test-org/test-repo",
		Spec:       "docs/rfc.md",
		Branch:     "wisp/only-session",
		SpriteName: "wisp-abc123",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session))

	branch, err := resolveBranch(store, []string{})
	assert.NoError(t, err)
	assert.Equal(t, "wisp/only-session", branch)
}

func TestResolveBranch_NoSessions(t *testing.T) {
	tmpDir := t.TempDir()
	store := state.NewStore(tmpDir)

	_, err := resolveBranch(store, []string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no sessions found")
}

func TestResolveBranch_MultipleSessions(t *testing.T) {
	tmpDir := t.TempDir()

	// Create .wisp/sessions directory
	sessionsDir := filepath.Join(tmpDir, ".wisp", "sessions")
	require.NoError(t, os.MkdirAll(sessionsDir, 0o755))

	store := state.NewStore(tmpDir)

	// Create multiple sessions
	session1 := &config.Session{
		Repo:       "test-org/test-repo",
		Spec:       "docs/rfc.md",
		Branch:     "wisp/feature-1",
		SpriteName: "wisp-abc123",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session1))

	session2 := &config.Session{
		Repo:       "test-org/test-repo",
		Spec:       "docs/rfc2.md",
		Branch:     "wisp/feature-2",
		SpriteName: "wisp-def456",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session2))

	_, err := resolveBranch(store, []string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "multiple sessions found")
}

func TestCountIncompleteTasks(t *testing.T) {
	tests := []struct {
		name     string
		tasks    []state.Task
		expected int
	}{
		{
			name: "all complete",
			tasks: []state.Task{
				{Description: "Task 1", Passes: true},
				{Description: "Task 2", Passes: true},
			},
			expected: 0,
		},
		{
			name: "some incomplete",
			tasks: []state.Task{
				{Description: "Task 1", Passes: true},
				{Description: "Task 2", Passes: false},
				{Description: "Task 3", Passes: true},
				{Description: "Task 4", Passes: false},
			},
			expected: 2,
		},
		{
			name: "all incomplete",
			tasks: []state.Task{
				{Description: "Task 1", Passes: false},
				{Description: "Task 2", Passes: false},
			},
			expected: 2,
		},
		{
			name:     "empty tasks",
			tasks:    []state.Task{},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := countIncompleteTasks(tt.tasks)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildFallbackPRTitle(t *testing.T) {
	tests := []struct {
		name     string
		tasks    []state.Task
		expected string
	}{
		{
			name: "uses first feature task",
			tasks: []state.Task{
				{Category: "setup", Description: "Initialize project"},
				{Category: "feature", Description: "Add user authentication"},
				{Category: "feature", Description: "Add user profile page"},
			},
			expected: "feat: add user authentication",
		},
		{
			name: "truncates long description",
			tasks: []state.Task{
				{Category: "feature", Description: "This is a very long description that exceeds the maximum allowed length for a PR title"},
			},
			expected: "feat: this is a very long description that exceeds the maximum allow...",
		},
		{
			name: "fallback for no features",
			tasks: []state.Task{
				{Category: "setup", Description: "Initialize project"},
				{Category: "test", Description: "Add unit tests"},
			},
			expected: "Implementation complete",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildFallbackPRTitle(tt.tasks)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildFallbackPRBody(t *testing.T) {
	tasks := []state.Task{
		{Category: "setup", Description: "Initialize project", Passes: true},
		{Category: "feature", Description: "Add authentication", Passes: true},
		{Category: "test", Description: "Add unit tests", Passes: true},
	}

	body := buildFallbackPRBody(tasks)

	// Verify body contains expected sections
	assert.Contains(t, body, "## Summary")
	assert.Contains(t, body, "- [x] Initialize project")
	assert.Contains(t, body, "- [x] Add authentication")
	assert.Contains(t, body, "- [x] Add unit tests")
	assert.Contains(t, body, "Generated with [wisp]")
}

func TestBuildCompareURL(t *testing.T) {
	tests := []struct {
		repo     string
		branch   string
		expected string
	}{
		{
			repo:     "electric-sql/electric",
			branch:   "wisp/add-auth",
			expected: "https://github.com/electric-sql/electric/compare/wisp%2Fadd-auth?expand=1",
		},
		{
			repo:     "org/repo",
			branch:   "feature-branch",
			expected: "https://github.com/org/repo/compare/feature-branch?expand=1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.repo+"/"+tt.branch, func(t *testing.T) {
			result := buildCompareURL(tt.repo, tt.branch)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDoneCommand_VerifiesAllTasksPass(t *testing.T) {
	tmpDir := t.TempDir()

	// Create .wisp/sessions directory
	sessionsDir := filepath.Join(tmpDir, ".wisp", "sessions")
	require.NoError(t, os.MkdirAll(sessionsDir, 0o755))

	store := state.NewStore(tmpDir)

	// Create a session
	session := &config.Session{
		Repo:       "test-org/test-repo",
		Spec:       "docs/rfc.md",
		Branch:     "wisp/incomplete",
		SpriteName: "wisp-incomplete",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session))

	// Create tasks with some incomplete
	tasks := []state.Task{
		{Category: "setup", Description: "Task 1", Passes: true},
		{Category: "feature", Description: "Task 2", Passes: false},
		{Category: "feature", Description: "Task 3", Passes: true},
	}
	require.NoError(t, store.SaveTasks("wisp/incomplete", tasks))

	// Load and verify
	loadedTasks, err := store.LoadTasks("wisp/incomplete")
	require.NoError(t, err)

	incomplete := countIncompleteTasks(loadedTasks)
	assert.Equal(t, 1, incomplete)
}

func TestDoneCommand_UpdatesSessionStatus(t *testing.T) {
	tmpDir := t.TempDir()

	// Create .wisp/sessions directory
	sessionsDir := filepath.Join(tmpDir, ".wisp", "sessions")
	require.NoError(t, os.MkdirAll(sessionsDir, 0o755))

	store := state.NewStore(tmpDir)

	// Create a running session
	session := &config.Session{
		Repo:       "test-org/test-repo",
		Spec:       "docs/rfc.md",
		Branch:     "wisp/done-test",
		SpriteName: "wisp-done123",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session))

	// Update status to completed (simulating what done command does)
	err := store.UpdateSession("wisp/done-test", func(s *config.Session) {
		s.Status = config.SessionStatusCompleted
	})
	require.NoError(t, err)

	// Verify updated status
	loaded, err := store.GetSession("wisp/done-test")
	require.NoError(t, err)
	assert.Equal(t, config.SessionStatusCompleted, loaded.Status)
}

func TestDoneCommand_PRModeValues(t *testing.T) {
	// Verify PRMode constants
	assert.Equal(t, PRMode(0), PRModeCancel)
	assert.Equal(t, PRMode(1), PRModeAuto)
	assert.Equal(t, PRMode(2), PRModeManual)
}

func TestDoneCommand_SessionDirAccess(t *testing.T) {
	tmpDir := t.TempDir()
	store := state.NewStore(tmpDir)

	// Verify SessionDir returns expected path
	dir := store.SessionDir("wisp/my-feature")
	expected := filepath.Join(tmpDir, ".wisp", "sessions", "wisp-my-feature")
	assert.Equal(t, expected, dir)
}

func TestDoneCommand_DivergenceFileSave(t *testing.T) {
	tmpDir := t.TempDir()

	// Create session directory
	sessionDir := filepath.Join(tmpDir, ".wisp", "sessions", "wisp-test")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))

	// Simulate writing divergence.md
	divergenceContent := "# Divergence Log\n\n- Changed API response format"
	divergencePath := filepath.Join(sessionDir, "divergence.md")
	require.NoError(t, os.WriteFile(divergencePath, []byte(divergenceContent), 0o644))

	// Verify it was written
	content, err := os.ReadFile(divergencePath)
	require.NoError(t, err)
	assert.Equal(t, divergenceContent, string(content))
}

func TestDoneCommand_FullIntegration(t *testing.T) {
	// Full integration test requires actual Sprite infrastructure
	t.Skip("Requires actual Sprite infrastructure for full integration test")
}
