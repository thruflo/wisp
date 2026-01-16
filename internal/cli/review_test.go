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

func TestReviewCommand_RequiresArgs(t *testing.T) {
	// The command should require exactly one argument (the feedback file path)
	assert.Equal(t, "review <feedback.md>", reviewCmd.Use)

	// Verify args validation is set to ExactArgs(1)
	err := reviewCmd.Args(reviewCmd, []string{})
	assert.Error(t, err)

	err = reviewCmd.Args(reviewCmd, []string{"feedback.md", "extra.md"})
	assert.Error(t, err)

	err = reviewCmd.Args(reviewCmd, []string{"feedback.md"})
	assert.NoError(t, err)
}

func TestReviewCommand_RequiresBranchFlag(t *testing.T) {
	// Verify the --branch flag is defined and required
	branchFlag := reviewCmd.Flags().Lookup("branch")
	require.NotNil(t, branchFlag, "branch flag should be defined")

	assert.Equal(t, "b", branchFlag.Shorthand)
	assert.Contains(t, branchFlag.Usage, "branch")
}

func TestReviewCommand_SessionNotFound(t *testing.T) {
	// Create a temp directory for the test
	tmpDir := t.TempDir()

	// Initialize store without creating any sessions
	store := state.NewStore(tmpDir)

	// Attempt to get a non-existent session
	_, err := store.GetSession("wisp/nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

func TestReviewCommand_SessionExists(t *testing.T) {
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

	// Verify we can load the session
	loaded, err := store.GetSession("wisp/test-feature")
	require.NoError(t, err)
	assert.Equal(t, session.Repo, loaded.Repo)
	assert.Equal(t, session.Spec, loaded.Spec)
	assert.Equal(t, session.Branch, loaded.Branch)
	assert.Equal(t, session.SpriteName, loaded.SpriteName)
}

func TestReviewCommand_FeedbackFileNotFound(t *testing.T) {
	// Create a temp directory for the test
	tmpDir := t.TempDir()

	// Try to read a non-existent feedback file
	feedbackPath := filepath.Join(tmpDir, "nonexistent.md")
	_, err := os.ReadFile(feedbackPath)
	assert.Error(t, err)
	assert.True(t, os.IsNotExist(err))
}

func TestReviewCommand_FeedbackFileRead(t *testing.T) {
	// Create a temp directory for the test
	tmpDir := t.TempDir()

	// Create a feedback file
	feedbackContent := `# PR Review Feedback

## Code Quality
- Fix the error handling in auth.go
- Add logging to the database calls

## Tests
- Add unit tests for the new helper functions

## Documentation
- Update README with new API endpoints
`
	feedbackPath := filepath.Join(tmpDir, "feedback.md")
	require.NoError(t, os.WriteFile(feedbackPath, []byte(feedbackContent), 0o644))

	// Verify we can read the feedback file
	content, err := os.ReadFile(feedbackPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "# PR Review Feedback")
	assert.Contains(t, string(content), "Fix the error handling")
	assert.Contains(t, string(content), "Add unit tests")
}

func TestReviewCommand_SessionStatusUpdate(t *testing.T) {
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
		Branch:     "wisp/review-test",
		SpriteName: "wisp-review123",
		StartedAt:  time.Now().Add(-1 * time.Hour),
		Status:     config.SessionStatusStopped,
	}

	// Create the session
	require.NoError(t, store.CreateSession(session))

	// Update session status to running (as review would do)
	err := store.UpdateSession("wisp/review-test", func(s *config.Session) {
		s.Status = config.SessionStatusRunning
	})
	require.NoError(t, err)

	// Verify the update
	loaded, err := store.GetSession("wisp/review-test")
	require.NoError(t, err)
	assert.Equal(t, config.SessionStatusRunning, loaded.Status)
}

func TestReviewCommand_TasksWithFeedback(t *testing.T) {
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
		Branch:     "wisp/feedback-test",
		SpriteName: "wisp-feedback123",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusStopped,
	}
	require.NoError(t, store.CreateSession(session))

	// Create existing tasks (as if some work was already done)
	existingTasks := []state.Task{
		{Category: "setup", Description: "Initialize project", Passes: true},
		{Category: "feature", Description: "Implement auth", Passes: true},
		{Category: "feature", Description: "Add API endpoints", Passes: true},
	}
	require.NoError(t, store.SaveTasks("wisp/feedback-test", existingTasks))

	// Verify tasks can be loaded (they would be synced to Sprite for review-tasks prompt)
	loadedTasks, err := store.LoadTasks("wisp/feedback-test")
	require.NoError(t, err)
	assert.Len(t, loadedTasks, 3)

	completedCount := 0
	for _, task := range loadedTasks {
		if task.Passes {
			completedCount++
		}
	}
	assert.Equal(t, 3, completedCount, "all existing tasks should be completed")
}

func TestReviewCommand_FeedbackPathVariants(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		valid    bool
	}{
		{
			name:  "simple filename",
			path:  "feedback.md",
			valid: true,
		},
		{
			name:  "relative path",
			path:  "docs/feedback.md",
			valid: true,
		},
		{
			name:  "pr comments file",
			path:  "pr-comments.md",
			valid: true,
		},
		{
			name:  "review file",
			path:  "code-review.txt",
			valid: true,
		},
	}

	tmpDir := t.TempDir()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create the file in the temp directory
			fullPath := filepath.Join(tmpDir, tt.path)
			require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o755))
			require.NoError(t, os.WriteFile(fullPath, []byte("test content"), 0o644))

			// Verify we can read it
			_, err := os.ReadFile(fullPath)
			if tt.valid {
				assert.NoError(t, err)
			}
		})
	}
}

func TestReviewCommand_SetupSequence(t *testing.T) {
	// This test documents the expected sequence of operations for the review command:
	// 1. Read feedback file from local path
	// 2. Load existing session
	// 3. Load config, settings, env
	// 4. Setup Sprite (reusing setupSpriteForResume)
	// 5. Sync local state to Sprite
	// 6. Copy feedback.md to Sprite .wisp/feedback.md
	// 7. Run review-tasks prompt with feedback
	// 8. Sync state from Sprite
	// 9. Continue iteration loop with TUI

	// Full integration test would require mock Sprite client
	t.Skip("Requires mock Sprite client infrastructure")
}

func TestReviewCommand_RunReviewTasksPromptIntegration(t *testing.T) {
	// This test would verify the review-tasks prompt execution
	// The prompt tells Claude to:
	// - Read feedback from .wisp/feedback.md
	// - Read current tasks.json
	// - Generate new tasks to address feedback
	// - Output updated tasks.json

	// Full integration test would require mock Sprite client
	t.Skip("Requires mock Sprite client infrastructure")
}

func TestReviewCommand_SyncManagerIntegration(t *testing.T) {
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
		Summary: "Testing review sync",
	}
	require.NoError(t, store.SaveState("wisp/sync-test", testState))

	// Verify SyncManager can be created with the store
	syncMgr := state.NewSyncManager(nil, store)
	assert.NotNil(t, syncMgr)

	// Verify state can be loaded for syncing
	loadedState, err := store.LoadState("wisp/sync-test")
	require.NoError(t, err)
	assert.Equal(t, state.StatusContinue, loadedState.Status)
}
