//go:build integration

// headless_test.go contains integration tests for headless mode execution.
//
// These tests verify that the --headless flag works correctly with mock
// components, producing valid JSON output without TUI interaction.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thruflo/wisp/internal/cli"
	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/loop"
	"github.com/thruflo/wisp/internal/sprite"
	"github.com/thruflo/wisp/internal/state"
	"github.com/thruflo/wisp/internal/testutil"
	"github.com/thruflo/wisp/internal/tui"
)

// TestHeadlessLoopProducesValidResult verifies that running a loop with a
// no-op TUI (as headless mode does) produces a valid result that can be
// serialized to JSON.
func TestHeadlessLoopProducesValidResult(t *testing.T) {
	tmpDir, store := testutil.SetupTestDir(t)

	cfg, err := config.LoadConfig(tmpDir)
	require.NoError(t, err)

	// Configure for quick exit (max 1 iteration)
	cfg.Limits.MaxIterations = 1

	session := &config.Session{
		Repo:       "test/repo",
		Branch:     "test-headless",
		SpriteName: "test-sprite-headless",
		Status:     config.SessionStatusRunning,
		StartedAt:  time.Now(),
	}
	require.NoError(t, store.CreateSession(session))

	// Create mock client that returns a DONE state
	mockClient := newHeadlessMockClient()

	// Set up responses for state.json to return DONE
	stateData, _ := json.Marshal(&state.State{
		Status:  state.StatusDone,
		Summary: "Test completed via headless",
	})
	mockClient.ReadResponses["/home/sprite/test/repo/.wisp/state.json"] = stateData

	// Set up tasks response
	tasksData, _ := json.Marshal([]state.Task{
		{Category: state.CategoryTest, Description: "Test task", Passes: true},
	})
	mockClient.ReadResponses["/home/sprite/test/repo/.wisp/tasks.json"] = tasksData

	syncMgr := state.NewSyncManager(mockClient, store)

	// Use NewNopTUI as headless mode does
	nopTUI := tui.NewNopTUI()

	l := loop.NewLoopWithOptions(loop.LoopOptions{
		Client:      mockClient,
		SyncManager: syncMgr,
		Store:       store,
		Config:      cfg,
		Session:     session,
		TUI:         nopTUI,
		RepoPath:    "/home/sprite/test/repo",
		TemplateDir: tmpDir,
		ClaudeConfig: loop.ClaudeConfig{
			MaxTurns:     1,
			OutputFormat: "stream-json",
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result := l.Run(ctx)

	// Build HeadlessResult as the CLI would
	headlessResult := cli.HeadlessResult{
		Reason:     result.Reason.String(),
		Iterations: result.Iterations,
		Branch:     session.Branch,
		SpriteName: session.SpriteName,
	}

	if result.Error != nil {
		headlessResult.Error = result.Error.Error()
	}

	if result.State != nil {
		headlessResult.Status = result.State.Status
		headlessResult.Summary = result.State.Summary
	}

	// Verify JSON serialization works
	output, err := json.MarshalIndent(headlessResult, "", "  ")
	require.NoError(t, err)
	assert.NotEmpty(t, output)

	// Verify we can parse it back
	var parsed cli.HeadlessResult
	require.NoError(t, json.Unmarshal(output, &parsed))
	assert.Equal(t, headlessResult.Branch, parsed.Branch)
	assert.Equal(t, headlessResult.SpriteName, parsed.SpriteName)
}

// TestNopTUIDoesNotBlock verifies that the no-op TUI methods don't block
// and can be called safely in headless mode.
func TestNopTUIDoesNotBlock(t *testing.T) {
	nopTUI := tui.NewNopTUI()

	// These should all complete without blocking
	nopTUI.SetState(tui.ViewState{
		Branch:         "test",
		Iteration:      1,
		MaxIterations:  10,
		Status:         "RUNNING",
		CompletedTasks: 0,
		TotalTasks:     5,
	})

	nopTUI.SetView(tui.ViewTail)
	nopTUI.AppendTailLine("test output line")
	nopTUI.AppendTailLines([]string{"line 1", "line 2"})
	nopTUI.Update()
	nopTUI.Bell()
	nopTUI.ClearTail()

	viewState := nopTUI.GetState()
	assert.Equal(t, "test", viewState.Branch)

	view := nopTUI.GetView()
	assert.Equal(t, tui.ViewTail, view)
}

// TestHeadlessResultOmitsEmptyFields verifies that optional fields are
// omitted from JSON when empty (using omitempty).
func TestHeadlessResultOmitsEmptyFields(t *testing.T) {
	result := cli.HeadlessResult{
		Reason:     "max iterations",
		Iterations: 10,
		Branch:     "test-branch",
		SpriteName: "test-sprite",
		// Error, Status, Summary intentionally left empty
	}

	data, err := json.Marshal(result)
	require.NoError(t, err)

	// Verify empty fields are omitted
	dataStr := string(data)
	assert.NotContains(t, dataStr, "error")
	assert.NotContains(t, dataStr, "status")
	assert.NotContains(t, dataStr, "summary")

	// Required fields should be present
	assert.Contains(t, dataStr, "reason")
	assert.Contains(t, dataStr, "iterations")
	assert.Contains(t, dataStr, "branch")
	assert.Contains(t, dataStr, "sprite_name")
}

// headlessMockClient implements sprite.Client for headless testing.
// It's a minimal implementation that supports configurable ReadFile responses.
type headlessMockClient struct {
	ReadResponses map[string][]byte
}

func newHeadlessMockClient() *headlessMockClient {
	return &headlessMockClient{
		ReadResponses: make(map[string][]byte),
	}
}

func (m *headlessMockClient) Create(ctx context.Context, name string, checkpoint string) error {
	return nil
}

func (m *headlessMockClient) Delete(ctx context.Context, name string) error {
	return nil
}

func (m *headlessMockClient) Exists(ctx context.Context, name string) (bool, error) {
	return true, nil
}

func (m *headlessMockClient) Execute(ctx context.Context, name string, dir string, env []string, args ...string) (*sprite.Cmd, error) {
	// Return a Cmd with mock readers
	return sprite.NewMockCmd(
		io.NopCloser(bytes.NewReader([]byte{})),
		io.NopCloser(bytes.NewReader([]byte{})),
	), nil
}

func (m *headlessMockClient) ExecuteOutput(ctx context.Context, name string, dir string, env []string, args ...string) (stdout, stderr []byte, exitCode int, err error) {
	return nil, nil, 0, nil
}

func (m *headlessMockClient) ExecuteOutputWithRetry(ctx context.Context, name string, dir string, env []string, args ...string) (stdout, stderr []byte, exitCode int, err error) {
	return m.ExecuteOutput(ctx, name, dir, env, args...)
}

func (m *headlessMockClient) WriteFile(ctx context.Context, name string, path string, content []byte) error {
	return nil
}

func (m *headlessMockClient) ReadFile(ctx context.Context, name string, path string) ([]byte, error) {
	if content, ok := m.ReadResponses[path]; ok {
		return content, nil
	}
	return []byte("{}"), nil
}

// Verify headlessMockClient implements sprite.Client
var _ sprite.Client = (*headlessMockClient)(nil)
