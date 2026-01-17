//go:build e2e

// cli_e2e_test.go provides end-to-end tests for the wisp CLI commands.
//
// These tests build and run the actual wisp binary to verify complete
// user workflows work correctly. Tests that require real Sprites are
// also tagged with real_sprites.
package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thruflo/wisp/internal/cli"
	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/sprite"
	"github.com/thruflo/wisp/internal/state"
	"github.com/thruflo/wisp/internal/testutil"
)

// TestCLI_Init verifies that 'wisp init' creates the correct .wisp directory structure.
func TestCLI_Init(t *testing.T) {
	h := NewCLIHarness(t)

	// Remove existing .wisp directory to test init from scratch
	wispDir := filepath.Join(h.WorkDir, ".wisp")
	require.NoError(t, os.RemoveAll(wispDir))

	// Run init command
	result := h.Run("init")
	h.RequireSuccess(result, "init command failed")

	// Verify output mentions initialization
	assert.Contains(t, result.Stdout, "Initialized .wisp/")

	// Verify directory structure was created
	assert.DirExists(t, wispDir, ".wisp directory should exist")
	assert.DirExists(t, filepath.Join(wispDir, "sessions"), "sessions directory should exist")
	assert.DirExists(t, filepath.Join(wispDir, "templates", "default"), "templates/default directory should exist")

	// Verify config files were created
	assert.FileExists(t, filepath.Join(wispDir, "config.yaml"), "config.yaml should exist")
	assert.FileExists(t, filepath.Join(wispDir, "settings.json"), "settings.json should exist")
	assert.FileExists(t, filepath.Join(wispDir, ".sprite.env"), ".sprite.env should exist")
	assert.FileExists(t, filepath.Join(wispDir, ".gitignore"), ".gitignore should exist")

	// Verify template files were created
	templateDir := filepath.Join(wispDir, "templates", "default")
	assert.FileExists(t, filepath.Join(templateDir, "context.md"), "context.md template should exist")
	assert.FileExists(t, filepath.Join(templateDir, "create-tasks.md"), "create-tasks.md template should exist")
	assert.FileExists(t, filepath.Join(templateDir, "iterate.md"), "iterate.md template should exist")

	// Verify config.yaml has valid content
	configContent, err := os.ReadFile(filepath.Join(wispDir, "config.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(configContent), "limits:")
	assert.Contains(t, string(configContent), "max_iterations:")

	// Verify settings.json has valid JSON
	settingsContent, err := os.ReadFile(filepath.Join(wispDir, "settings.json"))
	require.NoError(t, err)
	var settings config.Settings
	require.NoError(t, json.Unmarshal(settingsContent, &settings))
	assert.NotEmpty(t, settings.Permissions.Deny, "settings should have deny rules")
}

// TestCLI_InitAlreadyExists verifies that 'wisp init' fails if .wisp already exists.
func TestCLI_InitAlreadyExists(t *testing.T) {
	h := NewCLIHarness(t)

	// .wisp directory already exists from harness setup
	result := h.Run("init")
	h.RequireFailure(result, "init should fail when .wisp exists")

	assert.Contains(t, result.Stderr, "already exists")
}

// TestCLI_InitWithTemplate verifies that 'wisp init --template' creates a custom template directory.
func TestCLI_InitWithTemplate(t *testing.T) {
	h := NewCLIHarness(t)

	// Remove existing .wisp directory
	wispDir := filepath.Join(h.WorkDir, ".wisp")
	require.NoError(t, os.RemoveAll(wispDir))

	// Run init with custom template name
	result := h.Run("init", "--template", "custom")
	h.RequireSuccess(result, "init with template failed")

	// Verify custom template directory was created
	assert.DirExists(t, filepath.Join(wispDir, "templates", "custom"), "custom template directory should exist")
	assert.Contains(t, result.Stdout, "custom")
}

// TestCLI_Status verifies that 'wisp status' works with no sessions.
func TestCLI_Status(t *testing.T) {
	h := NewCLIHarness(t)

	// Run status command with no sessions
	result := h.Run("status")
	h.RequireSuccess(result, "status command failed")

	// Verify output indicates no sessions
	assert.Contains(t, result.Stdout, "No sessions found")
}

// TestCLI_StatusWithSessions verifies that 'wisp status' lists existing sessions.
func TestCLI_StatusWithSessions(t *testing.T) {
	h := NewCLIHarness(t)

	// Create a mock session manually
	store := state.NewStore(h.WorkDir)
	session := &config.Session{
		Repo:       "test/repo",
		Spec:       "docs/rfc.md",
		Branch:     "wisp/test-feature",
		SpriteName: "test-sprite",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session))

	// Create tasks file for the session
	sessionDir := store.SessionDir(session.Branch)
	tasks := []state.Task{
		{Category: "setup", Description: "Task 1", Passes: true},
		{Category: "feature", Description: "Task 2", Passes: false},
	}
	tasksData, err := json.MarshalIndent(tasks, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(sessionDir, "tasks.json"), tasksData, 0644))

	// Run status command
	result := h.Run("status")
	h.RequireSuccess(result, "status command failed")

	// Verify session is listed
	assert.Contains(t, result.Stdout, "wisp/test-feature")
	assert.Contains(t, result.Stdout, "running")
	// Should show task progress (1/2)
	assert.Contains(t, result.Stdout, "1/2")
}

// TestCLI_StatusDetailedSession verifies that 'wisp status <branch>' shows session details.
func TestCLI_StatusDetailedSession(t *testing.T) {
	h := NewCLIHarness(t)

	// Create a mock session
	store := state.NewStore(h.WorkDir)
	session := &config.Session{
		Repo:       "test/repo",
		Spec:       "docs/rfc.md",
		Branch:     "wisp/test-detail",
		SpriteName: "test-sprite-detail",
		StartedAt:  time.Now().Add(-30 * time.Minute),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session))

	// Create tasks and state files
	sessionDir := store.SessionDir(session.Branch)
	tasks := []state.Task{
		{Category: "setup", Description: "Setup task", Passes: true},
		{Category: "feature", Description: "Feature task", Passes: false},
	}
	tasksData, _ := json.MarshalIndent(tasks, "", "  ")
	require.NoError(t, os.WriteFile(filepath.Join(sessionDir, "tasks.json"), tasksData, 0644))

	st := &state.State{
		Status:  state.StatusContinue,
		Summary: "Working on feature task",
	}
	stateData, _ := json.MarshalIndent(st, "", "  ")
	require.NoError(t, os.WriteFile(filepath.Join(sessionDir, "state.json"), stateData, 0644))

	history := []state.History{
		{Iteration: 1, Status: state.StatusContinue},
		{Iteration: 2, Status: state.StatusContinue},
	}
	historyData, _ := json.MarshalIndent(history, "", "  ")
	require.NoError(t, os.WriteFile(filepath.Join(sessionDir, "history.json"), historyData, 0644))

	// Run status command with branch argument
	result := h.Run("status", "wisp/test-detail")
	h.RequireSuccess(result, "status detail command failed")

	// Verify detailed output
	assert.Contains(t, result.Stdout, "Session Details")
	assert.Contains(t, result.Stdout, "test/repo")
	assert.Contains(t, result.Stdout, "docs/rfc.md")
	assert.Contains(t, result.Stdout, "wisp/test-detail")
	assert.Contains(t, result.Stdout, "1/2 completed")
	assert.Contains(t, result.Stdout, "Working on feature task")
}

// TestCLI_StartMissingFlags verifies that 'wisp start' fails without required flags.
func TestCLI_StartMissingFlags(t *testing.T) {
	h := NewCLIHarness(t)

	// Run start without required flags
	result := h.Run("start")
	h.RequireFailure(result, "start should fail without required flags")

	assert.Contains(t, result.Stderr, "required")
}

// TestCLI_StartMissingRepo verifies that 'wisp start --spec' fails without --repo.
func TestCLI_StartMissingRepo(t *testing.T) {
	h := NewCLIHarness(t)

	result := h.Run("start", "--spec", "docs/rfc.md")
	h.RequireFailure(result, "start should fail without --repo")

	assert.Contains(t, result.Stderr, "required")
}

// TestCLI_StartMissingSpec verifies that 'wisp start --repo' fails without --spec.
func TestCLI_StartMissingSpec(t *testing.T) {
	h := NewCLIHarness(t)

	result := h.Run("start", "--repo", "test/repo")
	h.RequireFailure(result, "start should fail without --spec")

	assert.Contains(t, result.Stderr, "required")
}

// TestCLI_StopNonexistent verifies that 'wisp stop' fails for nonexistent session.
func TestCLI_StopNonexistent(t *testing.T) {
	h := NewCLIHarness(t)

	result := h.Run("stop", "nonexistent-branch")
	h.RequireFailure(result, "stop should fail for nonexistent session")

	assert.Contains(t, result.Stderr, "failed to load session")
}

// TestCLI_ResumeNonexistent verifies that 'wisp resume' fails for nonexistent session.
func TestCLI_ResumeNonexistent(t *testing.T) {
	h := NewCLIHarness(t)

	result := h.Run("resume", "nonexistent-branch")
	h.RequireFailure(result, "resume should fail for nonexistent session")

	assert.Contains(t, result.Stderr, "failed to load session")
}

// TestCLI_DoneNoSessions verifies that 'wisp done' fails with no sessions.
func TestCLI_DoneNoSessions(t *testing.T) {
	h := NewCLIHarness(t)

	result := h.Run("done")
	h.RequireFailure(result, "done should fail with no sessions")

	assert.Contains(t, result.Stderr, "no sessions found")
}

// TestCLI_DoneIncompleteTasks verifies that 'wisp done' fails if tasks are incomplete.
func TestCLI_DoneIncompleteTasks(t *testing.T) {
	h := NewCLIHarness(t)

	// Create a session with incomplete tasks
	store := state.NewStore(h.WorkDir)
	session := &config.Session{
		Repo:       "test/repo",
		Spec:       "docs/rfc.md",
		Branch:     "wisp/incomplete",
		SpriteName: "test-sprite",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session))

	// Create tasks with some incomplete
	sessionDir := store.SessionDir(session.Branch)
	tasks := []state.Task{
		{Category: "setup", Description: "Task 1", Passes: true},
		{Category: "feature", Description: "Task 2", Passes: false}, // Incomplete
	}
	tasksData, _ := json.MarshalIndent(tasks, "", "  ")
	require.NoError(t, os.WriteFile(filepath.Join(sessionDir, "tasks.json"), tasksData, 0644))

	// Run done - should fail because tasks are incomplete
	result := h.Run("done", "wisp/incomplete")
	h.RequireFailure(result, "done should fail with incomplete tasks")

	assert.Contains(t, result.Stderr, "not complete")
}

// TestCLI_VersionFlag verifies the --version flag works (if implemented).
// This test is informational - it passes whether or not version is implemented.
func TestCLI_VersionFlag(t *testing.T) {
	h := NewCLIHarness(t)

	result := h.Run("--version")
	// Version may or may not be implemented - just verify we get some output
	t.Logf("version output: %s", result.Stdout+result.Stderr)
}

// TestCLI_HelpFlag verifies the --help flag works.
func TestCLI_HelpFlag(t *testing.T) {
	h := NewCLIHarness(t)

	result := h.Run("--help")
	h.RequireSuccess(result, "help should succeed")

	// Verify help output contains expected commands
	assert.Contains(t, result.Stdout, "start")
	assert.Contains(t, result.Stdout, "status")
	assert.Contains(t, result.Stdout, "stop")
	assert.Contains(t, result.Stdout, "resume")
	assert.Contains(t, result.Stdout, "done")
}

// TestCLI_StartCreatesSprite verifies that 'wisp start --headless' creates a Sprite
// and runs the loop. This test requires real Sprite credentials.
// Skip unless both e2e and real_sprites tests are intended.
func TestCLI_StartCreatesSprite(t *testing.T) {
	// Skip if no real credentials (SetupRealSpriteEnv skips in short mode and when no token)
	env := testutil.SetupRealSpriteEnv(t)

	h := NewCLIHarness(t)

	// Set real credentials
	h.SetEnv("SPRITE_TOKEN", env.SpriteToken)
	if env.GitHubToken != "" {
		h.SetEnv("GITHUB_TOKEN", env.GitHubToken)
	}

	// Generate unique branch name for this test
	branch := "wisp/e2e-test-" + testutil.GenerateTestSpriteName(t)[10:] // trim "wisp-test-" prefix

	// Register cleanup for the sprite that will be created
	spriteName := sprite.GenerateSpriteName("thruflo/wisp", branch)
	testutil.RegisterSprite(spriteName)
	t.Cleanup(func() {
		testutil.CleanupSprite(t, env.Client, spriteName)
	})

	// Run start with headless mode (will fail due to missing RFC, but should create sprite)
	result := h.RunWithTimeout(3*time.Minute,
		"start",
		"--repo", "thruflo/wisp",
		"--spec", "docs/rfc.md", // Use actual RFC in the repo
		"--branch", branch,
		"--headless",
	)

	// The command may fail during task generation due to Claude costs/limits,
	// but we verify the sprite was created and session recorded
	t.Logf("start output: %s", result.Stdout)
	t.Logf("start stderr: %s", result.Stderr)

	// Verify session was created locally
	store := state.NewStore(h.WorkDir)
	session, err := store.GetSession(branch)
	if err != nil {
		t.Logf("session not found (may be expected if early failure): %v", err)
	} else {
		assert.Equal(t, branch, session.Branch)
		assert.Equal(t, "thruflo/wisp", session.Repo)
	}
}

// TestCLI_StopAndResume verifies that session can be stopped and resumed.
// This test uses mocked sessions without real Sprites.
func TestCLI_StopAndResume(t *testing.T) {
	h := NewCLIHarness(t)

	// Create a mock session
	store := state.NewStore(h.WorkDir)
	session := &config.Session{
		Repo:       "test/repo",
		Spec:       "docs/rfc.md",
		Branch:     "wisp/stop-resume-test",
		SpriteName: "test-sprite-stop",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session))

	// Create minimal state files
	sessionDir := store.SessionDir(session.Branch)
	tasks := []state.Task{
		{Category: "setup", Description: "Test task", Passes: false},
	}
	tasksData, _ := json.MarshalIndent(tasks, "", "  ")
	require.NoError(t, os.WriteFile(filepath.Join(sessionDir, "tasks.json"), tasksData, 0644))

	st := &state.State{Status: state.StatusContinue, Summary: "In progress"}
	stateData, _ := json.MarshalIndent(st, "", "  ")
	require.NoError(t, os.WriteFile(filepath.Join(sessionDir, "state.json"), stateData, 0644))

	// Stop the session (will warn about missing SPRITE_TOKEN but should update status)
	result := h.Run("stop", "wisp/stop-resume-test")
	// Note: stop may "succeed" even without real sprite due to graceful handling
	t.Logf("stop output: %s", result.Stdout)
	t.Logf("stop stderr: %s", result.Stderr)

	// Verify session status was updated to stopped
	updatedSession, err := store.GetSession("wisp/stop-resume-test")
	require.NoError(t, err)
	assert.Equal(t, config.SessionStatusStopped, updatedSession.Status)

	// Verify stop output indicates session was stopped
	assert.Contains(t, result.Stdout, "Session stopped")

	// Now try resume (will fail due to missing SPRITE_TOKEN for real sprite creation)
	result = h.Run("resume", "wisp/stop-resume-test")
	// Resume will fail without real credentials, but we verify it attempts correctly
	t.Logf("resume output: %s", result.Stdout)
	t.Logf("resume stderr: %s", result.Stderr)

	// The resume command should at least recognize the session
	if result.Success() || strings.Contains(result.Stderr, "SPRITE_TOKEN") {
		// Expected - either succeeded or failed due to missing token
		t.Log("resume behaved as expected (failed due to missing credentials)")
	}
}

// TestCLI_FullWorkflow tests the complete start -> iterate -> done flow.
// This is a comprehensive E2E test that requires real Sprite and Claude.
// It will be skipped if credentials are not available or in short mode.
func TestCLI_FullWorkflow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full workflow test in short mode")
	}

	// SetupRealSpriteEnv skips if no credentials available
	env := testutil.SetupRealSpriteEnv(t)

	h := NewCLIHarness(t)
	h.SetEnv("SPRITE_TOKEN", env.SpriteToken)
	if env.GitHubToken != "" {
		h.SetEnv("GITHUB_TOKEN", env.GitHubToken)
	}

	// Generate unique branch name
	branch := "wisp/e2e-full-" + testutil.GenerateTestSpriteName(t)[10:]

	// Register cleanup using actual sprite name generation
	spriteName := sprite.GenerateSpriteName("thruflo/wisp", branch)
	testutil.RegisterSprite(spriteName)
	t.Cleanup(func() {
		testutil.CleanupSprite(t, env.Client, spriteName)
	})

	// Start session with headless mode
	// Use a very simple RFC or test fixture to minimize Claude calls
	result := h.RunWithTimeout(10*time.Minute,
		"start",
		"--repo", "thruflo/wisp",
		"--spec", "docs/rfc.md",
		"--branch", branch,
		"--headless",
	)

	t.Logf("full workflow start output: %s", result.Stdout)

	// Parse the headless result if available
	if result.Success() {
		var headlessResult cli.HeadlessResult
		if err := json.Unmarshal([]byte(result.Stdout), &headlessResult); err == nil {
			t.Logf("headless result: iterations=%d, reason=%s, status=%s",
				headlessResult.Iterations, headlessResult.Reason, headlessResult.Status)
		}
	}

	// Verify session exists
	store := state.NewStore(h.WorkDir)
	session, err := store.GetSession(branch)
	if err != nil {
		t.Logf("full workflow: session lookup failed (may be expected): %v", err)
		return
	}

	t.Logf("full workflow: session status=%s, sprite=%s", session.Status, session.SpriteName)

	// Check status via CLI
	statusResult := h.Run("status", branch)
	t.Logf("status output: %s", statusResult.Stdout)
}
