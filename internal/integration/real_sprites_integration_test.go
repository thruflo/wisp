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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/sprite"
	"github.com/thruflo/wisp/internal/state"
)

// realSpriteEnv holds credentials and client for real Sprite tests.
type realSpriteEnv struct {
	SpriteToken string
	GitHubToken string
	client      sprite.Client
}

// setupRealSpriteEnv loads credentials and creates a real Sprite client.
// Skips the test if SPRITE_TOKEN is not available.
func setupRealSpriteEnv(t *testing.T) *realSpriteEnv {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping real Sprite test in short mode")
	}

	env := &realSpriteEnv{}

	// Try loading from .wisp/.sprite.env at project root
	projectRoot := findProjectRoot(t)
	if projectRoot != "" {
		envVars, err := config.LoadEnvFile(projectRoot)
		if err == nil {
			env.SpriteToken = envVars["SPRITE_TOKEN"]
			env.GitHubToken = envVars["GITHUB_TOKEN"]
		}
	}

	// Fall back to environment variables (for CI/CD)
	if env.SpriteToken == "" {
		env.SpriteToken = os.Getenv("SPRITE_TOKEN")
	}
	if env.GitHubToken == "" {
		env.GitHubToken = os.Getenv("GITHUB_TOKEN")
	}

	// Skip if no token available
	if env.SpriteToken == "" {
		t.Skip("SPRITE_TOKEN not available, skipping real Sprite test")
	}

	// Create real client (never log the token)
	env.client = sprite.NewSDKClient(env.SpriteToken)

	return env
}

// findProjectRoot walks up from the current directory to find the .wisp directory.
func findProjectRoot(t *testing.T) string {
	t.Helper()

	// Start from the current working directory
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}

	// Walk up looking for .wisp directory
	for {
		wispDir := filepath.Join(dir, ".wisp")
		if info, err := os.Stat(wispDir); err == nil && info.IsDir() {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root
			return ""
		}
		dir = parent
	}
}

// generateTestSpriteName creates a unique Sprite name for testing.
// Includes timestamp to prevent collisions between test runs.
func generateTestSpriteName(t *testing.T, suffix string) string {
	t.Helper()
	timestamp := time.Now().UnixNano()
	// Keep name short but unique: wisp-test-<suffix>-<timestamp-last-8-digits>
	return fmt.Sprintf("wisp-test-%s-%d", suffix, timestamp%100000000)
}

// cleanupSprite safely deletes a Sprite, logging but not failing on errors.
func cleanupSprite(t *testing.T, client sprite.Client, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := client.Delete(ctx, name); err != nil {
		// Log but don't fail - the Sprite may already be deleted or never created
		t.Logf("cleanup: failed to delete Sprite %s: %v", name, err)
	} else {
		t.Logf("cleanup: deleted Sprite %s", name)
	}
}

// waitForSpriteReady polls until the sprite can execute commands.
// Returns error if sprite isn't ready within maxWait.
func waitForSpriteReady(t *testing.T, client sprite.Client, name string, maxWait time.Duration) error {
	t.Helper()

	deadline := time.Now().Add(maxWait)
	pollInterval := 1 * time.Second

	for time.Now().Before(deadline) {
		// Try to execute a simple command with a short timeout
		ready := make(chan bool, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd, err := client.Execute(ctx, name, "", nil, "echo", "ready")
			if err != nil {
				ready <- false
				return
			}
			// Read output in goroutine to avoid blocking
			go io.Copy(io.Discard, cmd.Stdout)
			go io.Copy(io.Discard, cmd.Stderr)
			if cmd.Wait() == nil {
				ready <- true
				return
			}
			ready <- false
		}()

		select {
		case isReady := <-ready:
			if isReady {
				t.Logf("sprite %s ready after %v", name, maxWait-time.Until(deadline))
				return nil
			}
		case <-time.After(6 * time.Second):
			// Timeout on this attempt, try again
		}
		time.Sleep(pollInterval)
	}

	return fmt.Errorf("sprite %s not ready after %v", name, maxWait)
}

// TestRealSprite_CreateAndDelete tests creating and deleting a real Sprite.
func TestRealSprite_CreateAndDelete(t *testing.T) {
	env := setupRealSpriteEnv(t)

	spriteName := generateTestSpriteName(t, "create")
	t.Logf("testing with Sprite: %s", spriteName)

	// Ensure cleanup happens even if test fails
	t.Cleanup(func() {
		cleanupSprite(t, env.client, spriteName)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Create the Sprite
	err := env.client.Create(ctx, spriteName, "")
	require.NoError(t, err, "failed to create Sprite")

	// Verify it exists
	exists, err := env.client.Exists(ctx, spriteName)
	require.NoError(t, err, "failed to check Sprite existence")
	assert.True(t, exists, "Sprite should exist after creation")

	// Delete the Sprite
	err = env.client.Delete(ctx, spriteName)
	require.NoError(t, err, "failed to delete Sprite")

	// Verify it's gone
	exists, err = env.client.Exists(ctx, spriteName)
	require.NoError(t, err, "failed to check Sprite existence after deletion")
	assert.False(t, exists, "Sprite should not exist after deletion")
}

// TestRealSprite_WriteReadFile tests writing and reading files on a real Sprite.
func TestRealSprite_WriteReadFile(t *testing.T) {
	env := setupRealSpriteEnv(t)

	spriteName := generateTestSpriteName(t, "file")
	t.Logf("testing with Sprite: %s", spriteName)

	t.Cleanup(func() {
		cleanupSprite(t, env.client, spriteName)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Create the Sprite
	err := env.client.Create(ctx, spriteName, "")
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
	err = env.client.WriteFile(ctx, spriteName, filePath, content)
	require.NoError(t, err, "failed to write file to Sprite")

	// Read file back
	readContent, err := env.client.ReadFile(ctx, spriteName, filePath)
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
// SKIP: The SDK's pipe-based Execute has issues where pipes don't close properly,
// causing io.ReadAll to block forever. The Filesystem API works correctly.
// TODO: Investigate SDK pipe handling or use cmd.Output() directly.
func TestRealSprite_ExecuteCommand(t *testing.T) {
	t.Skip("SDK pipe handling issue - pipes don't close, causing ReadAll to block")
}

// TestRealSprite_StateFileRoundTrip tests writing and reading state.json on a real Sprite.
func TestRealSprite_StateFileRoundTrip(t *testing.T) {
	env := setupRealSpriteEnv(t)

	spriteName := generateTestSpriteName(t, "state")
	t.Logf("testing with Sprite: %s", spriteName)

	t.Cleanup(func() {
		cleanupSprite(t, env.client, spriteName)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Create the Sprite
	err := env.client.Create(ctx, spriteName, "")
	require.NoError(t, err, "failed to create Sprite")

	// Create .wisp directory
	cmd, err := env.client.Execute(ctx, spriteName, "", nil, "mkdir", "-p", "/tmp/repo/.wisp")
	require.NoError(t, err)
	require.NoError(t, cmd.Wait())

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
	err = env.client.WriteFile(ctx, spriteName, statePath, stateData)
	require.NoError(t, err, "failed to write state.json")

	// Read state.json back
	readData, err := env.client.ReadFile(ctx, spriteName, statePath)
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
func TestRealSprite_SyncManagerIntegration(t *testing.T) {
	env := setupRealSpriteEnv(t)

	spriteName := generateTestSpriteName(t, "sync")
	t.Logf("testing with Sprite: %s", spriteName)

	t.Cleanup(func() {
		cleanupSprite(t, env.client, spriteName)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Create the Sprite
	err := env.client.Create(ctx, spriteName, "")
	require.NoError(t, err, "failed to create Sprite")

	// Create test directory structure on Sprite
	repoPath := "/tmp/test-repo"
	cmd, err := env.client.Execute(ctx, spriteName, "", nil, "mkdir", "-p", repoPath+"/.wisp")
	require.NoError(t, err)
	require.NoError(t, cmd.Wait())

	// Create local test environment
	tmpDir := t.TempDir()
	wispDir := filepath.Join(tmpDir, ".wisp", "sessions", "test-branch")
	require.NoError(t, os.MkdirAll(wispDir, 0755))

	// Create local store and SyncManager
	store := state.NewStore(tmpDir)
	syncMgr := state.NewSyncManager(env.client, store)

	branch := "test-branch"

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
	err = syncMgr.SyncToSprite(ctx, spriteName, branch, repoPath)
	require.NoError(t, err, "failed to sync to Sprite")

	// Verify files exist on Sprite by reading them directly
	stateData, err := env.client.ReadFile(ctx, spriteName, repoPath+"/.wisp/state.json")
	require.NoError(t, err, "state.json should exist on Sprite")
	assert.Contains(t, string(stateData), "NEEDS_INPUT")

	tasksData, err := env.client.ReadFile(ctx, spriteName, repoPath+"/.wisp/tasks.json")
	require.NoError(t, err, "tasks.json should exist on Sprite")
	assert.Contains(t, string(tasksData), "Implement API endpoints")

	historyData, err := env.client.ReadFile(ctx, spriteName, repoPath+"/.wisp/history.json")
	require.NoError(t, err, "history.json should exist on Sprite")
	assert.Contains(t, string(historyData), "Initial setup")

	// Modify state on Sprite (simulating Claude updating it)
	updatedState := &state.State{
		Status:  state.StatusContinue,
		Summary: "Proceeding with REST API",
	}
	updatedStateData, err := json.MarshalIndent(updatedState, "", "  ")
	require.NoError(t, err)
	err = env.client.WriteFile(ctx, spriteName, repoPath+"/.wisp/state.json", updatedStateData)
	require.NoError(t, err)

	// Clear local state to verify sync works
	newBranch := "test-branch-2"
	newWispDir := filepath.Join(tmpDir, ".wisp", "sessions", newBranch)
	require.NoError(t, os.MkdirAll(newWispDir, 0755))

	// Sync FROM Sprite to new branch
	err = syncMgr.SyncFromSprite(ctx, spriteName, newBranch, repoPath)
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
