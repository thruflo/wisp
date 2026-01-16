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
			name:  "assistant message",
			input: `{"type":"assistant","content":"Hello, world!"}`,
			want:  "Hello, world!",
		},
		{
			name:  "tool use",
			input: `{"type":"tool_use","tool":"Bash","message":"Running command"}`,
			want:  "[Bash] Running command",
		},
		{
			name:  "tool result short",
			input: `{"type":"tool_result","result":"Success"}`,
			want:  "Success",
		},
		{
			name:  "tool result long",
			input: `{"type":"tool_result","result":"` + strings.Repeat("x", 300) + `"}`,
			want:  strings.Repeat("x", 200) + "...",
		},
		{
			name:  "error message",
			input: `{"type":"error","message":"Something went wrong"}`,
			want:  "ERROR: Something went wrong",
		},
		{
			name:  "unknown type with content",
			input: `{"type":"other","content":"Some content"}`,
			want:  "Some content",
		},
		{
			name:  "unknown type with message",
			input: `{"type":"other","message":"Some message"}`,
			want:  "Some message",
		},
		{
			name:  "whitespace input",
			input: "   ",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := loop.parseStreamJSON(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestBuildClaudeArgs tests Claude command argument building.
func TestBuildClaudeArgs(t *testing.T) {
	loop := &Loop{
		cfg: &config.Config{
			Limits: config.Limits{
				MaxBudgetUSD: 15.50,
			},
		},
		repoPath: "/home/sprite/org/repo",
		wispPath: "/home/sprite/org/repo/.wisp",
	}

	args := loop.buildClaudeArgs()

	// Check required flags
	assert.Contains(t, args, "claude")
	assert.Contains(t, args, "--dangerously-skip-permissions")
	assert.Contains(t, args, "--output-format")
	assert.Contains(t, args, "stream-json")
	assert.Contains(t, args, "--max-turns")
	assert.Contains(t, args, "100")

	// Check budget flag
	assert.Contains(t, args, "--max-budget-usd")
	assert.Contains(t, args, "15.50")

	// Check prompt file references
	var foundIteratePrompt bool
	var foundContextFile bool
	for i, arg := range args {
		if arg == "-p" && i+1 < len(args) {
			if args[i+1] == "$(cat /home/sprite/org/repo/.wisp/iterate.md)" {
				foundIteratePrompt = true
			}
		}
		if arg == "--append-system-prompt-file" && i+1 < len(args) {
			if args[i+1] == "/home/sprite/org/repo/.wisp/context.md" {
				foundContextFile = true
			}
		}
	}
	assert.True(t, foundIteratePrompt, "should include iterate.md prompt")
	assert.True(t, foundContextFile, "should include context.md")
}

// TestBuildClaudeArgsNoBudget tests args without budget limit.
func TestBuildClaudeArgsNoBudget(t *testing.T) {
	loop := &Loop{
		cfg: &config.Config{
			Limits: config.Limits{
				MaxBudgetUSD: 0, // No budget limit
			},
		},
		repoPath: "/home/sprite/org/repo",
		wispPath: "/home/sprite/org/repo/.wisp",
	}

	args := loop.buildClaudeArgs()

	// Should not contain budget flag
	for _, arg := range args {
		assert.NotEqual(t, "--max-budget-usd", arg)
	}
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
	mockClient.SetFile("/home/sprite/org/repo/.wisp/state.json", stateData)

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
		"/home/sprite/org/repo",
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
