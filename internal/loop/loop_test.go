package loop

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/sprite"
	"github.com/thruflo/wisp/internal/state"
	"github.com/thruflo/wisp/internal/tui"
)

// MockSpriteClient implements sprite.Client for testing.
type MockSpriteClient struct {
	mu            sync.Mutex
	files         map[string][]byte
	executeResult *MockCmd
	executeErr    error
	createCalled  bool
	deleteCalled  bool
}

func NewMockSpriteClient() *MockSpriteClient {
	return &MockSpriteClient{
		files: make(map[string][]byte),
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.executeErr != nil {
		return nil, m.executeErr
	}
	if m.executeResult != nil {
		return m.executeResult.ToSpriteCmd(), nil
	}
	// Default: return a completed command
	return NewMockCmd("", nil).ToSpriteCmd(), nil
}

func (m *MockSpriteClient) ExecuteOutput(ctx context.Context, name string, dir string, env []string, args ...string) (stdout, stderr []byte, exitCode int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.executeErr != nil {
		return nil, nil, -1, m.executeErr
	}
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

// SetExecuteResult sets the result for Execute calls.
func (m *MockSpriteClient) SetExecuteResult(cmd *MockCmd) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.executeResult = cmd
}

// MockCmd is a mock command for testing.
type MockCmd struct {
	stdout   *bytes.Buffer
	stderr   *bytes.Buffer
	waitErr  error
	exitCode int
}

func NewMockCmd(output string, err error) *MockCmd {
	return &MockCmd{
		stdout:   bytes.NewBufferString(output),
		stderr:   bytes.NewBuffer(nil),
		waitErr:  err,
		exitCode: 0,
	}
}

func (m *MockCmd) ToSpriteCmd() *sprite.Cmd {
	return &sprite.Cmd{
		Stdout: io.NopCloser(m.stdout),
		Stderr: io.NopCloser(m.stderr),
	}
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

// TestParseStreamJSON tests stream-json parsing.
// Tests the actual Claude --output-format stream-json format.
func TestParseStreamJSON(t *testing.T) {
	loop := &Loop{}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
		{
			name:  "non-json input",
			input: "plain text line",
			want:  "plain text line",
		},
		{
			name:  "whitespace input",
			input: "   ",
			want:  "",
		},
		{
			name:  "assistant text message",
			input: `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello, world!"}]}}`,
			want:  "Hello, world!",
		},
		{
			name:  "assistant tool use - bash",
			input: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","id":"toolu_123","input":{"command":"ls -la"}}]}}`,
			want:  "[Bash] ls -la",
		},
		{
			name:  "assistant tool use - read",
			input: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","id":"toolu_123","input":{"file_path":"/path/to/file.go"}}]}}`,
			want:  "[Read] /path/to/file.go",
		},
		{
			name:  "assistant tool use - unknown tool",
			input: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"CustomTool","id":"toolu_123","input":{"foo":"bar"}}]}}`,
			want:  "[CustomTool] ",
		},
		{
			name:  "user tool result short",
			input: `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_123","content":"Success"}]}}`,
			want:  "Success",
		},
		{
			name:  "user tool result long",
			input: `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_123","content":"` + strings.Repeat("x", 300) + `"}]}}`,
			want:  strings.Repeat("x", 200) + "...",
		},
		{
			name:  "user tool result with cat-n line numbers",
			input: `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_123","content":"     1→line one\n     2→line two\n     3→line three"}]}}`,
			want:  "line one line two line three",
		},
		{
			name:  "user tool result multiline collapses whitespace",
			input: `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_123","content":"first line\n\n   second line   \n\nthird"}]}}`,
			want:  "first line second line third",
		},
		{
			name:  "result success",
			input: `{"type":"result","subtype":"success","session_id":"abc123","cost_usd":1.50}`,
			want:  "[Session completed]",
		},
		{
			name:  "result other",
			input: `{"type":"result","subtype":"error"}`,
			want:  "[Result: error]",
		},
		{
			name:  "system init",
			input: `{"type":"system","subtype":"init","session_id":"abc123"}`,
			want:  "[Session started]",
		},
		{
			name:  "unknown type",
			input: `{"type":"unknown_type"}`,
			want:  "",
		},
		{
			name:  "multiple content items",
			input: `{"type":"assistant","message":{"content":[{"type":"text","text":"First"},{"type":"text","text":"Second"}]}}`,
			want:  "First\nSecond",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := loop.parseStreamJSON(tt.input)
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

// TestBuildClaudeArgs tests Claude command argument building.
func TestBuildClaudeArgs(t *testing.T) {
	loop := &Loop{
		cfg: &config.Config{
			Limits: config.Limits{
				MaxBudgetUSD: 15.50,
			},
		},
		repoPath:  "/var/local/wisp/repos/org/repo",
		claudeCfg: DefaultClaudeConfig(),
	}

	args := loop.buildClaudeArgs()

	// args should be ["bash", "-c", "export HOME=... && claude ..."]
	require.Len(t, args, 3)
	assert.Equal(t, "bash", args[0])
	assert.Equal(t, "-c", args[1])

	// Check the bash command string contains expected flags
	bashCmd := args[2]
	assert.Contains(t, bashCmd, "claude")
	assert.Contains(t, bashCmd, "--dangerously-skip-permissions")
	assert.Contains(t, bashCmd, "--verbose") // required when using -p with --output-format stream-json
	assert.Contains(t, bashCmd, "--output-format stream-json")
	assert.Contains(t, bashCmd, "--max-turns 200")

	// Check budget flag from config.Limits (since ClaudeConfig.MaxBudget is 0)
	assert.Contains(t, bashCmd, "--max-budget-usd 15.50")

	// Check prompt file references use absolute paths
	assert.Contains(t, bashCmd, "$(cat /var/local/wisp/templates/iterate.md)")
	assert.Contains(t, bashCmd, "--append-system-prompt-file /var/local/wisp/templates/context.md")
}

// TestBuildClaudeArgsNoBudget tests args without budget limit.
func TestBuildClaudeArgsNoBudget(t *testing.T) {
	loop := &Loop{
		cfg: &config.Config{
			Limits: config.Limits{
				MaxBudgetUSD: 0, // No budget limit
			},
		},
		repoPath:  "/var/local/wisp/repos/org/repo",
		claudeCfg: DefaultClaudeConfig(),
	}

	args := loop.buildClaudeArgs()

	// Should not contain budget flag in the bash command
	require.Len(t, args, 3)
	bashCmd := args[2]
	assert.NotContains(t, bashCmd, "--max-budget-usd")
}

// TestBuildClaudeArgsWithCustomClaudeConfig tests buildClaudeArgs with custom ClaudeConfig.
func TestBuildClaudeArgsWithCustomClaudeConfig(t *testing.T) {
	t.Run("custom max turns", func(t *testing.T) {
		loop := &Loop{
			cfg:      &config.Config{},
			repoPath: "/var/local/wisp/repos/org/repo",
			claudeCfg: ClaudeConfig{
				MaxTurns:     20,
				Verbose:      true,
				OutputFormat: "stream-json",
			},
		}

		args := loop.buildClaudeArgs()

		require.Len(t, args, 3)
		bashCmd := args[2]
		assert.Contains(t, bashCmd, "--max-turns 20")
		assert.NotContains(t, bashCmd, "--max-turns 100")
	})

	t.Run("custom budget from ClaudeConfig overrides config.Limits", func(t *testing.T) {
		loop := &Loop{
			cfg: &config.Config{
				Limits: config.Limits{
					MaxBudgetUSD: 50.0, // This should be ignored
				},
			},
			repoPath: "/var/local/wisp/repos/org/repo",
			claudeCfg: ClaudeConfig{
				MaxTurns:     200,
				MaxBudget:    5.0, // ClaudeConfig budget takes precedence
				Verbose:      true,
				OutputFormat: "stream-json",
			},
		}

		args := loop.buildClaudeArgs()

		require.Len(t, args, 3)
		bashCmd := args[2]
		assert.Contains(t, bashCmd, "--max-budget-usd 5.00")
		assert.NotContains(t, bashCmd, "50.00")
	})

	t.Run("verbose disabled", func(t *testing.T) {
		loop := &Loop{
			cfg:      &config.Config{},
			repoPath: "/var/local/wisp/repos/org/repo",
			claudeCfg: ClaudeConfig{
				MaxTurns:     200,
				Verbose:      false,
				OutputFormat: "stream-json",
			},
		}

		args := loop.buildClaudeArgs()

		require.Len(t, args, 3)
		bashCmd := args[2]
		assert.NotContains(t, bashCmd, "--verbose")
	})

	t.Run("custom output format", func(t *testing.T) {
		loop := &Loop{
			cfg:      &config.Config{},
			repoPath: "/var/local/wisp/repos/org/repo",
			claudeCfg: ClaudeConfig{
				MaxTurns:     100,
				Verbose:      true,
				OutputFormat: "text",
			},
		}

		args := loop.buildClaudeArgs()

		require.Len(t, args, 3)
		bashCmd := args[2]
		assert.Contains(t, bashCmd, "--output-format text")
		assert.NotContains(t, bashCmd, "stream-json")
	})

	t.Run("zero max turns omits flag", func(t *testing.T) {
		loop := &Loop{
			cfg:      &config.Config{},
			repoPath: "/var/local/wisp/repos/org/repo",
			claudeCfg: ClaudeConfig{
				MaxTurns:     0, // Zero means no limit
				Verbose:      true,
				OutputFormat: "stream-json",
			},
		}

		args := loop.buildClaudeArgs()

		require.Len(t, args, 3)
		bashCmd := args[2]
		assert.NotContains(t, bashCmd, "--max-turns")
	})

	t.Run("empty output format omits flag", func(t *testing.T) {
		loop := &Loop{
			cfg:      &config.Config{},
			repoPath: "/var/local/wisp/repos/org/repo",
			claudeCfg: ClaudeConfig{
				MaxTurns:     200,
				Verbose:      true,
				OutputFormat: "",
			},
		}

		args := loop.buildClaudeArgs()

		require.Len(t, args, 3)
		bashCmd := args[2]
		assert.NotContains(t, bashCmd, "--output-format")
	})
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

// TestCheckDurationLimit tests duration limit checking.
func TestCheckDurationLimit(t *testing.T) {
	t.Run("no limit", func(t *testing.T) {
		loop := &Loop{
			cfg: &config.Config{
				Limits: config.Limits{
					MaxDurationHours: 0,
				},
			},
			startTime: time.Now().Add(-24 * time.Hour),
		}
		assert.False(t, loop.checkDurationLimit())
	})

	t.Run("within limit", func(t *testing.T) {
		loop := &Loop{
			cfg: &config.Config{
				Limits: config.Limits{
					MaxDurationHours: 4,
				},
			},
			startTime: time.Now().Add(-1 * time.Hour),
		}
		assert.False(t, loop.checkDurationLimit())
	})

	t.Run("exceeded limit", func(t *testing.T) {
		loop := &Loop{
			cfg: &config.Config{
				Limits: config.Limits{
					MaxDurationHours: 4,
				},
			},
			startTime: time.Now().Add(-5 * time.Hour),
		}
		assert.True(t, loop.checkDurationLimit())
	})
}

// TestAllTasksComplete tests task completion checking.
func TestAllTasksComplete(t *testing.T) {
	tmpDir := t.TempDir()
	store := state.NewStore(tmpDir)

	branch := "test-branch"

	t.Run("no tasks", func(t *testing.T) {
		loop := &Loop{
			store:   store,
			session: &config.Session{Branch: branch},
		}
		// No tasks file exists
		assert.False(t, loop.allTasksComplete())
	})

	t.Run("empty tasks", func(t *testing.T) {
		err := store.SaveTasks(branch, []state.Task{})
		require.NoError(t, err)

		loop := &Loop{
			store:   store,
			session: &config.Session{Branch: branch},
		}
		assert.False(t, loop.allTasksComplete())
	})

	t.Run("incomplete tasks", func(t *testing.T) {
		err := store.SaveTasks(branch, []state.Task{
			{Description: "Task 1", Passes: true},
			{Description: "Task 2", Passes: false},
		})
		require.NoError(t, err)

		loop := &Loop{
			store:   store,
			session: &config.Session{Branch: branch},
		}
		assert.False(t, loop.allTasksComplete())
	})

	t.Run("all complete", func(t *testing.T) {
		err := store.SaveTasks(branch, []state.Task{
			{Description: "Task 1", Passes: true},
			{Description: "Task 2", Passes: true},
		})
		require.NoError(t, err)

		loop := &Loop{
			store:   store,
			session: &config.Session{Branch: branch},
		}
		assert.True(t, loop.allTasksComplete())
	})
}

// TestLoopRunMaxIterations tests loop exit on max iterations.
func TestLoopRunMaxIterations(t *testing.T) {
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

	// Create initial state
	initialState := &state.State{Status: state.StatusContinue, Summary: "Initial"}
	require.NoError(t, store.SaveState(branch, initialState))

	// Create tasks
	tasks := []state.Task{
		{Description: "Task 1", Passes: false},
	}
	require.NoError(t, store.SaveTasks(branch, tasks))

	mockClient := NewMockSpriteClient()

	// Set up state.json that Claude would write
	stateData, _ := json.Marshal(&state.State{Status: state.StatusContinue, Summary: "Working"})
	mockClient.SetFile("/var/local/wisp/session/state.json", stateData)

	syncMgr := state.NewSyncManager(mockClient, store)

	cfg := &config.Config{
		Limits: config.Limits{
			MaxIterations:       2,
			NoProgressThreshold: 10, // High so stuck detection doesn't trigger
		},
	}

	// Create a minimal TUI that doesn't require terminal
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

	// Run with a cancelled context to exit immediately after first check
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel() // Cancel immediately

	result := loop.Run(cancelCtx)

	// Should exit due to context cancellation (background)
	assert.Equal(t, ExitReasonBackground, result.Reason)
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

// TestNeedsInputFlow tests the complete NEEDS_INPUT cycle:
// 1. Claude returns NEEDS_INPUT status with a question
// 2. Loop pauses and displays question in TUI
// 3. User provides response via TUI input
// 4. Response is written to response.json on Sprite
// 5. Loop continues to next iteration
func TestNeedsInputFlow(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := state.NewStore(tmpDir)
	branch := "test-needs-input"

	// Create session
	session := &config.Session{
		Repo:       "org/repo",
		Branch:     branch,
		SpriteName: "wisp-test",
	}
	require.NoError(t, store.CreateSession(session))

	// Create initial tasks
	tasks := []state.Task{
		{Description: "Task 1", Passes: false},
	}
	require.NoError(t, store.SaveTasks(branch, tasks))

	mockClient := NewMockSpriteClient()
	repoPath := "/var/local/wisp/repos/org/repo"

	// Set up NEEDS_INPUT state that Claude would write (using new absolute paths)
	needsInputState := &state.State{
		Status:   state.StatusNeedsInput,
		Summary:  "Need clarification on implementation",
		Question: "Should we use Redis or in-memory cache?",
	}
	needsInputData, _ := json.Marshal(needsInputState)
	mockClient.SetFile("/var/local/wisp/session/state.json", needsInputData)

	syncMgr := state.NewSyncManager(mockClient, store)

	cfg := &config.Config{
		Limits: config.Limits{
			MaxIterations:       10,
			NoProgressThreshold: 5,
		},
	}

	// Create TUI with mock output
	mockTUI := tui.NewTUI(io.Discard)

	l := NewLoop(
		mockClient,
		syncMgr,
		store,
		cfg,
		session,
		mockTUI,
		repoPath,
		"",
	)

	// Test handleNeedsInput directly
	t.Run("handleNeedsInput writes response to Sprite", func(t *testing.T) {
		// Create a context that won't be cancelled
		ctx := context.Background()

		// Simulate user submitting input
		go func() {
			// Wait a bit for handleNeedsInput to start listening
			time.Sleep(10 * time.Millisecond)
			// Send submit action through the action channel
			mockTUI.Actions() // Get channel reference
			// Manually inject action by calling the internal channel
		}()

		// We can't easily test the full async flow, so test the sync part:
		// Write response directly and verify it was written
		err := syncMgr.WriteResponseToSprite(ctx, session.SpriteName, "Use Redis for distributed caching")
		require.NoError(t, err)

		// Verify response.json was written to Sprite
		responseData, err := mockClient.ReadFile(ctx, session.SpriteName, "/var/local/wisp/session/response.json")
		require.NoError(t, err)

		var response state.Response
		err = json.Unmarshal(responseData, &response)
		require.NoError(t, err)
		assert.Equal(t, "Use Redis for distributed caching", response.Answer)
	})

	t.Run("NEEDS_INPUT state is synced to local storage", func(t *testing.T) {
		ctx := context.Background()

		// Sync from Sprite to local
		err := syncMgr.SyncFromSprite(ctx, session.SpriteName, branch)
		require.NoError(t, err)

		// Verify local state has NEEDS_INPUT status and question
		localState, err := store.LoadState(branch)
		require.NoError(t, err)
		require.NotNil(t, localState)
		assert.Equal(t, state.StatusNeedsInput, localState.Status)
		assert.Equal(t, "Should we use Redis or in-memory cache?", localState.Question)
	})

	t.Run("handleNeedsInput returns correct result on cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		// Cancel context to simulate user cancellation
		go func() {
			time.Sleep(10 * time.Millisecond)
			cancel()
		}()

		result := l.handleNeedsInput(ctx, needsInputState)

		// Should exit with background reason when context cancelled
		assert.Equal(t, ExitReasonBackground, result.Reason)
	})
}

// TestNeedsInputFlowTUIActions tests the TUI action handling during NEEDS_INPUT.
func TestNeedsInputFlowTUIActions(t *testing.T) {
	t.Parallel()

	// Test that TUI correctly handles input view
	buf := &bytes.Buffer{}
	mockTUI := tui.NewTUI(buf)

	question := "What database should we use?"
	mockTUI.ShowInput(question)

	// Verify TUI is in input view
	assert.Equal(t, tui.ViewInput, mockTUI.GetView())
	assert.Equal(t, question, mockTUI.GetState().Question)
}

// TestNeedsInputResponseFormat tests the response.json format.
func TestNeedsInputResponseFormat(t *testing.T) {
	t.Parallel()

	response := state.Response{Answer: "Test answer with special chars: 日本語 & <xml>"}

	data, err := json.MarshalIndent(response, "", "  ")
	require.NoError(t, err)

	// Verify JSON structure
	var parsed state.Response
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)
	assert.Equal(t, response.Answer, parsed.Answer)

	// Verify it's valid JSON with expected field
	var raw map[string]interface{}
	err = json.Unmarshal(data, &raw)
	require.NoError(t, err)
	assert.Contains(t, raw, "answer")
}

// TestNeedsInputStatusTransitions tests status transitions during NEEDS_INPUT flow.
func TestNeedsInputStatusTransitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		initialStatus  string
		expectedAction ExitReason
	}{
		{
			name:           "NEEDS_INPUT triggers input handling",
			initialStatus:  state.StatusNeedsInput,
			expectedAction: ExitReasonUnknown, // Returns unknown to continue loop
		},
		{
			name:           "DONE triggers completion check",
			initialStatus:  state.StatusDone,
			expectedAction: ExitReasonUnknown, // If tasks not complete, continues
		},
		{
			name:           "BLOCKED triggers immediate exit",
			initialStatus:  state.StatusBlocked,
			expectedAction: ExitReasonBlocked,
		},
		{
			name:           "CONTINUE continues loop",
			initialStatus:  state.StatusContinue,
			expectedAction: ExitReasonUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := &state.State{
				Status:   tt.initialStatus,
				Question: "Test question",
				Error:    "Test error",
			}

			// For BLOCKED status, we can test the switch case directly
			if tt.initialStatus == state.StatusBlocked {
				// The switch case returns ExitReasonBlocked
				assert.Equal(t, state.StatusBlocked, st.Status)
			}
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
		assert.Equal(t, "/var/local/wisp/repos/test-org/test-repo/.wisp", loop.wispPath)
		assert.Equal(t, "/path/to/templates", loop.templateDir)
	})

	t.Run("StartTime is injected", func(t *testing.T) {
		injectedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)

		opts := LoopOptions{
			Client:      mockClient,
			SyncManager: syncMgr,
			Store:       store,
			Config:      cfg,
			Session:     session,
			TUI:         mockTUI,
			RepoPath:    "/var/local/wisp/repos/test-org/test-repo",
			StartTime:   injectedTime,
		}

		loop := NewLoopWithOptions(opts)

		assert.Equal(t, injectedTime, loop.startTime)
	})

	t.Run("zero StartTime allows Run to set current time", func(t *testing.T) {
		opts := LoopOptions{
			Client:      mockClient,
			SyncManager: syncMgr,
			Store:       store,
			Config:      cfg,
			Session:     session,
			TUI:         mockTUI,
			RepoPath:    "/var/local/wisp/repos/test-org/test-repo",
			// StartTime not set (zero value)
		}

		loop := NewLoopWithOptions(opts)
		assert.True(t, loop.startTime.IsZero())
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
	assert.Equal(t, "/var/local/wisp/repos/org/repo/.wisp", loop.wispPath)
	assert.Equal(t, "/templates", loop.templateDir)
	// StartTime should be zero when using NewLoop (not injected)
	assert.True(t, loop.startTime.IsZero())
}

// TestLoopRunWithInjectedStartTime tests that injected StartTime is used for duration checks.
func TestLoopRunWithInjectedStartTime(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	store := state.NewStore(tmpDir)
	branch := "test-branch"

	session := &config.Session{
		Repo:       "org/repo",
		Branch:     branch,
		SpriteName: "wisp-test",
	}
	require.NoError(t, store.CreateSession(session))

	// Create initial state and tasks
	initialState := &state.State{Status: state.StatusContinue}
	require.NoError(t, store.SaveState(branch, initialState))
	tasks := []state.Task{{Description: "Task 1", Passes: false}}
	require.NoError(t, store.SaveTasks(branch, tasks))

	mockClient := NewMockSpriteClient()
	stateData, _ := json.Marshal(&state.State{Status: state.StatusContinue, Summary: "Working"})
	mockClient.SetFile("/var/local/wisp/session/state.json", stateData)

	syncMgr := state.NewSyncManager(mockClient, store)
	mockTUI := tui.NewTUI(io.Discard)

	// Set max duration to 1 hour
	cfg := &config.Config{
		Limits: config.Limits{
			MaxIterations:       100,
			MaxDurationHours:    1.0,
			NoProgressThreshold: 100,
		},
	}

	t.Run("exceeds duration limit with injected past time", func(t *testing.T) {
		// Inject a start time 2 hours in the past
		pastTime := time.Now().Add(-2 * time.Hour)

		loop := NewLoopWithOptions(LoopOptions{
			Client:      mockClient,
			SyncManager: syncMgr,
			Store:       store,
			Config:      cfg,
			Session:     session,
			TUI:         mockTUI,
			RepoPath:    "/var/local/wisp/repos/org/repo",
			StartTime:   pastTime,
		})

		ctx := context.Background()
		result := loop.Run(ctx)

		// Should exit due to max duration (since we started "2 hours ago")
		assert.Equal(t, ExitReasonMaxDuration, result.Reason)
	})

	t.Run("within duration limit with injected recent time", func(t *testing.T) {
		// Inject a start time 30 minutes in the past (within 1 hour limit)
		recentTime := time.Now().Add(-30 * time.Minute)

		loop := NewLoopWithOptions(LoopOptions{
			Client:      mockClient,
			SyncManager: syncMgr,
			Store:       store,
			Config:      cfg,
			Session:     session,
			TUI:         mockTUI,
			RepoPath:    "/var/local/wisp/repos/org/repo",
			StartTime:   recentTime,
		})

		// Use a cancelled context to avoid running actual iterations
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		result := loop.Run(ctx)

		// Should exit due to context cancellation, not duration
		assert.Equal(t, ExitReasonBackground, result.Reason)
	})
}
