package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/sprite"
)

// mockSpriteClient implements sprite.Client for testing.
type mockSpriteClient struct {
	writeCalls   []writeCall
	readCalls    []readCall
	executeCalls []executeCall

	writeErr   error
	readErr    error
	executeErr error

	readResponses map[string][]byte
}

type writeCall struct {
	name    string
	path    string
	content []byte
}

type readCall struct {
	name string
	path string
}

type executeCall struct {
	name string
	dir  string
	env  []string
	args []string
}

func newMockSpriteClient() *mockSpriteClient {
	return &mockSpriteClient{
		readResponses: make(map[string][]byte),
	}
}

func (m *mockSpriteClient) Create(ctx context.Context, name string, checkpoint string) error {
	return nil
}

func (m *mockSpriteClient) Execute(ctx context.Context, name string, dir string, env []string, args ...string) (*sprite.Cmd, error) {
	m.executeCalls = append(m.executeCalls, executeCall{name: name, dir: dir, env: env, args: args})
	if m.executeErr != nil {
		return nil, m.executeErr
	}
	// Return a mock cmd that does nothing
	return &sprite.Cmd{
		Stdout: io.NopCloser(bytes.NewReader(nil)),
		Stderr: io.NopCloser(bytes.NewReader(nil)),
	}, nil
}

func (m *mockSpriteClient) WriteFile(ctx context.Context, name string, path string, content []byte) error {
	m.writeCalls = append(m.writeCalls, writeCall{name: name, path: path, content: content})
	return m.writeErr
}

func (m *mockSpriteClient) ReadFile(ctx context.Context, name string, path string) ([]byte, error) {
	m.readCalls = append(m.readCalls, readCall{name: name, path: path})
	if m.readErr != nil {
		return nil, m.readErr
	}
	if content, ok := m.readResponses[path]; ok {
		return content, nil
	}
	return nil, errors.New("file not found")
}

func (m *mockSpriteClient) Delete(ctx context.Context, name string) error {
	return nil
}

func (m *mockSpriteClient) Exists(ctx context.Context, name string) (bool, error) {
	return true, nil
}

func TestSyncManager_SyncToSprite(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)
	mockClient := newMockSpriteClient()
	syncManager := NewSyncManager(mockClient, store)

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

	state := &State{Status: StatusContinue, Summary: "Working on it"}
	require.NoError(t, store.SaveState("wisp/feature", state))

	tasks := []Task{
		{Category: CategorySetup, Description: "Setup project", Passes: true},
		{Category: CategoryFeature, Description: "Add feature", Passes: false},
	}
	require.NoError(t, store.SaveTasks("wisp/feature", tasks))

	history := []History{
		{Iteration: 1, Summary: "First iteration", TasksCompleted: 1, Status: StatusContinue},
	}
	require.NoError(t, store.SaveHistory("wisp/feature", history))

	// Sync to sprite
	ctx := context.Background()
	err := syncManager.SyncToSprite(ctx, "wisp-abc", "wisp/feature", "/home/sprite/repo")
	require.NoError(t, err)

	// Verify writes
	require.Len(t, mockClient.writeCalls, 3)

	// Check state was written
	var stateWritten bool
	for _, call := range mockClient.writeCalls {
		if call.path == "/home/sprite/repo/.wisp/state.json" {
			stateWritten = true
			assert.Contains(t, string(call.content), "CONTINUE")
			assert.Contains(t, string(call.content), "Working on it")
		}
	}
	assert.True(t, stateWritten)

	// Check tasks was written
	var tasksWritten bool
	for _, call := range mockClient.writeCalls {
		if call.path == "/home/sprite/repo/.wisp/tasks.json" {
			tasksWritten = true
			assert.Contains(t, string(call.content), "Setup project")
		}
	}
	assert.True(t, tasksWritten)

	// Check history was written
	var historyWritten bool
	for _, call := range mockClient.writeCalls {
		if call.path == "/home/sprite/repo/.wisp/history.json" {
			historyWritten = true
			assert.Contains(t, string(call.content), "First iteration")
		}
	}
	assert.True(t, historyWritten)
}

func TestSyncManager_SyncToSprite_NoFiles(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)
	mockClient := newMockSpriteClient()
	syncManager := NewSyncManager(mockClient, store)

	// Create session without any state files
	session := &config.Session{
		Repo:       "org/repo",
		Spec:       "spec.md",
		Branch:     "wisp/feature",
		SpriteName: "wisp-abc",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}
	require.NoError(t, store.CreateSession(session))

	// Sync to sprite - should not fail, just skip files
	ctx := context.Background()
	err := syncManager.SyncToSprite(ctx, "wisp-abc", "wisp/feature", "/home/sprite/repo")
	require.NoError(t, err)

	// No writes should have occurred
	assert.Empty(t, mockClient.writeCalls)
}

func TestSyncManager_SyncFromSprite(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)
	mockClient := newMockSpriteClient()
	syncManager := NewSyncManager(mockClient, store)

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

	// Set up mock responses
	mockClient.readResponses["/home/sprite/repo/.wisp/state.json"] = []byte(`{"status":"DONE","summary":"Completed all tasks"}`)
	mockClient.readResponses["/home/sprite/repo/.wisp/tasks.json"] = []byte(`[{"category":"feature","description":"Done task","steps":[],"passes":true}]`)
	mockClient.readResponses["/home/sprite/repo/.wisp/history.json"] = []byte(`[{"iteration":1,"summary":"Finished","tasks_completed":1,"status":"DONE"}]`)

	// Sync from sprite
	ctx := context.Background()
	err := syncManager.SyncFromSprite(ctx, "wisp-abc", "wisp/feature", "/home/sprite/repo")
	require.NoError(t, err)

	// Verify reads
	require.Len(t, mockClient.readCalls, 3)

	// Verify local state was updated
	state, err := store.LoadState("wisp/feature")
	require.NoError(t, err)
	require.NotNil(t, state)
	assert.Equal(t, StatusDone, state.Status)
	assert.Equal(t, "Completed all tasks", state.Summary)

	// Verify local tasks were updated
	tasks, err := store.LoadTasks("wisp/feature")
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "Done task", tasks[0].Description)
	assert.True(t, tasks[0].Passes)

	// Verify local history was updated
	history, err := store.LoadHistory("wisp/feature")
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, StatusDone, history[0].Status)
}

func TestSyncManager_SyncFromSprite_MissingFiles(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)
	mockClient := newMockSpriteClient()
	syncManager := NewSyncManager(mockClient, store)

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

	// No mock responses - all files missing

	// Sync from sprite - should not fail
	ctx := context.Background()
	err := syncManager.SyncFromSprite(ctx, "wisp-abc", "wisp/feature", "/home/sprite/repo")
	require.NoError(t, err)

	// No local files should have been created
	state, err := store.LoadState("wisp/feature")
	require.NoError(t, err)
	assert.Nil(t, state)
}

func TestSyncManager_CopySettingsToSprite(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)
	mockClient := newMockSpriteClient()
	syncManager := NewSyncManager(mockClient, store)

	settings := &config.Settings{
		Permissions: config.Permissions{
			Deny: []string{"Read(~/.ssh/**)", "Edit(~/.aws/**)"},
		},
		MCPServers: map[string]config.MCPServer{
			"playwright": {
				Command: "npx",
				Args:    []string{"@anthropic/mcp-playwright"},
			},
		},
	}

	ctx := context.Background()
	err := syncManager.CopySettingsToSprite(ctx, "wisp-abc", settings)
	require.NoError(t, err)

	// Should have executed mkdir and written file
	require.Len(t, mockClient.executeCalls, 1)
	assert.Contains(t, mockClient.executeCalls[0].args, "mkdir -p /home/sprite/.claude")

	require.Len(t, mockClient.writeCalls, 1)
	assert.Equal(t, "/home/sprite/.claude/settings.json", mockClient.writeCalls[0].path)
	assert.Contains(t, string(mockClient.writeCalls[0].content), "Read(~/.ssh/**)")
	assert.Contains(t, string(mockClient.writeCalls[0].content), "playwright")
}

func TestSyncManager_CopyTemplatesToSprite(t *testing.T) {
	t.Parallel()

	// Create local templates directory
	tmpDir := t.TempDir()
	templatesDir := filepath.Join(tmpDir, "templates")
	require.NoError(t, os.MkdirAll(templatesDir, 0o755))

	// Create template files
	require.NoError(t, os.WriteFile(filepath.Join(templatesDir, "iterate.md"), []byte("# Iteration prompt"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(templatesDir, "create-tasks.md"), []byte("# Create tasks prompt"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(templatesDir, "context.md"), []byte("# Context"), 0o644))

	store := NewStore(tmpDir)
	mockClient := newMockSpriteClient()
	syncManager := NewSyncManager(mockClient, store)

	ctx := context.Background()
	err := syncManager.CopyTemplatesToSprite(ctx, "wisp-abc", "/home/sprite/repo", templatesDir)
	require.NoError(t, err)

	// Should have executed mkdir
	require.Len(t, mockClient.executeCalls, 1)
	assert.Contains(t, mockClient.executeCalls[0].args, "mkdir -p /home/sprite/repo/.wisp")

	// Should have written all 3 template files
	require.Len(t, mockClient.writeCalls, 3)

	// Verify each template was written
	paths := make(map[string]string)
	for _, call := range mockClient.writeCalls {
		paths[call.path] = string(call.content)
	}

	assert.Contains(t, paths["/home/sprite/repo/.wisp/iterate.md"], "Iteration prompt")
	assert.Contains(t, paths["/home/sprite/repo/.wisp/create-tasks.md"], "Create tasks prompt")
	assert.Contains(t, paths["/home/sprite/repo/.wisp/context.md"], "Context")
}

func TestSyncManager_WriteResponseToSprite(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)
	mockClient := newMockSpriteClient()
	syncManager := NewSyncManager(mockClient, store)

	ctx := context.Background()
	err := syncManager.WriteResponseToSprite(ctx, "wisp-abc", "/home/sprite/repo", "Yes, use the Redis approach")
	require.NoError(t, err)

	require.Len(t, mockClient.writeCalls, 1)
	assert.Equal(t, "/home/sprite/repo/.wisp/response.json", mockClient.writeCalls[0].path)
	assert.Contains(t, string(mockClient.writeCalls[0].content), "Yes, use the Redis approach")
	assert.Contains(t, string(mockClient.writeCalls[0].content), `"answer"`)
}

func TestSyncManager_EnsureWispDirOnSprite(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)
	mockClient := newMockSpriteClient()
	syncManager := NewSyncManager(mockClient, store)

	ctx := context.Background()
	err := syncManager.EnsureWispDirOnSprite(ctx, "wisp-abc", "/home/sprite/repo")
	require.NoError(t, err)

	require.Len(t, mockClient.executeCalls, 1)
	assert.Contains(t, mockClient.executeCalls[0].args, "mkdir -p /home/sprite/repo/.wisp")
}

func TestSyncManager_SyncToSprite_WriteError(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)
	mockClient := newMockSpriteClient()
	mockClient.writeErr = errors.New("disk full")
	syncManager := NewSyncManager(mockClient, store)

	// Create session with state
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

	ctx := context.Background()
	err := syncManager.SyncToSprite(ctx, "wisp-abc", "wisp/feature", "/home/sprite/repo")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disk full")
}

func TestSyncManager_SyncFromSprite_ParseError(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := NewStore(tmpDir)
	mockClient := newMockSpriteClient()
	syncManager := NewSyncManager(mockClient, store)

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

	// Set up invalid JSON response
	mockClient.readResponses["/home/sprite/repo/.wisp/state.json"] = []byte(`{invalid json}`)

	ctx := context.Background()
	err := syncManager.SyncFromSprite(ctx, "wisp-abc", "wisp/feature", "/home/sprite/repo")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse state")
}

func TestResponse_Marshal(t *testing.T) {
	t.Parallel()

	response := Response{Answer: "Test answer"}

	// Verify it marshals correctly
	data, err := json.Marshal(response)
	require.NoError(t, err)
	assert.JSONEq(t, `{"answer":"Test answer"}`, string(data))
}
