//go:build integration && real_sprites && real_claude

// Package integration provides integration tests for wisp workflows.
//
// This file contains tests that run against real Sprite infrastructure with real Claude.
// These tests require valid credentials and will create actual Sprites and call Claude API.
//
// To run:
//
//	go test -v -tags=integration,real_sprites,real_claude -timeout 10m ./internal/integration/...
//
// Requirements:
//   - SPRITE_TOKEN: Set in .wisp/.sprite.env or environment
//   - ANTHROPIC_API_KEY: Claude API key (or Claude Code installed on Sprite)
//
// WARNING: These tests cost money! Budget is limited per test but be aware of costs.
package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/loop"
	"github.com/thruflo/wisp/internal/sprite"
	"github.com/thruflo/wisp/internal/state"
	"github.com/thruflo/wisp/internal/testutil"
)

// copyClaudeCredentialsToSprite copies local Claude credentials to the Sprite for Claude Max auth.
// Uses the shared sprite.CopyClaudeCredentials function.
func copyClaudeCredentialsToSprite(t *testing.T, client sprite.Client, spriteName string) {
	t.Helper()
	ctx, cancel := testutil.ShortOperationContext(t)
	defer cancel()

	err := sprite.CopyClaudeCredentials(ctx, client, spriteName)
	require.NoError(t, err, "failed to copy Claude credentials")
	t.Logf("Copied Claude credentials to Sprite %s", spriteName)
}

// setupSpriteWithTemplates creates a Sprite and copies wisp templates to it.
func setupSpriteWithTemplates(t *testing.T, client sprite.Client, spriteName, repoPath string) {
	t.Helper()
	ctx, cancel := testutil.SpriteOperationContext(t)
	defer cancel()

	// Create .wisp directory
	_, _, exitCode, err := client.ExecuteOutput(ctx, spriteName, "", nil, "mkdir", "-p", filepath.Join(repoPath, ".wisp"))
	require.NoError(t, err, "failed to create .wisp directory")
	require.Equal(t, 0, exitCode, "mkdir should succeed")

	// Initialize git repo if not already
	_, _, exitCode, err = client.ExecuteOutput(ctx, spriteName, repoPath, nil, "git", "init")
	require.NoError(t, err, "failed to init git")
	require.Equal(t, 0, exitCode, "git init should succeed")

	// Configure git user for commits
	_, _, exitCode, err = client.ExecuteOutput(ctx, spriteName, repoPath, nil, "git", "config", "user.email", "test@wisp.dev")
	require.NoError(t, err, "failed to configure git email")
	_, _, exitCode, err = client.ExecuteOutput(ctx, spriteName, repoPath, nil, "git", "config", "user.name", "Wisp Test")
	require.NoError(t, err, "failed to configure git name")

	// Copy Claude credentials for authentication
	copyClaudeCredentialsToSprite(t, client, spriteName)

	// Find and copy templates
	projectRoot := testutil.FindProjectRoot(t)
	require.NotEmpty(t, projectRoot, "could not find project root")

	templateDir := filepath.Join(projectRoot, ".wisp", "templates", "default")
	entries, err := os.ReadDir(templateDir)
	require.NoError(t, err, "failed to read templates directory")

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		localPath := filepath.Join(templateDir, entry.Name())
		remotePath := filepath.Join(repoPath, ".wisp", entry.Name())

		content, err := os.ReadFile(localPath)
		require.NoError(t, err, "failed to read template %s", entry.Name())

		err = client.WriteFile(ctx, spriteName, remotePath, content)
		require.NoError(t, err, "failed to write template %s to sprite", entry.Name())
	}
}

// writeTasksToSprite writes tasks.json to the Sprite.
func writeTasksToSprite(t *testing.T, client sprite.Client, spriteName, repoPath string, tasks []state.Task) {
	t.Helper()
	ctx, cancel := testutil.ShortOperationContext(t)
	defer cancel()

	data, err := json.MarshalIndent(tasks, "", "  ")
	require.NoError(t, err, "failed to marshal tasks")

	err = client.WriteFile(ctx, spriteName, filepath.Join(repoPath, ".wisp", "tasks.json"), data)
	require.NoError(t, err, "failed to write tasks.json to sprite")
}

// writeStateToSprite writes state.json to the Sprite.
func writeStateToSprite(t *testing.T, client sprite.Client, spriteName, repoPath string, st *state.State) {
	t.Helper()
	ctx, cancel := testutil.ShortOperationContext(t)
	defer cancel()

	data, err := json.MarshalIndent(st, "", "  ")
	require.NoError(t, err, "failed to marshal state")

	err = client.WriteFile(ctx, spriteName, filepath.Join(repoPath, ".wisp", "state.json"), data)
	require.NoError(t, err, "failed to write state.json to sprite")
}

// readStateFromSprite reads state.json from the Sprite.
func readStateFromSprite(t *testing.T, client sprite.Client, spriteName, repoPath string) *state.State {
	t.Helper()
	ctx, cancel := testutil.ShortOperationContext(t)
	defer cancel()

	data, err := client.ReadFile(ctx, spriteName, filepath.Join(repoPath, ".wisp", "state.json"))
	require.NoError(t, err, "failed to read state.json from sprite")

	var st state.State
	err = json.Unmarshal(data, &st)
	require.NoError(t, err, "failed to unmarshal state")

	return &st
}

// readTasksFromSprite reads tasks.json from the Sprite.
func readTasksFromSprite(t *testing.T, client sprite.Client, spriteName, repoPath string) []state.Task {
	t.Helper()
	ctx, cancel := testutil.ShortOperationContext(t)
	defer cancel()

	data, err := client.ReadFile(ctx, spriteName, filepath.Join(repoPath, ".wisp", "tasks.json"))
	require.NoError(t, err, "failed to read tasks.json from sprite")

	var tasks []state.Task
	err = json.Unmarshal(data, &tasks)
	require.NoError(t, err, "failed to unmarshal tasks")

	return tasks
}

// runClaudeIteration executes one Claude iteration and streams output to test log.
func runClaudeIteration(t *testing.T, client sprite.Client, spriteName, repoPath string, budget float64, timeout time.Duration) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Build Claude command
	iteratePath := filepath.Join(repoPath, ".wisp", "iterate.md")
	contextPath := filepath.Join(repoPath, ".wisp", "context.md")

	// Use bash -c to expand the $(cat ...) properly
	// Note: --verbose is required when using -p with --output-format stream-json
	// Note: 20 turns allows enough room for reading context, doing work, and updating state files
	cmdStr := fmt.Sprintf(
		`claude -p "$(cat %s)" --append-system-prompt-file %s --dangerously-skip-permissions --verbose --output-format stream-json --max-turns 20 --max-budget-usd %.2f`,
		iteratePath, contextPath, budget,
	)

	cmd, err := client.Execute(ctx, spriteName, repoPath, nil, "bash", "-c", cmdStr)
	if err != nil {
		return fmt.Errorf("failed to start Claude: %w", err)
	}

	// Stream output to test log
	go func() {
		scanner := bufio.NewScanner(cmd.Stdout)
		buf := make([]byte, 64*1024)
		scanner.Buffer(buf, 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if line != "" {
				// Parse and log meaningful content
				t.Logf("[claude] %s", truncateLine(line, 200))
			}
		}
	}()

	go func() {
		scanner := bufio.NewScanner(cmd.Stderr)
		for scanner.Scan() {
			line := scanner.Text()
			if line != "" {
				t.Logf("[claude:err] %s", line)
			}
		}
	}()

	// Wait for completion
	if err := cmd.Wait(); err != nil {
		// Non-zero exit may be okay if state.json was written
		t.Logf("Claude exited with error (may be okay): %v, exit code: %d", err, cmd.ExitCode())
	}

	return nil
}

// truncateLine truncates a line to maxLen characters.
func truncateLine(line string, maxLen int) string {
	if len(line) > maxLen {
		return line[:maxLen] + "..."
	}
	return line
}

// TestRealSprite_SingleIterationWithClaude verifies one iteration of the ralph loop works with real Claude.
// This test uses the production Loop with NewLoopWithOptions to ensure tests exercise
// the same code paths as production.
func TestRealSprite_SingleIterationWithClaude(t *testing.T) {
	env := testutil.SetupRealSpriteEnv(t)

	spriteName := testutil.GenerateTestSpriteName(t)
	t.Logf("testing with Sprite: %s", spriteName)

	// Register sprite for cleanup
	testutil.RegisterSprite(spriteName)
	t.Cleanup(func() {
		testutil.CleanupSprite(t, env.Client, spriteName)
	})

	ctx, cancel := testutil.ClaudeExecutionContext(t)
	defer cancel()

	// Create the Sprite
	err := env.Client.Create(ctx, spriteName, "")
	require.NoError(t, err, "failed to create Sprite")

	// Setup repo path
	repoPath := "/home/sprite/test-org/test-repo"
	_, _, exitCode, err := env.Client.ExecuteOutput(ctx, spriteName, "", nil, "mkdir", "-p", repoPath)
	require.NoError(t, err, "failed to create repo directory")
	require.Equal(t, 0, exitCode, "mkdir should succeed")

	// Setup templates
	setupSpriteWithTemplates(t, env.Client, spriteName, repoPath)

	// Write pre-defined task
	tasks := []state.Task{
		{
			Category:    state.CategorySetup,
			Description: "Create a file called hello.txt containing 'Hello, World!'",
			Steps:       []string{"Create the file hello.txt", "Write the content 'Hello, World!'"},
			Passes:      false,
		},
	}
	writeTasksToSprite(t, env.Client, spriteName, repoPath, tasks)

	// Write initial state
	initialState := &state.State{
		Status:  state.StatusContinue,
		Summary: "Starting work",
	}
	writeStateToSprite(t, env.Client, spriteName, repoPath, initialState)

	// Create local test environment
	tmpDir := t.TempDir()
	wispDir := filepath.Join(tmpDir, ".wisp", "sessions", "test-branch")
	require.NoError(t, os.MkdirAll(wispDir, 0755))

	// Create local store and SyncManager
	store := state.NewStore(tmpDir)
	syncMgr := state.NewSyncManager(env.Client, store)

	// Save initial state locally
	require.NoError(t, store.SaveTasks("test-branch", tasks))
	require.NoError(t, store.SaveState("test-branch", initialState))

	// Create config with max 1 iteration to run only once
	cfg := &config.Config{
		Limits: config.Limits{
			MaxIterations:       1, // Only run one iteration
			MaxBudgetUSD:        0.50,
			MaxDurationHours:    1,
			NoProgressThreshold: 3,
		},
	}

	// Create session
	session := &config.Session{
		Repo:       "test-org/test-repo",
		Branch:     "test-branch",
		SpriteName: spriteName,
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session))

	// Create TUI that writes to discard (no terminal for tests)
	testTUI := createTestTUI()

	// Find template dir
	projectRoot := testutil.FindProjectRoot(t)
	require.NotEmpty(t, projectRoot, "could not find project root")
	templateDir := filepath.Join(projectRoot, ".wisp", "templates", "default")

	// Create ClaudeConfig with test-appropriate settings
	// Use enough turns for the task but not excessive
	claudeCfg := loop.ClaudeConfig{
		MaxTurns:     20, // Enough turns to complete a simple task
		MaxBudget:    0.50,
		Verbose:      true,
		OutputFormat: "stream-json",
	}

	// Create Loop using NewLoopWithOptions with production-like config
	l := loop.NewLoopWithOptions(loop.LoopOptions{
		Client:       env.Client,
		SyncManager:  syncMgr,
		Store:        store,
		Config:       cfg,
		Session:      session,
		TUI:          testTUI,
		RepoPath:     repoPath,
		TemplateDir:  templateDir,
		ClaudeConfig: claudeCfg,
	})

	// Run loop (will execute exactly 1 iteration due to MaxIterations=1)
	t.Log("Running loop with production code path...")
	result := l.Run(ctx)

	t.Logf("Loop result: reason=%s, iterations=%d", result.Reason, result.Iterations)
	if result.Error != nil {
		t.Logf("Loop error: %v", result.Error)
	}

	// The loop should exit with MaxIterations (since we only run 1 iteration)
	// or Done if the task completed in that single iteration
	assert.Contains(t, []loop.ExitReason{loop.ExitReasonMaxIterations, loop.ExitReasonDone}, result.Reason,
		"loop should exit with MaxIterations or Done")
	assert.Equal(t, 1, result.Iterations, "should have run exactly 1 iteration")

	// Verify state.json via local store (synced from Sprite)
	finalState, err := store.LoadState("test-branch")
	require.NoError(t, err, "should be able to load final state")
	t.Logf("Final state: status=%s, summary=%s", finalState.Status, finalState.Summary)
	assert.Contains(t, []string{state.StatusContinue, state.StatusDone}, finalState.Status,
		"status should be CONTINUE or DONE")

	// Verify tasks.json via local store
	finalTasks, err := store.LoadTasks("test-branch")
	require.NoError(t, err, "should be able to load final tasks")
	require.Len(t, finalTasks, 1, "should have 1 task")
	assert.True(t, finalTasks[0].Passes, "task should be marked as passes: true")

	// Verify hello.txt exists with correct content on Sprite
	helloContent, err := env.Client.ReadFile(ctx, spriteName, filepath.Join(repoPath, "hello.txt"))
	require.NoError(t, err, "hello.txt should exist")
	assert.Contains(t, string(helloContent), "Hello, World!", "hello.txt should contain expected content")

	// Verify a git commit was made
	stdout, _, exitCode, err := env.Client.ExecuteOutput(ctx, spriteName, repoPath, nil, "git", "log", "--oneline", "-1")
	require.NoError(t, err, "git log should work")
	assert.Equal(t, 0, exitCode, "git log should succeed")
	assert.NotEmpty(t, stdout, "should have at least one commit")
	t.Logf("Latest commit: %s", strings.TrimSpace(string(stdout)))

	// Verify history was recorded
	history, err := store.LoadHistory("test-branch")
	require.NoError(t, err, "should be able to load history")
	assert.Len(t, history, 1, "should have 1 history entry for 1 iteration")
}

// TestRealSprite_FullLoopCompletion verifies the full loop runs to completion with ExitReasonDone.
// This test uses cli.SetupSprite patterns and production Loop with real Claude via NewLoopWithOptions
// to ensure tests exercise the same code paths as production.
func TestRealSprite_FullLoopCompletion(t *testing.T) {
	env := testutil.SetupRealSpriteEnv(t)

	spriteName := testutil.GenerateTestSpriteName(t)
	t.Logf("testing with Sprite: %s", spriteName)

	// Register sprite for cleanup (production pattern)
	testutil.RegisterSprite(spriteName)
	t.Cleanup(func() {
		testutil.CleanupSprite(t, env.Client, spriteName)
	})

	ctx, cancel := testutil.ClaudeExecutionContext(t)
	defer cancel()

	// Create the Sprite
	err := env.Client.Create(ctx, spriteName, "")
	require.NoError(t, err, "failed to create Sprite")

	// Setup repo path
	repoPath := "/home/sprite/test-org/test-repo"
	_, _, exitCode, err := env.Client.ExecuteOutput(ctx, spriteName, "", nil, "mkdir", "-p", repoPath)
	require.NoError(t, err, "failed to create repo directory")
	require.Equal(t, 0, exitCode, "mkdir should succeed")

	// Setup templates
	setupSpriteWithTemplates(t, env.Client, spriteName, repoPath)

	// Write tasks - two simple tasks
	tasks := []state.Task{
		{
			Category:    state.CategorySetup,
			Description: "Create src directory",
			Steps:       []string{"Create directory src/"},
			Passes:      false,
		},
		{
			Category:    state.CategoryFeature,
			Description: "Create main.go with hello world",
			Steps:       []string{"Create src/main.go", "Add main function that prints Hello"},
			Passes:      false,
		},
	}
	writeTasksToSprite(t, env.Client, spriteName, repoPath, tasks)

	// Write initial state
	initialState := &state.State{
		Status:  state.StatusContinue,
		Summary: "Starting work",
	}
	writeStateToSprite(t, env.Client, spriteName, repoPath, initialState)

	// Create local test environment
	tmpDir := t.TempDir()
	wispDir := filepath.Join(tmpDir, ".wisp", "sessions", "test-branch")
	require.NoError(t, os.MkdirAll(wispDir, 0755))

	// Create local store and SyncManager
	store := state.NewStore(tmpDir)
	syncMgr := state.NewSyncManager(env.Client, store)

	// Save initial state locally
	require.NoError(t, store.SaveTasks("test-branch", tasks))
	require.NoError(t, store.SaveState("test-branch", initialState))

	// Create config with max iterations to allow loop to complete
	cfg := &config.Config{
		Limits: config.Limits{
			MaxIterations:       5,
			MaxBudgetUSD:        2.00,
			MaxDurationHours:    1,
			NoProgressThreshold: 3,
		},
	}

	// Create session
	session := &config.Session{
		Repo:       "test-org/test-repo",
		Branch:     "test-branch",
		SpriteName: spriteName,
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session))

	// Create TUI that writes to discard (no terminal for tests)
	testTUI := createTestTUI()

	// Find template dir
	projectRoot := testutil.FindProjectRoot(t)
	require.NotEmpty(t, projectRoot, "could not find project root")
	templateDir := filepath.Join(projectRoot, ".wisp", "templates", "default")

	// Create ClaudeConfig with test-appropriate settings
	// Use enough turns for the tasks but with budget control
	claudeCfg := loop.ClaudeConfig{
		MaxTurns:     30, // Enough turns to complete two simple tasks
		MaxBudget:    2.00,
		Verbose:      true,
		OutputFormat: "stream-json",
	}

	// Create Loop using NewLoopWithOptions with production-like config
	l := loop.NewLoopWithOptions(loop.LoopOptions{
		Client:       env.Client,
		SyncManager:  syncMgr,
		Store:        store,
		Config:       cfg,
		Session:      session,
		TUI:          testTUI,
		RepoPath:     repoPath,
		TemplateDir:  templateDir,
		ClaudeConfig: claudeCfg,
	})

	t.Log("Running loop with production code path...")
	result := l.Run(ctx)

	t.Logf("Loop result: reason=%s, iterations=%d", result.Reason, result.Iterations)
	if result.Error != nil {
		t.Logf("Loop error: %v", result.Error)
	}

	// Verify result
	assert.Equal(t, loop.ExitReasonDone, result.Reason, "loop should exit with ExitReasonDone")

	// ============================================================
	// Verify production state sync works end-to-end
	// All state files should be synced from Sprite to local store
	// ============================================================

	// Verify state.json synced correctly
	finalState, err := store.LoadState("test-branch")
	require.NoError(t, err, "should be able to load final state from local store")
	assert.Equal(t, state.StatusDone, finalState.Status, "final status should be DONE")
	assert.NotEmpty(t, finalState.Summary, "final state should have a summary")
	t.Logf("Final state: status=%s, summary=%s", finalState.Status, finalState.Summary)

	// Verify state.json also exists on Sprite (production sync bi-directional)
	spriteState := readStateFromSprite(t, env.Client, spriteName, repoPath)
	assert.Equal(t, state.StatusDone, spriteState.Status, "sprite state should also be DONE")

	// Verify tasks.json synced correctly - all tasks should pass
	finalTasks, err := store.LoadTasks("test-branch")
	require.NoError(t, err, "should be able to load final tasks from local store")
	require.Len(t, finalTasks, 2, "should have 2 tasks")
	for i, task := range finalTasks {
		assert.True(t, task.Passes, "task %d should pass: %s", i, task.Description)
	}
	t.Logf("Tasks: %d total, all passing", len(finalTasks))

	// Verify tasks.json also matches on Sprite
	spriteTasks := readTasksFromSprite(t, env.Client, spriteName, repoPath)
	require.Len(t, spriteTasks, 2, "sprite should have 2 tasks")
	for i, task := range spriteTasks {
		assert.True(t, task.Passes, "sprite task %d should pass: %s", i, task.Description)
	}

	// ============================================================
	// Verify production history recording works
	// ============================================================

	history, err := store.LoadHistory("test-branch")
	require.NoError(t, err, "should be able to load history from local store")
	assert.NotEmpty(t, history, "should have history entries")
	assert.GreaterOrEqual(t, len(history), 1, "should have at least 1 history entry")

	// Verify history entries have required fields
	t.Logf("History entries: %d", len(history))
	for _, h := range history {
		assert.Greater(t, h.Iteration, 0, "history iteration should be > 0")
		assert.NotEmpty(t, h.Status, "history entry should have status")
		t.Logf("  Iteration %d: status=%s, summary=%s (tasks_completed=%d)",
			h.Iteration, h.Status, h.Summary, h.TasksCompleted)
	}

	// Last history entry should show completion
	lastHistory := history[len(history)-1]
	assert.Equal(t, state.StatusDone, lastHistory.Status, "last history status should be DONE")
	assert.Equal(t, 2, lastHistory.TasksCompleted, "last history should show 2 tasks completed")

	// ============================================================
	// Verify files created by Claude exist on Sprite
	// ============================================================

	// Verify src directory exists
	_, _, exitCode, err = env.Client.ExecuteOutput(ctx, spriteName, repoPath, nil, "test", "-d", "src")
	require.NoError(t, err, "should be able to check src directory")
	assert.Equal(t, 0, exitCode, "src directory should exist")

	// Verify src/main.go exists with expected content
	mainGoContent, err := env.Client.ReadFile(ctx, spriteName, filepath.Join(repoPath, "src", "main.go"))
	require.NoError(t, err, "src/main.go should exist on sprite")
	assert.Contains(t, strings.ToLower(string(mainGoContent)), "hello",
		"main.go should contain hello (case insensitive)")
	t.Logf("main.go content length: %d bytes", len(mainGoContent))

	// Verify a git commit was made
	stdout, _, exitCode, err := env.Client.ExecuteOutput(ctx, spriteName, repoPath, nil, "git", "log", "--oneline", "-1")
	require.NoError(t, err, "git log should work")
	assert.Equal(t, 0, exitCode, "git log should succeed")
	assert.NotEmpty(t, stdout, "should have at least one commit")
	t.Logf("Latest commit: %s", strings.TrimSpace(string(stdout)))
}

// TestRealSprite_NeedsInputFlow verifies NEEDS_INPUT state works correctly.
func TestRealSprite_NeedsInputFlow(t *testing.T) {
	env := testutil.SetupRealSpriteEnv(t)

	spriteName := testutil.GenerateTestSpriteName(t)
	t.Logf("testing with Sprite: %s", spriteName)

	t.Cleanup(func() {
		testutil.CleanupSprite(t, env.Client, spriteName)
	})

	ctx, cancel := testutil.ClaudeExecutionContext(t)
	defer cancel()

	// Create the Sprite
	err := env.Client.Create(ctx, spriteName, "")
	require.NoError(t, err, "failed to create Sprite")

	// Setup repo path
	repoPath := "/home/sprite/test-org/test-repo"
	_, _, exitCode, err := env.Client.ExecuteOutput(ctx, spriteName, "", nil, "mkdir", "-p", repoPath)
	require.NoError(t, err, "failed to create repo directory")
	require.Equal(t, 0, exitCode, "mkdir should succeed")

	// Setup templates
	setupSpriteWithTemplates(t, env.Client, spriteName, repoPath)

	// Write task that will require input
	tasks := []state.Task{
		{
			Category:    state.CategorySetup,
			Description: "Create a config file with the user's preferred name",
			Steps:       []string{"Ask user for their preferred name", "Create config.json with that name"},
			Passes:      false,
		},
	}
	writeTasksToSprite(t, env.Client, spriteName, repoPath, tasks)

	// Write initial state
	initialState := &state.State{
		Status:  state.StatusContinue,
		Summary: "Starting work",
	}
	writeStateToSprite(t, env.Client, spriteName, repoPath, initialState)

	// Run first iteration - Claude should set status: NEEDS_INPUT
	t.Log("Running first Claude iteration (expecting NEEDS_INPUT)...")
	err = runClaudeIteration(t, env.Client, spriteName, repoPath, 0.50, 2*time.Minute)
	require.NoError(t, err, "first Claude iteration should complete")

	// Read state - should be NEEDS_INPUT
	state1 := readStateFromSprite(t, env.Client, spriteName, repoPath)
	t.Logf("After first iteration: status=%s, question=%s", state1.Status, state1.Question)

	// If Claude didn't ask for input, it may have made assumptions - that's okay too
	if state1.Status == state.StatusNeedsInput {
		assert.NotEmpty(t, state1.Question, "should have a question when NEEDS_INPUT")

		// Write response.json with answer
		response := state.Response{Answer: "TestUser"}
		responseData, err := json.MarshalIndent(response, "", "  ")
		require.NoError(t, err)
		err = env.Client.WriteFile(ctx, spriteName, filepath.Join(repoPath, ".wisp", "response.json"), responseData)
		require.NoError(t, err, "failed to write response.json")

		// Run second iteration - Claude should complete the task
		t.Log("Running second Claude iteration (with response)...")
		err = runClaudeIteration(t, env.Client, spriteName, repoPath, 0.50, 2*time.Minute)
		require.NoError(t, err, "second Claude iteration should complete")
	}

	// Verify final state
	finalState := readStateFromSprite(t, env.Client, spriteName, repoPath)
	t.Logf("Final state: status=%s, summary=%s", finalState.Status, finalState.Summary)
	assert.Contains(t, []string{state.StatusContinue, state.StatusDone}, finalState.Status,
		"final status should be CONTINUE or DONE")

	// Verify task completed
	finalTasks := readTasksFromSprite(t, env.Client, spriteName, repoPath)
	require.Len(t, finalTasks, 1, "should have 1 task")
	assert.True(t, finalTasks[0].Passes, "task should be marked as passes: true")

	// Verify config.json exists
	configContent, err := env.Client.ReadFile(ctx, spriteName, filepath.Join(repoPath, "config.json"))
	require.NoError(t, err, "config.json should exist")
	t.Logf("Config content: %s", string(configContent))
}
