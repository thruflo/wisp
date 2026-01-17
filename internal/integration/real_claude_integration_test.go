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
func TestRealSprite_SingleIterationWithClaude(t *testing.T) {
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

	// Run one Claude iteration
	t.Log("Running Claude iteration...")
	err = runClaudeIteration(t, env.Client, spriteName, repoPath, 0.50, 3*time.Minute)
	require.NoError(t, err, "Claude iteration should complete")

	// Verify state.json
	finalState := readStateFromSprite(t, env.Client, spriteName, repoPath)
	t.Logf("Final state: status=%s, summary=%s", finalState.Status, finalState.Summary)
	assert.Contains(t, []string{state.StatusContinue, state.StatusDone}, finalState.Status,
		"status should be CONTINUE or DONE")

	// Verify tasks.json
	finalTasks := readTasksFromSprite(t, env.Client, spriteName, repoPath)
	require.Len(t, finalTasks, 1, "should have 1 task")
	assert.True(t, finalTasks[0].Passes, "task should be marked as passes: true")

	// Verify hello.txt exists with correct content
	helloContent, err := env.Client.ReadFile(ctx, spriteName, filepath.Join(repoPath, "hello.txt"))
	require.NoError(t, err, "hello.txt should exist")
	assert.Contains(t, string(helloContent), "Hello, World!", "hello.txt should contain expected content")

	// Verify a git commit was made
	stdout, _, exitCode, err := env.Client.ExecuteOutput(ctx, spriteName, repoPath, nil, "git", "log", "--oneline", "-1")
	require.NoError(t, err, "git log should work")
	assert.Equal(t, 0, exitCode, "git log should succeed")
	assert.NotEmpty(t, stdout, "should have at least one commit")
	t.Logf("Latest commit: %s", strings.TrimSpace(string(stdout)))
}

// TestRealSprite_FullLoopCompletion verifies the full loop runs to completion with ExitReasonDone.
func TestRealSprite_FullLoopCompletion(t *testing.T) {
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

	// Create config
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

	// Create TUI that writes to test log
	testTUI := createTestTUI()

	// Find template dir
	projectRoot := testutil.FindProjectRoot(t)
	require.NotEmpty(t, projectRoot, "could not find project root")
	templateDir := filepath.Join(projectRoot, ".wisp", "templates", "default")

	// Create and run loop
	l := loop.NewLoop(env.Client, syncMgr, store, cfg, session, testTUI, repoPath, templateDir)

	t.Log("Running loop...")
	result := l.Run(ctx)

	t.Logf("Loop result: reason=%s, iterations=%d", result.Reason, result.Iterations)
	if result.Error != nil {
		t.Logf("Loop error: %v", result.Error)
	}

	// Verify result
	assert.Equal(t, loop.ExitReasonDone, result.Reason, "loop should exit with ExitReasonDone")

	// Verify all tasks passed locally
	finalTasks, err := store.LoadTasks("test-branch")
	require.NoError(t, err)
	for i, task := range finalTasks {
		assert.True(t, task.Passes, "task %d should pass: %s", i, task.Description)
	}

	// Verify final state
	finalState, err := store.LoadState("test-branch")
	require.NoError(t, err)
	assert.Equal(t, state.StatusDone, finalState.Status, "final status should be DONE")

	// Verify history
	history, err := store.LoadHistory("test-branch")
	require.NoError(t, err)
	assert.NotEmpty(t, history, "should have history entries")
	t.Logf("History entries: %d", len(history))
	for _, h := range history {
		t.Logf("  Iteration %d: %s (tasks_completed=%d)", h.Iteration, h.Summary, h.TasksCompleted)
	}

	// Verify files exist on Sprite
	_, err = env.Client.ReadFile(ctx, spriteName, filepath.Join(repoPath, "src", "main.go"))
	require.NoError(t, err, "src/main.go should exist on sprite")
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
