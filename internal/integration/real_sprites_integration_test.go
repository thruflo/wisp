//go:build integration && real_sprites

// Package integration provides integration tests for wisp workflows.
//
// This file contains tests that run against real Sprite infrastructure.
// These tests require valid credentials and will create actual Sprites.
//
// To run:
//
//	go test -v -tags=integration,real_sprites ./internal/integration/...
//
// With safety timeout:
//
//	go test -tags=integration,real_sprites -timeout 10m ./internal/integration/...
//
// Requirements:
//   - SPRITE_TOKEN: Set in .wisp/.sprite.env or environment
//   - GITHUB_TOKEN: Optional, for repo cloning tests
package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thruflo/wisp/internal/sprite"
	"github.com/thruflo/wisp/internal/state"
	"github.com/thruflo/wisp/internal/testutil"
)

// TestRealSprite_CreateAndDelete tests creating and deleting a real Sprite.
func TestRealSprite_CreateAndDelete(t *testing.T) {
	env := testutil.SetupRealSpriteEnv(t)

	spriteName := testutil.GenerateTestSpriteName(t)
	t.Logf("testing with Sprite: %s", spriteName)

	// Ensure cleanup happens even if test fails
	t.Cleanup(func() {
		testutil.CleanupSprite(t, env.Client, spriteName)
	})

	ctx, cancel := testutil.SpriteOperationContext(t)
	defer cancel()

	// Create the Sprite
	err := env.Client.Create(ctx, spriteName, "")
	require.NoError(t, err, "failed to create Sprite")

	// Verify it exists
	exists, err := env.Client.Exists(ctx, spriteName)
	require.NoError(t, err, "failed to check Sprite existence")
	assert.True(t, exists, "Sprite should exist after creation")

	// Delete the Sprite
	err = env.Client.Delete(ctx, spriteName)
	require.NoError(t, err, "failed to delete Sprite")

	// Verify it's gone
	exists, err = env.Client.Exists(ctx, spriteName)
	require.NoError(t, err, "failed to check Sprite existence after deletion")
	assert.False(t, exists, "Sprite should not exist after deletion")
}

// TestRealSprite_WriteReadFile tests writing and reading files on a real Sprite.
func TestRealSprite_WriteReadFile(t *testing.T) {
	env := testutil.SetupRealSpriteEnv(t)

	spriteName := testutil.GenerateTestSpriteName(t)
	t.Logf("testing with Sprite: %s", spriteName)

	t.Cleanup(func() {
		testutil.CleanupSprite(t, env.Client, spriteName)
	})

	ctx, cancel := testutil.SpriteOperationContext(t)
	defer cancel()

	// Create the Sprite
	err := env.Client.Create(ctx, spriteName, "")
	require.NoError(t, err, "failed to create Sprite")

	// Test data
	testData := map[string]interface{}{
		"name":    "test-config",
		"version": 1,
		"enabled": true,
		"items":   []string{"a", "b", "c"},
	}
	content, err := json.MarshalIndent(testData, "", "  ")
	require.NoError(t, err)

	// Write file
	filePath := "/tmp/test-config.json"
	err = env.Client.WriteFile(ctx, spriteName, filePath, content)
	require.NoError(t, err, "failed to write file to Sprite")

	// Read file back
	readContent, err := env.Client.ReadFile(ctx, spriteName, filePath)
	require.NoError(t, err, "failed to read file from Sprite")

	// Verify content matches
	assert.JSONEq(t, string(content), string(readContent), "file content should match")

	// Unmarshal and verify structure
	var readData map[string]interface{}
	err = json.Unmarshal(readContent, &readData)
	require.NoError(t, err)
	assert.Equal(t, "test-config", readData["name"])
	assert.Equal(t, float64(1), readData["version"]) // JSON numbers are float64
	assert.Equal(t, true, readData["enabled"])
}

// TestRealSprite_ExecuteCommand tests executing commands on a real Sprite.
// Uses ExecuteOutput which avoids pipe handling issues with the SDK.
func TestRealSprite_ExecuteCommand(t *testing.T) {
	env := testutil.SetupRealSpriteEnv(t)

	spriteName := testutil.GenerateTestSpriteName(t)
	t.Logf("testing with Sprite: %s", spriteName)

	t.Cleanup(func() {
		testutil.CleanupSprite(t, env.Client, spriteName)
	})

	ctx, cancel := testutil.SpriteOperationContext(t)
	defer cancel()

	// Create the Sprite
	err := env.Client.Create(ctx, spriteName, "")
	require.NoError(t, err, "failed to create Sprite")

	// Test 1: Simple echo command using ExecuteOutput (non-streaming)
	stdout, stderr, exitCode, err := env.Client.ExecuteOutput(ctx, spriteName, "", nil, "echo", "hello world")
	require.NoError(t, err, "ExecuteOutput should not error")
	assert.Equal(t, 0, exitCode, "exit code should be 0")
	assert.Contains(t, string(stdout), "hello world", "stdout should contain expected output")
	assert.Empty(t, stderr, "stderr should be empty")

	// Test 2: Command with non-zero exit code
	stdout, stderr, exitCode, err = env.Client.ExecuteOutput(ctx, spriteName, "", nil, "bash", "-c", "exit 42")
	require.NoError(t, err, "ExecuteOutput should not error even with non-zero exit")
	assert.Equal(t, 42, exitCode, "exit code should be 42")

	// Test 3: Command with directory
	stdout, stderr, exitCode, err = env.Client.ExecuteOutput(ctx, spriteName, "/tmp", nil, "pwd")
	require.NoError(t, err, "ExecuteOutput with dir should not error")
	assert.Equal(t, 0, exitCode, "exit code should be 0")
	assert.Contains(t, string(stdout), "/tmp", "should show /tmp as current directory")

	// Test 4: Command with environment variables
	stdout, stderr, exitCode, err = env.Client.ExecuteOutput(ctx, spriteName, "", []string{"MY_VAR=test_value"}, "bash", "-c", "echo $MY_VAR")
	require.NoError(t, err, "ExecuteOutput with env should not error")
	assert.Equal(t, 0, exitCode, "exit code should be 0")
	assert.Contains(t, string(stdout), "test_value", "should show environment variable value")

	// Note: Streaming Execute with pipes has known SDK issues where pipes don't close properly.
	// Use ExecuteOutput for reliable command execution. The streaming Execute is still available
	// for cases like Claude output where long-running streaming is needed, but the caller must
	// handle the pipe lifecycle carefully.
}

// TestRealSprite_StateFileRoundTrip tests writing and reading state.json on a real Sprite.
func TestRealSprite_StateFileRoundTrip(t *testing.T) {
	env := testutil.SetupRealSpriteEnv(t)

	spriteName := testutil.GenerateTestSpriteName(t)
	t.Logf("testing with Sprite: %s", spriteName)

	t.Cleanup(func() {
		testutil.CleanupSprite(t, env.Client, spriteName)
	})

	ctx, cancel := testutil.SpriteOperationContext(t)
	defer cancel()

	// Create the Sprite
	err := env.Client.Create(ctx, spriteName, "")
	require.NoError(t, err, "failed to create Sprite")

	// Create .wisp directory
	_, _, exitCode, err := env.Client.ExecuteOutput(ctx, spriteName, "", nil, "mkdir", "-p", "/tmp/repo/.wisp")
	require.NoError(t, err)
	require.Equal(t, 0, exitCode, "mkdir should succeed")

	// Create test state
	testState := &state.State{
		Status:  state.StatusContinue,
		Summary: "Working on feature implementation",
		Verification: &state.Verification{
			Method:  state.VerificationTests,
			Passed:  true,
			Details: "All 42 tests passed",
		},
	}

	stateData, err := json.MarshalIndent(testState, "", "  ")
	require.NoError(t, err)

	// Write state.json
	statePath := "/tmp/repo/.wisp/state.json"
	err = env.Client.WriteFile(ctx, spriteName, statePath, stateData)
	require.NoError(t, err, "failed to write state.json")

	// Read state.json back
	readData, err := env.Client.ReadFile(ctx, spriteName, statePath)
	require.NoError(t, err, "failed to read state.json")

	// Unmarshal and verify
	var readState state.State
	err = json.Unmarshal(readData, &readState)
	require.NoError(t, err, "failed to unmarshal state")

	assert.Equal(t, state.StatusContinue, readState.Status)
	assert.Equal(t, "Working on feature implementation", readState.Summary)
	require.NotNil(t, readState.Verification)
	assert.Equal(t, state.VerificationTests, readState.Verification.Method)
	assert.True(t, readState.Verification.Passed)
	assert.Equal(t, "All 42 tests passed", readState.Verification.Details)
}

// TestRealSprite_SyncManagerIntegration tests the full SyncManager round-trip with a real Sprite.
// This test exercises the production SyncManager code paths for syncing state, tasks, and history
// between local storage and a Sprite.
func TestRealSprite_SyncManagerIntegration(t *testing.T) {
	env := testutil.SetupRealSpriteEnv(t)

	spriteName := testutil.GenerateTestSpriteName(t)
	t.Logf("testing with Sprite: %s", spriteName)

	t.Cleanup(func() {
		testutil.CleanupSprite(t, env.Client, spriteName)
	})

	ctx, cancel := testutil.SpriteOperationContext(t)
	defer cancel()

	// Create the Sprite
	err := env.Client.Create(ctx, spriteName, "")
	require.NoError(t, err, "failed to create Sprite")

	// Register with cleanup registry for global cleanup in case test cleanup fails
	testutil.RegisterSprite(spriteName)

	// Create local test environment
	tmpDir := t.TempDir()
	store := state.NewStore(tmpDir)
	syncMgr := state.NewSyncManager(env.Client, store)

	// Create directory structure on Sprite using production SyncManager method
	err = syncMgr.EnsureDirectoriesOnSprite(ctx, spriteName)
	require.NoError(t, err, "failed to create directories on Sprite")

	// Create local session directory for the branch
	branch := "test-branch"
	wispDir := filepath.Join(tmpDir, ".wisp", "sessions", branch)
	require.NoError(t, os.MkdirAll(wispDir, 0755))

	// Create and save local state
	localState := &state.State{
		Status:  state.StatusNeedsInput,
		Summary: "Need clarification on API design",
		Question: "Should we use REST or GraphQL?",
	}
	err = store.SaveState(branch, localState)
	require.NoError(t, err)

	// Create and save local tasks
	localTasks := []state.Task{
		{Category: state.CategorySetup, Description: "Set up project structure", Passes: true},
		{Category: state.CategoryFeature, Description: "Implement API endpoints", Passes: false},
		{Category: state.CategoryTest, Description: "Add API tests", Passes: false},
	}
	err = store.SaveTasks(branch, localTasks)
	require.NoError(t, err)

	// Create and save local history
	localHistory := []state.History{
		{Iteration: 1, Summary: "Initial setup", TasksCompleted: 1, Status: state.StatusContinue},
	}
	err = store.SaveHistory(branch, localHistory)
	require.NoError(t, err)

	// Sync TO Sprite
	err = syncMgr.SyncToSprite(ctx, spriteName, branch)
	require.NoError(t, err, "failed to sync to Sprite")

	// Verify files exist on Sprite by reading them directly
	stateData, err := env.Client.ReadFile(ctx, spriteName, sprite.SessionDir+"/state.json")
	require.NoError(t, err, "state.json should exist on Sprite")
	assert.Contains(t, string(stateData), "NEEDS_INPUT")

	tasksData, err := env.Client.ReadFile(ctx, spriteName, sprite.SessionDir+"/tasks.json")
	require.NoError(t, err, "tasks.json should exist on Sprite")
	assert.Contains(t, string(tasksData), "Implement API endpoints")

	historyData, err := env.Client.ReadFile(ctx, spriteName, sprite.SessionDir+"/history.json")
	require.NoError(t, err, "history.json should exist on Sprite")
	assert.Contains(t, string(historyData), "Initial setup")

	// Modify state on Sprite (simulating Claude updating it)
	updatedState := &state.State{
		Status:  state.StatusContinue,
		Summary: "Proceeding with REST API",
	}
	updatedStateData, err := json.MarshalIndent(updatedState, "", "  ")
	require.NoError(t, err)
	err = env.Client.WriteFile(ctx, spriteName, sprite.SessionDir+"/state.json", updatedStateData)
	require.NoError(t, err)

	// Clear local state to verify sync works
	newBranch := "test-branch-2"
	newWispDir := filepath.Join(tmpDir, ".wisp", "sessions", newBranch)
	require.NoError(t, os.MkdirAll(newWispDir, 0755))

	// Sync FROM Sprite to new branch
	err = syncMgr.SyncFromSprite(ctx, spriteName, newBranch)
	require.NoError(t, err, "failed to sync from Sprite")

	// Verify state was synced
	syncedState, err := store.LoadState(newBranch)
	require.NoError(t, err)
	assert.Equal(t, state.StatusContinue, syncedState.Status)
	assert.Equal(t, "Proceeding with REST API", syncedState.Summary)

	// Verify tasks were synced
	syncedTasks, err := store.LoadTasks(newBranch)
	require.NoError(t, err)
	assert.Len(t, syncedTasks, 3)
	assert.True(t, syncedTasks[0].Passes)
	assert.False(t, syncedTasks[1].Passes)

	// Verify history was synced
	syncedHistory, err := store.LoadHistory(newBranch)
	require.NoError(t, err)
	assert.Len(t, syncedHistory, 1)
	assert.Equal(t, "Initial setup", syncedHistory[0].Summary)
}
