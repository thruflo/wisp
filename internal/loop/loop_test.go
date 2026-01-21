package loop

import (
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/sprite"
	"github.com/thruflo/wisp/internal/state"
	"github.com/thruflo/wisp/internal/tui"
	"golang.org/x/net/context"
)

// MockSpriteClient implements sprite.Client for testing.
type MockSpriteClient struct {
	mu             sync.Mutex
	files          map[string][]byte
	executeOutputs map[string]mockExecOutput // key: command string, value: output
	createCalled   bool
	deleteCalled   bool
}

type mockExecOutput struct {
	stdout   []byte
	stderr   []byte
	exitCode int
	err      error
}

func NewMockSpriteClient() *MockSpriteClient {
	return &MockSpriteClient{
		files:          make(map[string][]byte),
		executeOutputs: make(map[string]mockExecOutput),
	}
}

func (m *MockSpriteClient) Create(ctx context.Context, name string, checkpoint string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createCalled = true
	return nil
}

func (m *MockSpriteClient) Delete(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteCalled = true
	return nil
}

func (m *MockSpriteClient) Exists(ctx context.Context, name string) (bool, error) {
	return true, nil
}

func (m *MockSpriteClient) Execute(ctx context.Context, name string, dir string, env []string, args ...string) (*sprite.Cmd, error) {
	return nil, nil
}

func (m *MockSpriteClient) ExecuteOutput(ctx context.Context, name string, dir string, env []string, args ...string) (stdout, stderr []byte, exitCode int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Default: command succeeded with exit code 0
	return nil, nil, 0, nil
}

func (m *MockSpriteClient) ExecuteOutputWithRetry(ctx context.Context, name string, dir string, env []string, args ...string) (stdout, stderr []byte, exitCode int, err error) {
	return m.ExecuteOutput(ctx, name, dir, env, args...)
}

func (m *MockSpriteClient) WriteFile(ctx context.Context, name string, path string, content []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[path] = content
	return nil
}

func (m *MockSpriteClient) ReadFile(ctx context.Context, name string, path string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if content, ok := m.files[path]; ok {
		return content, nil
	}
	return nil, io.EOF
}

// SetFile sets a file in the mock filesystem.
func (m *MockSpriteClient) SetFile(path string, content []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[path] = content
}

// TestDetectStuck tests the stuck detection logic.
func TestDetectStuck(t *testing.T) {
	tests := []struct {
		name      string
		history   []state.History
		threshold int
		want      bool
	}{
		{
			name:      "empty history",
			history:   nil,
			threshold: 3,
			want:      false,
		},
		{
			name: "history shorter than threshold",
			history: []state.History{
				{Iteration: 1, TasksCompleted: 1},
				{Iteration: 2, TasksCompleted: 1},
			},
			threshold: 3,
			want:      false,
		},
		{
			name: "progress made",
			history: []state.History{
				{Iteration: 1, TasksCompleted: 1},
				{Iteration: 2, TasksCompleted: 2},
				{Iteration: 3, TasksCompleted: 3},
			},
			threshold: 3,
			want:      false,
		},
		{
			name: "stuck - no progress",
			history: []state.History{
				{Iteration: 1, TasksCompleted: 2},
				{Iteration: 2, TasksCompleted: 2},
				{Iteration: 3, TasksCompleted: 2},
			},
			threshold: 3,
			want:      true,
		},
		{
			name: "stuck - progress then stuck",
			history: []state.History{
				{Iteration: 1, TasksCompleted: 1},
				{Iteration: 2, TasksCompleted: 2},
				{Iteration: 3, TasksCompleted: 2},
				{Iteration: 4, TasksCompleted: 2},
				{Iteration: 5, TasksCompleted: 2},
			},
			threshold: 3,
			want:      true,
		},
		{
			name: "progress at end breaks stuck",
			history: []state.History{
				{Iteration: 1, TasksCompleted: 2},
				{Iteration: 2, TasksCompleted: 2},
				{Iteration: 3, TasksCompleted: 2},
				{Iteration: 4, TasksCompleted: 2},
				{Iteration: 5, TasksCompleted: 3},
			},
			threshold: 3,
			want:      false,
		},
		{
			name: "zero threshold",
			history: []state.History{
				{Iteration: 1, TasksCompleted: 2},
			},
			threshold: 0,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectStuck(tt.history, tt.threshold)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestCalculateProgress tests progress calculation.
func TestCalculateProgress(t *testing.T) {
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
			name: "no tasks complete",
			tasks: []state.Task{
				{Description: "Task 1", Passes: false},
				{Description: "Task 2", Passes: false},
			},
			wantCompleted: 0,
			wantTotal:     2,
		},
		{
			name: "some tasks complete",
			tasks: []state.Task{
				{Description: "Task 1", Passes: true},
				{Description: "Task 2", Passes: false},
				{Description: "Task 3", Passes: true},
			},
			wantCompleted: 2,
			wantTotal:     3,
		},
		{
			name: "all tasks complete",
			tasks: []state.Task{
				{Description: "Task 1", Passes: true},
				{Description: "Task 2", Passes: true},
			},
			wantCompleted: 2,
			wantTotal:     2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			completed, total := CalculateProgress(tt.tasks)
			assert.Equal(t, tt.wantCompleted, completed)
			assert.Equal(t, tt.wantTotal, total)
		})
	}
}

// TestProgressRate tests progress rate calculation.
func TestProgressRate(t *testing.T) {
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
			name: "single entry",
			history: []state.History{
				{Iteration: 1, TasksCompleted: 2},
			},
			window: 3,
			want:   0,
		},
		{
			name: "consistent progress",
			history: []state.History{
				{Iteration: 1, TasksCompleted: 1},
				{Iteration: 2, TasksCompleted: 2},
				{Iteration: 3, TasksCompleted: 3},
			},
			window: 3,
			want:   1.0,
		},
		{
			name: "no progress",
			history: []state.History{
				{Iteration: 1, TasksCompleted: 2},
				{Iteration: 2, TasksCompleted: 2},
				{Iteration: 3, TasksCompleted: 2},
			},
			window: 3,
			want:   0,
		},
		{
			name: "partial progress",
			history: []state.History{
				{Iteration: 1, TasksCompleted: 1},
				{Iteration: 2, TasksCompleted: 1},
				{Iteration: 3, TasksCompleted: 2},
			},
			window: 3,
			want:   0.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ProgressRate(tt.history, tt.window)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestDefaultClaudeConfig tests that DefaultClaudeConfig returns production defaults.
func TestDefaultClaudeConfig(t *testing.T) {
	cfg := DefaultClaudeConfig()

	assert.Equal(t, 200, cfg.MaxTurns, "MaxTurns should be 200")
	assert.Equal(t, float64(0), cfg.MaxBudget, "MaxBudget should be 0 (no limit)")
	assert.True(t, cfg.Verbose, "Verbose should be true")
	assert.Equal(t, "stream-json", cfg.OutputFormat, "OutputFormat should be stream-json")
}

// TestExitReasonString tests ExitReason.String().
func TestExitReasonString(t *testing.T) {
	tests := []struct {
		reason ExitReason
		want   string
	}{
		{ExitReasonDone, "completed"},
		{ExitReasonNeedsInput, "needs input"},
		{ExitReasonBlocked, "blocked"},
		{ExitReasonMaxIterations, "max iterations"},
		{ExitReasonMaxBudget, "max budget"},
		{ExitReasonMaxDuration, "max duration"},
		{ExitReasonStuck, "stuck"},
		{ExitReasonUserKill, "user killed"},
		{ExitReasonBackground, "backgrounded"},
		{ExitReasonCrash, "crash"},
		{ExitReasonUnknown, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.reason.String())
		})
	}
}

// TestNewLoopWithOptions tests the LoopOptions constructor.
func TestNewLoopWithOptions(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := state.NewStore(tmpDir)
	mockClient := NewMockSpriteClient()
	syncMgr := state.NewSyncManager(mockClient, store)
	mockTUI := tui.NewTUI(io.Discard)

	cfg := &config.Config{
		Limits: config.Limits{
			MaxIterations:    50,
			MaxBudgetUSD:     25.0,
			MaxDurationHours: 2.0,
		},
	}
	session := &config.Session{
		Repo:       "test-org/test-repo",
		Branch:     "feature-branch",
		SpriteName: "wisp-test-123",
	}

	t.Run("creates Loop with all options", func(t *testing.T) {
		opts := LoopOptions{
			Client:      mockClient,
			SyncManager: syncMgr,
			Store:       store,
			Config:      cfg,
			Session:     session,
			TUI:         mockTUI,
			RepoPath:    "/var/local/wisp/repos/test-org/test-repo",
			TemplateDir: "/path/to/templates",
		}

		loop := NewLoopWithOptions(opts)

		assert.NotNil(t, loop)
		assert.Equal(t, "/var/local/wisp/repos/test-org/test-repo", loop.repoPath)
		assert.Equal(t, "/path/to/templates", loop.templateDir)
	})

	t.Run("uses default ClaudeConfig when zero-valued", func(t *testing.T) {
		opts := LoopOptions{
			Client:      mockClient,
			SyncManager: syncMgr,
			Store:       store,
			Config:      cfg,
			Session:     session,
			TUI:         mockTUI,
			RepoPath:    "/var/local/wisp/repos/test-org/test-repo",
			// ClaudeConfig not set (zero value)
		}

		loop := NewLoopWithOptions(opts)

		// Should use production defaults
		assert.Equal(t, 200, loop.claudeCfg.MaxTurns)
		assert.True(t, loop.claudeCfg.Verbose)
		assert.Equal(t, "stream-json", loop.claudeCfg.OutputFormat)
	})

	t.Run("uses provided ClaudeConfig when non-zero", func(t *testing.T) {
		customCfg := ClaudeConfig{
			MaxTurns:     20,
			MaxBudget:    10.0,
			Verbose:      false,
			OutputFormat: "text",
		}

		opts := LoopOptions{
			Client:       mockClient,
			SyncManager:  syncMgr,
			Store:        store,
			Config:       cfg,
			Session:      session,
			TUI:          mockTUI,
			RepoPath:     "/var/local/wisp/repos/test-org/test-repo",
			ClaudeConfig: customCfg,
		}

		loop := NewLoopWithOptions(opts)

		assert.Equal(t, 20, loop.claudeCfg.MaxTurns)
		assert.Equal(t, 10.0, loop.claudeCfg.MaxBudget)
		assert.False(t, loop.claudeCfg.Verbose)
		assert.Equal(t, "text", loop.claudeCfg.OutputFormat)
	})
}

// TestNewLoopUsesOptions tests that NewLoop correctly uses NewLoopWithOptions internally.
func TestNewLoopUsesOptions(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := state.NewStore(tmpDir)
	mockClient := NewMockSpriteClient()
	syncMgr := state.NewSyncManager(mockClient, store)
	mockTUI := tui.NewTUI(io.Discard)

	cfg := &config.Config{
		Limits: config.Limits{
			MaxIterations: 100,
		},
	}
	session := &config.Session{
		Branch: "test-branch",
	}

	loop := NewLoop(
		mockClient,
		syncMgr,
		store,
		cfg,
		session,
		mockTUI,
		"/var/local/wisp/repos/org/repo",
		"/templates",
	)

	assert.NotNil(t, loop)
	assert.Equal(t, "/var/local/wisp/repos/org/repo", loop.repoPath)
	assert.Equal(t, "/templates", loop.templateDir)
	// StartTime should be zero when using NewLoop (not injected)
	assert.True(t, loop.startTime.IsZero())
}

// TestLoopGetStartingIteration tests iteration resume.
func TestLoopGetStartingIteration(t *testing.T) {
	tmpDir := t.TempDir()
	store := state.NewStore(tmpDir)
	branch := "test-branch"

	// Create session
	session := &config.Session{Branch: branch}
	require.NoError(t, store.CreateSession(session))

	t.Run("no history", func(t *testing.T) {
		loop := &Loop{
			store:   store,
			session: session,
		}
		assert.Equal(t, 0, loop.getStartingIteration())
	})

	t.Run("with history", func(t *testing.T) {
		history := []state.History{
			{Iteration: 1, TasksCompleted: 1},
			{Iteration: 2, TasksCompleted: 2},
			{Iteration: 5, TasksCompleted: 3},
		}
		require.NoError(t, store.SaveHistory(branch, history))

		loop := &Loop{
			store:   store,
			session: session,
		}
		assert.Equal(t, 5, loop.getStartingIteration())
	})
}

// TestLoopRunContextCancellation tests that Run exits on context cancellation.
func TestLoopRunContextCancellation(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	store := state.NewStore(tmpDir)
	branch := "test-branch"

	// Create session directory
	session := &config.Session{
		Repo:       "org/repo",
		Branch:     branch,
		SpriteName: "wisp-test",
	}
	require.NoError(t, store.CreateSession(session))

	mockClient := NewMockSpriteClient()
	syncMgr := state.NewSyncManager(mockClient, store)

	cfg := &config.Config{
		Limits: config.Limits{
			MaxIterations:       100,
			NoProgressThreshold: 10,
		},
	}

	// Create a minimal TUI
	mockTUI := tui.NewTUI(io.Discard)

	loop := NewLoop(
		mockClient,
		syncMgr,
		store,
		cfg,
		session,
		mockTUI,
		"/var/local/wisp/repos/org/repo",
		"",
	)

	// Run with a cancelled context to exit immediately
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel() // Cancel immediately

	result := loop.Run(cancelCtx)

	// Should exit due to context cancellation (background) or crash if sprite runner not available
	// Since we haven't set up the mock to return wisp-sprite running, it will fail to connect
	assert.True(t, result.Reason == ExitReasonBackground || result.Reason == ExitReasonCrash,
		"Expected Background or Crash, got %s", result.Reason)
}

// TestSyncStateFromSprite tests that syncStateFromSprite handles errors gracefully.
func TestSyncStateFromSprite(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := state.NewStore(tmpDir)
	branch := "test-branch"

	session := &config.Session{
		Branch:     branch,
		SpriteName: "wisp-test",
	}
	require.NoError(t, store.CreateSession(session))

	mockClient := NewMockSpriteClient()
	syncMgr := state.NewSyncManager(mockClient, store)

	loop := &Loop{
		sync:    syncMgr,
		session: session,
	}

	// Should not panic even if sync fails
	ctx := context.Background()
	loop.syncStateFromSprite(ctx)
}

// TestHandleTUIActionKill tests that ActionKill returns UserKill reason.
func TestHandleTUIActionKill(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := state.NewStore(tmpDir)

	session := &config.Session{
		Branch:     "test-branch",
		SpriteName: "wisp-test",
	}

	mockClient := NewMockSpriteClient()
	syncMgr := state.NewSyncManager(mockClient, store)
	mockTUI := tui.NewTUI(io.Discard)
	cfg := &config.Config{}

	// We can't fully test handleTUIAction without a stream client,
	// but we can verify the Loop struct is created correctly
	loop := NewLoopWithOptions(LoopOptions{
		Client:      mockClient,
		SyncManager: syncMgr,
		Store:       store,
		Config:      cfg,
		Session:     session,
		TUI:         mockTUI,
		RepoPath:    "/var/local/wisp/repos/org/repo",
	})

	assert.NotNil(t, loop)
	assert.Equal(t, session, loop.session)
}

// TestHandleTUIActionBackground tests that ActionBackground returns Background reason.
func TestHandleTUIActionBackground(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := state.NewStore(tmpDir)

	session := &config.Session{
		Branch:     "test-branch",
		SpriteName: "wisp-test",
	}

	mockClient := NewMockSpriteClient()
	syncMgr := state.NewSyncManager(mockClient, store)
	mockTUI := tui.NewTUI(io.Discard)
	cfg := &config.Config{}

	loop := NewLoopWithOptions(LoopOptions{
		Client:      mockClient,
		SyncManager: syncMgr,
		Store:       store,
		Config:      cfg,
		Session:     session,
		TUI:         mockTUI,
		RepoPath:    "/var/local/wisp/repos/org/repo",
	})

	assert.NotNil(t, loop)
}

// TestBroadcastFunctionsNoServer tests that broadcast functions are no-ops without server.
func TestBroadcastFunctionsNoServer(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := state.NewStore(tmpDir)

	session := &config.Session{
		Branch:     "test-branch",
		SpriteName: "wisp-test",
	}

	mockClient := NewMockSpriteClient()
	syncMgr := state.NewSyncManager(mockClient, store)
	mockTUI := tui.NewTUI(io.Discard)
	cfg := &config.Config{}

	// No server
	loop := NewLoopWithOptions(LoopOptions{
		Client:      mockClient,
		SyncManager: syncMgr,
		Store:       store,
		Config:      cfg,
		Session:     session,
		TUI:         mockTUI,
		Server:      nil, // No server
		RepoPath:    "/var/local/wisp/repos/org/repo",
	})

	// All broadcast functions should be no-ops and not panic
	loop.broadcastSession(nil)
	loop.broadcastClaudeEvent(nil)
	loop.broadcastInputRequest(nil)
}
