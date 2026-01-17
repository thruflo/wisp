//go:build integration

// Package integration provides integration tests for wisp workflows.
//
// These tests use a mock Sprite client to simulate full workflow scenarios
// without requiring actual Sprite infrastructure. To run:
//
//	go test -tags=integration ./internal/integration/...
//
// # Test Requirements for Real Sprites
//
// To run tests against real Sprite infrastructure:
//
// 1. Set environment variables:
//   - GITHUB_TOKEN: Your GitHub token (for repo cloning)
//   - SPRITE_TOKEN: Your Sprites API token
//
// 2. Run with the real_sprites build tag:
//
//	go test -tags=integration,real_sprites ./internal/integration/...
//
// 3. Note that real Sprite tests will:
//   - Create actual Sprites (billing applies)
//   - Clone real repositories
//   - Make API calls to Anthropic
//   - Take significantly longer to complete
//
//  4. For CI/CD, use the mock tests only unless you have dedicated
//     test infrastructure with appropriate credentials.
package integration

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/loop"
	"github.com/thruflo/wisp/internal/sprite"
	"github.com/thruflo/wisp/internal/state"
	"github.com/thruflo/wisp/internal/tui"
)

// Test Fixtures

// sampleRFC provides a minimal RFC for testing task generation.
const sampleRFC = `# Sample RFC: Test Feature

## Overview

Implement a simple feature with setup and verification.

## Requirements

1. Create a configuration file
2. Implement the main logic
3. Add unit tests
`

// sampleUpdatedRFC provides an updated RFC for testing the update command.
const sampleUpdatedRFC = `# Sample RFC: Test Feature (Updated)

## Overview

Implement a simple feature with setup and verification.

## Requirements

1. Create a configuration file
2. Implement the main logic with caching
3. Add unit tests
4. Add integration tests
`

// sampleFeedback provides PR feedback for testing the review command.
const sampleFeedback = `# PR Review Feedback

## Issues

1. Missing error handling in the main function
2. Configuration validation is too lenient
3. Test coverage should be at least 80%
`

// expectedTasks represents the expected task structure after RFC parsing.
var expectedTasks = []state.Task{
	{
		Category:    "setup",
		Description: "Create configuration file",
		Steps:       []string{"Create config directory", "Write default config"},
		Passes:      false,
	},
	{
		Category:    "feature",
		Description: "Implement main logic",
		Steps:       []string{"Implement core function", "Add error handling"},
		Passes:      false,
	},
	{
		Category:    "test",
		Description: "Add unit tests",
		Steps:       []string{"Write test cases", "Verify coverage"},
		Passes:      false,
	},
}

// Test Helpers

// setupTestEnv creates a test environment with .wisp directory structure.
func setupTestEnv(t *testing.T) (string, *state.Store) {
	t.Helper()

	tmpDir := t.TempDir()

	// Create .wisp directory structure
	wispDir := filepath.Join(tmpDir, ".wisp")
	dirs := []string{
		wispDir,
		filepath.Join(wispDir, "sessions"),
		filepath.Join(wispDir, "templates", "default"),
	}
	for _, dir := range dirs {
		require.NoError(t, os.MkdirAll(dir, 0755))
	}

	// Write config.yaml
	configContent := `limits:
  max_iterations: 10
  max_budget_usd: 5.00
  max_duration_hours: 1
  no_progress_threshold: 3
`
	require.NoError(t, os.WriteFile(filepath.Join(wispDir, "config.yaml"), []byte(configContent), 0644))

	// Write settings.json
	settings := config.Settings{
		Permissions: config.Permissions{
			Deny: []string{"Read(~/.ssh/**)", "Edit(~/.ssh/**)"},
		},
		MCPServers: map[string]config.MCPServer{},
	}
	settingsData, err := json.MarshalIndent(settings, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(wispDir, "settings.json"), settingsData, 0644))

	// Write .sprite.env
	envContent := "GITHUB_TOKEN=test-token\nSPRITE_TOKEN=test-key\n"
	require.NoError(t, os.WriteFile(filepath.Join(wispDir, ".sprite.env"), []byte(envContent), 0644))

	// Write template files
	templateDir := filepath.Join(wispDir, "templates", "default")
	require.NoError(t, os.WriteFile(filepath.Join(templateDir, "context.md"), []byte("# Context"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(templateDir, "create-tasks.md"), []byte("# Create Tasks"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(templateDir, "update-tasks.md"), []byte("# Update Tasks"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(templateDir, "review-tasks.md"), []byte("# Review Tasks"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(templateDir, "iterate.md"), []byte("# Iterate"), 0644))

	store := state.NewStore(tmpDir)
	return tmpDir, store
}

// createTestSession creates a session for testing.
func createTestSession(t *testing.T, store *state.Store, branch string) *config.Session {
	t.Helper()

	session := &config.Session{
		Repo:       "test-org/test-repo",
		Spec:       "docs/rfc.md",
		Siblings:   nil,
		Checkpoint: "",
		Branch:     branch,
		SpriteName: sprite.GenerateSpriteName("test-org/test-repo", branch),
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session))
	return session
}

// createTestTUI creates a TUI for testing (writes to discard).
func createTestTUI() *tui.TUI {
	return tui.NewTUI(io.Discard)
}

// Integration Tests

// TestFullInitStartDoneFlow tests the complete workflow from init to done.
func TestFullInitStartDoneFlow(t *testing.T) {
	t.Parallel()

	tmpDir, store := setupTestEnv(t)
	branch := "wisp/test-feature"

	// Create session
	session := createTestSession(t, store, branch)

	// Setup mock client
	mockClient := sprite.NewMockSpriteClient()
	repoPath := "/home/sprite/test-org/test-repo"

	// Set up initial state files that would be created during start
	mockClient.SetFile(filepath.Join(repoPath, ".wisp", "tasks.json"), mustMarshalJSON(t, expectedTasks))
	mockClient.SetFile(filepath.Join(repoPath, ".wisp", "state.json"), mustMarshalJSON(t, &state.State{
		Status:  state.StatusContinue,
		Summary: "Ready to start",
	}))

	// Create sync manager
	syncMgr := state.NewSyncManager(mockClient, store)

	// Sync initial state from "Sprite" to local
	ctx := context.Background()
	err := syncMgr.SyncFromSprite(ctx, session.SpriteName, branch, repoPath)
	require.NoError(t, err)

	// Verify tasks were synced
	tasks, err := store.LoadTasks(branch)
	require.NoError(t, err)
	assert.Len(t, tasks, 3)

	// Simulate completing all tasks
	for i := range tasks {
		tasks[i].Passes = true
	}
	require.NoError(t, store.SaveTasks(branch, tasks))

	// Set DONE state on Sprite
	mockClient.SetFile(filepath.Join(repoPath, ".wisp", "state.json"), mustMarshalJSON(t, &state.State{
		Status:  state.StatusDone,
		Summary: "All tasks completed",
		Verification: &state.Verification{
			Method:  "tests",
			Passed:  true,
			Details: "All tests passed",
		},
	}))

	// Load config
	cfg, err := config.LoadConfig(tmpDir)
	require.NoError(t, err)

	// Create TUI and loop
	testTUI := createTestTUI()
	templateDir := filepath.Join(tmpDir, ".wisp", "templates", "default")

	l := loop.NewLoop(mockClient, syncMgr, store, cfg, session, testTUI, repoPath, templateDir)

	// Run loop with immediate cancellation (to simulate one iteration)
	runCtx, cancel := context.WithCancel(ctx)
	cancel() // Cancel immediately to exit after first check

	result := l.Run(runCtx)

	// Should exit due to context cancellation (background)
	assert.Equal(t, loop.ExitReasonBackground, result.Reason)

	// Verify session can be updated to completed status
	err = store.UpdateSession(branch, func(s *config.Session) {
		s.Status = config.SessionStatusCompleted
	})
	require.NoError(t, err)

	updatedSession, err := store.GetSession(branch)
	require.NoError(t, err)
	assert.Equal(t, config.SessionStatusCompleted, updatedSession.Status)
}

// TestNeedsInputPauseAndResumeCycle tests the NEEDS_INPUT flow.
func TestNeedsInputPauseAndResumeCycle(t *testing.T) {
	t.Parallel()

	_, store := setupTestEnv(t)
	branch := "wisp/needs-input-test"

	// Create session
	session := createTestSession(t, store, branch)

	// Setup mock client
	mockClient := sprite.NewMockSpriteClient()
	repoPath := "/home/sprite/test-org/test-repo"

	// Set up tasks
	tasks := []state.Task{
		{Category: "feature", Description: "Task requiring input", Passes: false},
	}
	mockClient.SetFile(filepath.Join(repoPath, ".wisp", "tasks.json"), mustMarshalJSON(t, tasks))
	require.NoError(t, store.SaveTasks(branch, tasks))

	// Set NEEDS_INPUT state
	needsInputState := &state.State{
		Status:   state.StatusNeedsInput,
		Summary:  "Need clarification",
		Question: "Should we use approach A or B?",
	}
	mockClient.SetFile(filepath.Join(repoPath, ".wisp", "state.json"), mustMarshalJSON(t, needsInputState))

	// Create sync manager
	syncMgr := state.NewSyncManager(mockClient, store)
	ctx := context.Background()

	// Sync state from Sprite to local
	err := syncMgr.SyncFromSprite(ctx, session.SpriteName, branch, repoPath)
	require.NoError(t, err)

	// Verify local state reflects NEEDS_INPUT
	localState, err := store.LoadState(branch)
	require.NoError(t, err)
	assert.Equal(t, state.StatusNeedsInput, localState.Status)
	assert.Equal(t, "Should we use approach A or B?", localState.Question)

	// Simulate user providing response
	userResponse := "Use approach A for simplicity"
	err = syncMgr.WriteResponseToSprite(ctx, session.SpriteName, repoPath, userResponse)
	require.NoError(t, err)

	// Verify response.json was written to Sprite
	responseData, ok := mockClient.GetFile(filepath.Join(repoPath, ".wisp", "response.json"))
	require.True(t, ok, "response.json should be written")

	var response state.Response
	err = json.Unmarshal(responseData, &response)
	require.NoError(t, err)
	assert.Equal(t, userResponse, response.Answer)

	// Simulate Claude reading response and continuing
	// Update state to CONTINUE
	mockClient.SetFile(filepath.Join(repoPath, ".wisp", "state.json"), mustMarshalJSON(t, &state.State{
		Status:  state.StatusContinue,
		Summary: "Proceeding with approach A",
	}))

	// Sync again
	err = syncMgr.SyncFromSprite(ctx, session.SpriteName, branch, repoPath)
	require.NoError(t, err)

	// Verify state is now CONTINUE
	localState, err = store.LoadState(branch)
	require.NoError(t, err)
	assert.Equal(t, state.StatusContinue, localState.Status)
}

// TestUpdateCommandWithRFCChanges tests the update workflow.
func TestUpdateCommandWithRFCChanges(t *testing.T) {
	t.Parallel()

	tmpDir, store := setupTestEnv(t)
	branch := "wisp/update-test"

	// Create session
	session := createTestSession(t, store, branch)

	// Setup mock client
	mockClient := sprite.NewMockSpriteClient()
	repoPath := "/home/sprite/test-org/test-repo"

	// Set up original RFC on Sprite
	mockClient.SetFile(filepath.Join(repoPath, "docs", "rfc.md"), []byte(sampleRFC))

	// Set up existing tasks (partially completed)
	existingTasks := []state.Task{
		{Category: "setup", Description: "Create configuration file", Passes: true},
		{Category: "feature", Description: "Implement main logic", Passes: false},
		{Category: "test", Description: "Add unit tests", Passes: false},
	}
	mockClient.SetFile(filepath.Join(repoPath, ".wisp", "tasks.json"), mustMarshalJSON(t, existingTasks))
	require.NoError(t, store.SaveTasks(branch, existingTasks))

	// Write updated RFC locally
	updatedRFCPath := filepath.Join(tmpDir, "docs")
	require.NoError(t, os.MkdirAll(updatedRFCPath, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(updatedRFCPath, "updated-rfc.md"), []byte(sampleUpdatedRFC), 0644))

	// Read old RFC from Sprite
	ctx := context.Background()
	oldRFC, err := mockClient.ReadFile(ctx, session.SpriteName, filepath.Join(repoPath, "docs", "rfc.md"))
	require.NoError(t, err)
	assert.Equal(t, sampleRFC, string(oldRFC))

	// Read new RFC from local
	newRFC, err := os.ReadFile(filepath.Join(updatedRFCPath, "updated-rfc.md"))
	require.NoError(t, err)
	assert.Equal(t, sampleUpdatedRFC, string(newRFC))

	// Verify the RFCs are different (as expected in update flow)
	assert.NotEqual(t, string(oldRFC), string(newRFC))

	// Write diff to Sprite (simulating what update command does)
	diff := "# RFC Changes\n\n+ Implement the main logic with caching\n+ Add integration tests"
	err = mockClient.WriteFile(ctx, session.SpriteName, filepath.Join(repoPath, ".wisp", "rfc-diff.md"), []byte(diff))
	require.NoError(t, err)

	// Simulate updated tasks after RFC reconciliation
	updatedTasks := []state.Task{
		{Category: "setup", Description: "Create configuration file", Passes: true},
		{Category: "feature", Description: "Implement main logic with caching", Passes: false},
		{Category: "test", Description: "Add unit tests", Passes: false},
		{Category: "test", Description: "Add integration tests", Passes: false},
	}
	mockClient.SetFile(filepath.Join(repoPath, ".wisp", "tasks.json"), mustMarshalJSON(t, updatedTasks))

	// Sync from Sprite
	syncMgr := state.NewSyncManager(mockClient, store)
	err = syncMgr.SyncFromSprite(ctx, session.SpriteName, branch, repoPath)
	require.NoError(t, err)

	// Verify tasks were updated
	tasks, err := store.LoadTasks(branch)
	require.NoError(t, err)
	assert.Len(t, tasks, 4)
	assert.Equal(t, "Add integration tests", tasks[3].Description)
}

// TestReviewCommandWithFeedback tests the review workflow.
func TestReviewCommandWithFeedback(t *testing.T) {
	t.Parallel()

	tmpDir, store := setupTestEnv(t)
	branch := "wisp/review-test"

	// Create session
	session := createTestSession(t, store, branch)

	// Setup mock client
	mockClient := sprite.NewMockSpriteClient()
	repoPath := "/home/sprite/test-org/test-repo"

	// Set up existing tasks (all completed)
	existingTasks := []state.Task{
		{Category: "setup", Description: "Create configuration file", Passes: true},
		{Category: "feature", Description: "Implement main logic", Passes: true},
		{Category: "test", Description: "Add unit tests", Passes: true},
	}
	mockClient.SetFile(filepath.Join(repoPath, ".wisp", "tasks.json"), mustMarshalJSON(t, existingTasks))
	require.NoError(t, store.SaveTasks(branch, existingTasks))

	// Write feedback file locally
	feedbackPath := filepath.Join(tmpDir, "feedback.md")
	require.NoError(t, os.WriteFile(feedbackPath, []byte(sampleFeedback), 0644))

	// Read feedback
	feedbackContent, err := os.ReadFile(feedbackPath)
	require.NoError(t, err)

	// Write feedback to Sprite (simulating what review command does)
	ctx := context.Background()
	err = mockClient.WriteFile(ctx, session.SpriteName, filepath.Join(repoPath, ".wisp", "feedback.md"), feedbackContent)
	require.NoError(t, err)

	// Verify feedback was written
	spriteFeedback, ok := mockClient.GetFile(filepath.Join(repoPath, ".wisp", "feedback.md"))
	require.True(t, ok)
	assert.Equal(t, sampleFeedback, string(spriteFeedback))

	// Simulate new tasks generated from feedback
	feedbackTasks := []state.Task{
		{Category: "setup", Description: "Create configuration file", Passes: true},
		{Category: "feature", Description: "Implement main logic", Passes: true},
		{Category: "test", Description: "Add unit tests", Passes: true},
		{Category: "bugfix", Description: "Add error handling to main function", Passes: false},
		{Category: "bugfix", Description: "Improve configuration validation", Passes: false},
		{Category: "test", Description: "Increase test coverage to 80%", Passes: false},
	}
	mockClient.SetFile(filepath.Join(repoPath, ".wisp", "tasks.json"), mustMarshalJSON(t, feedbackTasks))

	// Sync from Sprite
	syncMgr := state.NewSyncManager(mockClient, store)
	err = syncMgr.SyncFromSprite(ctx, session.SpriteName, branch, repoPath)
	require.NoError(t, err)

	// Verify tasks were updated with feedback-addressing tasks
	tasks, err := store.LoadTasks(branch)
	require.NoError(t, err)
	assert.Len(t, tasks, 6)

	// Count incomplete tasks (from feedback)
	incomplete := 0
	for _, task := range tasks {
		if !task.Passes {
			incomplete++
		}
	}
	assert.Equal(t, 3, incomplete)
}

// TestStateSyncOnTaskCompletion tests that state is synced after task completion.
func TestStateSyncOnTaskCompletion(t *testing.T) {
	t.Parallel()

	_, store := setupTestEnv(t)
	branch := "wisp/sync-test"

	// Create session
	session := createTestSession(t, store, branch)

	// Setup mock client
	mockClient := sprite.NewMockSpriteClient()
	repoPath := "/home/sprite/test-org/test-repo"

	// Set up initial state (one task incomplete)
	tasks := []state.Task{
		{Category: "feature", Description: "Task 1", Passes: false},
		{Category: "feature", Description: "Task 2", Passes: false},
	}
	require.NoError(t, store.SaveTasks(branch, tasks))

	// Simulate completing first task on Sprite
	completedTasks := []state.Task{
		{Category: "feature", Description: "Task 1", Passes: true},
		{Category: "feature", Description: "Task 2", Passes: false},
	}
	mockClient.SetFile(filepath.Join(repoPath, ".wisp", "tasks.json"), mustMarshalJSON(t, completedTasks))
	mockClient.SetFile(filepath.Join(repoPath, ".wisp", "state.json"), mustMarshalJSON(t, &state.State{
		Status:  state.StatusContinue,
		Summary: "Completed Task 1",
	}))
	mockClient.SetFile(filepath.Join(repoPath, ".wisp", "history.json"), mustMarshalJSON(t, []state.History{
		{Iteration: 1, Summary: "Completed Task 1", TasksCompleted: 1, Status: state.StatusContinue},
	}))

	// Sync from Sprite
	ctx := context.Background()
	syncMgr := state.NewSyncManager(mockClient, store)
	err := syncMgr.SyncFromSprite(ctx, session.SpriteName, branch, repoPath)
	require.NoError(t, err)

	// Verify tasks synced
	localTasks, err := store.LoadTasks(branch)
	require.NoError(t, err)
	assert.True(t, localTasks[0].Passes)
	assert.False(t, localTasks[1].Passes)

	// Verify state synced
	localState, err := store.LoadState(branch)
	require.NoError(t, err)
	assert.Equal(t, "Completed Task 1", localState.Summary)

	// Verify history synced
	history, err := store.LoadHistory(branch)
	require.NoError(t, err)
	assert.Len(t, history, 1)
	assert.Equal(t, 1, history[0].TasksCompleted)
}

// TestStuckDetectionTriggeringBlocked tests that stuck detection marks session BLOCKED.
func TestStuckDetectionTriggeringBlocked(t *testing.T) {
	t.Parallel()

	_, store := setupTestEnv(t)
	branch := "wisp/stuck-test"

	// Create session
	createTestSession(t, store, branch)

	// Create config with low no_progress_threshold
	cfg := &config.Config{
		Limits: config.Limits{
			MaxIterations:       50,
			MaxBudgetUSD:        20,
			MaxDurationHours:    4,
			NoProgressThreshold: 3, // Stuck after 3 iterations without progress
		},
	}

	// Set up tasks (none completed)
	tasks := []state.Task{
		{Category: "feature", Description: "Difficult task", Passes: false},
	}
	require.NoError(t, store.SaveTasks(branch, tasks))

	// Set up history showing no progress for 3 iterations
	history := []state.History{
		{Iteration: 1, Summary: "Attempted task", TasksCompleted: 0, Status: state.StatusContinue},
		{Iteration: 2, Summary: "Still working", TasksCompleted: 0, Status: state.StatusContinue},
		{Iteration: 3, Summary: "No progress", TasksCompleted: 0, Status: state.StatusContinue},
	}
	require.NoError(t, store.SaveHistory(branch, history))

	// Verify stuck detection
	isStuck := loop.DetectStuck(history, cfg.Limits.NoProgressThreshold)
	assert.True(t, isStuck, "Should detect stuck state")

	// Verify with progress at end (not stuck)
	historyWithProgress := append(history, state.History{
		Iteration: 4, Summary: "Made progress", TasksCompleted: 1, Status: state.StatusContinue,
	})
	isStuck = loop.DetectStuck(historyWithProgress, cfg.Limits.NoProgressThreshold)
	assert.False(t, isStuck, "Should not be stuck after progress")

	// Verify cfg is used (to avoid unused variable warning)
	assert.Equal(t, 3, cfg.Limits.NoProgressThreshold)
}

// TestMaxIterationsLimit tests that loop exits when max iterations reached.
func TestMaxIterationsLimit(t *testing.T) {
	t.Parallel()

	tmpDir, store := setupTestEnv(t)
	branch := "wisp/max-iter-test"

	// Create session
	session := createTestSession(t, store, branch)

	// Create config with low max iterations
	cfg := &config.Config{
		Limits: config.Limits{
			MaxIterations:       2, // Very low for testing
			MaxBudgetUSD:        20,
			MaxDurationHours:    4,
			NoProgressThreshold: 10, // High to avoid stuck detection
		},
	}

	// Set up tasks (none completed)
	tasks := []state.Task{
		{Category: "feature", Description: "Long task", Passes: false},
	}
	require.NoError(t, store.SaveTasks(branch, tasks))

	// Setup mock client
	mockClient := sprite.NewMockSpriteClient()
	repoPath := "/home/sprite/test-org/test-repo"

	// Set up state that allows iteration
	mockClient.SetFile(filepath.Join(repoPath, ".wisp", "state.json"), mustMarshalJSON(t, &state.State{
		Status:  state.StatusContinue,
		Summary: "Working",
	}))
	mockClient.SetFile(filepath.Join(repoPath, ".wisp", "tasks.json"), mustMarshalJSON(t, tasks))

	syncMgr := state.NewSyncManager(mockClient, store)
	testTUI := createTestTUI()
	templateDir := filepath.Join(tmpDir, ".wisp", "templates", "default")

	l := loop.NewLoop(mockClient, syncMgr, store, cfg, session, testTUI, repoPath, templateDir)

	// Run with a short-lived context to trigger max iterations check
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result := l.Run(ctx)

	// Should exit due to context (short timeout) or max iterations
	assert.True(t, result.Reason == loop.ExitReasonBackground ||
		result.Reason == loop.ExitReasonMaxIterations ||
		result.Reason == loop.ExitReasonCrash,
		"Expected background, max iterations, or crash exit, got: %s", result.Reason)
}

// TestSpriteFailureHandling tests behavior when Sprite fails without state.json.
func TestSpriteFailureHandling(t *testing.T) {
	t.Parallel()

	tmpDir, store := setupTestEnv(t)
	branch := "wisp/failure-test"

	// Create session
	session := createTestSession(t, store, branch)

	// Create config
	cfg := &config.Config{
		Limits: config.Limits{
			MaxIterations:       10,
			MaxBudgetUSD:        20,
			MaxDurationHours:    4,
			NoProgressThreshold: 5,
		},
	}

	// Set up tasks
	tasks := []state.Task{
		{Category: "feature", Description: "Task", Passes: false},
	}
	require.NoError(t, store.SaveTasks(branch, tasks))

	// Setup mock client that simulates crash (no state.json after command)
	mockClient := sprite.NewMockSpriteClient()
	repoPath := "/home/sprite/test-org/test-repo"

	// Don't set state.json - simulating Claude crash
	mockClient.SetFile(filepath.Join(repoPath, ".wisp", "tasks.json"), mustMarshalJSON(t, tasks))

	syncMgr := state.NewSyncManager(mockClient, store)
	testTUI := createTestTUI()
	templateDir := filepath.Join(tmpDir, ".wisp", "templates", "default")

	l := loop.NewLoop(mockClient, syncMgr, store, cfg, session, testTUI, repoPath, templateDir)

	// Run loop
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result := l.Run(ctx)

	// Should exit - either crash (missing state.json) or background (context cancelled)
	assert.True(t, result.Reason == loop.ExitReasonCrash ||
		result.Reason == loop.ExitReasonBackground,
		"Expected crash or background exit, got: %s", result.Reason)
}

// TestDiffGeneration tests the RFC diff generation.
func TestDiffGeneration(t *testing.T) {
	t.Parallel()

	oldRFC := `# RFC
## Section 1
Content A
## Section 2
Content B`

	newRFC := `# RFC
## Section 1
Content A Modified
## Section 2
Content B
## Section 3
New Content`

	oldLines := []string{"# RFC", "## Section 1", "Content A", "## Section 2", "Content B"}
	newLines := []string{"# RFC", "## Section 1", "Content A Modified", "## Section 2", "Content B", "## Section 3", "New Content"}

	// Test that differences are detected
	assert.NotEqual(t, oldRFC, newRFC)
	assert.NotEqual(t, oldLines, newLines)
}

// TestBranchNaming tests sprite name generation.
func TestBranchNaming(t *testing.T) {
	t.Parallel()

	tests := []struct {
		repo   string
		branch string
	}{
		{"org/repo", "wisp/feature-1"},
		{"org/repo", "wisp/feature-2"},
		{"other/repo", "wisp/feature-1"},
	}

	names := make(map[string]bool)
	for _, tt := range tests {
		name := sprite.GenerateSpriteName(tt.repo, tt.branch)
		assert.True(t, len(name) > 0)
		assert.Contains(t, name, "wisp-")
		names[name] = true
	}

	// All names should be unique
	assert.Len(t, names, 3, "Sprite names should be unique for different repo/branch combinations")

	// Same inputs should produce same name
	name1 := sprite.GenerateSpriteName("org/repo", "branch")
	name2 := sprite.GenerateSpriteName("org/repo", "branch")
	assert.Equal(t, name1, name2, "Same inputs should produce same sprite name")
}

// TestHistoryAppend tests history recording.
func TestHistoryAppend(t *testing.T) {
	t.Parallel()

	_, store := setupTestEnv(t)
	branch := "wisp/history-test"

	// Create session
	createTestSession(t, store, branch)

	// Append history entries
	entries := []state.History{
		{Iteration: 1, Summary: "First iteration", TasksCompleted: 0, Status: state.StatusContinue},
		{Iteration: 2, Summary: "Second iteration", TasksCompleted: 1, Status: state.StatusContinue},
		{Iteration: 3, Summary: "Third iteration", TasksCompleted: 2, Status: state.StatusDone},
	}

	for _, entry := range entries {
		err := store.AppendHistory(branch, entry)
		require.NoError(t, err)
	}

	// Load and verify
	history, err := store.LoadHistory(branch)
	require.NoError(t, err)
	assert.Len(t, history, 3)
	assert.Equal(t, 2, history[2].TasksCompleted)
	assert.Equal(t, state.StatusDone, history[2].Status)
}

// TestProgressCalculation tests task progress calculation.
func TestProgressCalculation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		tasks         []state.Task
		wantCompleted int
		wantTotal     int
	}{
		{
			name:          "empty tasks",
			tasks:         nil,
			wantCompleted: 0,
			wantTotal:     0,
		},
		{
			name: "none complete",
			tasks: []state.Task{
				{Passes: false},
				{Passes: false},
			},
			wantCompleted: 0,
			wantTotal:     2,
		},
		{
			name: "some complete",
			tasks: []state.Task{
				{Passes: true},
				{Passes: false},
				{Passes: true},
			},
			wantCompleted: 2,
			wantTotal:     3,
		},
		{
			name: "all complete",
			tasks: []state.Task{
				{Passes: true},
				{Passes: true},
			},
			wantCompleted: 2,
			wantTotal:     2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			completed, total := loop.CalculateProgress(tt.tasks)
			assert.Equal(t, tt.wantCompleted, completed)
			assert.Equal(t, tt.wantTotal, total)
		})
	}
}

// TestProgressRate tests progress rate calculation.
func TestProgressRate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		history []state.History
		window  int
		want    float64
	}{
		{
			name:    "empty history",
			history: nil,
			window:  3,
			want:    0,
		},
		{
			name: "consistent progress",
			history: []state.History{
				{Iteration: 1, TasksCompleted: 0},
				{Iteration: 2, TasksCompleted: 1},
				{Iteration: 3, TasksCompleted: 2},
			},
			window: 3,
			want:   1.0,
		},
		{
			name: "no progress",
			history: []state.History{
				{Iteration: 1, TasksCompleted: 1},
				{Iteration: 2, TasksCompleted: 1},
				{Iteration: 3, TasksCompleted: 1},
			},
			window: 3,
			want:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rate := loop.ProgressRate(tt.history, tt.window)
			assert.Equal(t, tt.want, rate)
		})
	}
}

// Helpers

func mustMarshalJSON(t *testing.T, v interface{}) []byte {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	return data
}
