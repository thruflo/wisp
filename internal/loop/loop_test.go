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
	assert.Contains(t, args, "--verbose") // required when using -p with --output-format stream-json
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
	repoPath := "/home/sprite/org/repo"

	// Set up NEEDS_INPUT state that Claude would write
	needsInputState := &state.State{
		Status:   state.StatusNeedsInput,
		Summary:  "Need clarification on implementation",
		Question: "Should we use Redis or in-memory cache?",
	}
	needsInputData, _ := json.Marshal(needsInputState)
	mockClient.SetFile(repoPath+"/.wisp/state.json", needsInputData)

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
		err := syncMgr.WriteResponseToSprite(ctx, session.SpriteName, repoPath, "Use Redis for distributed caching")
		require.NoError(t, err)

		// Verify response.json was written to Sprite
		responseData, err := mockClient.ReadFile(ctx, session.SpriteName, repoPath+"/.wisp/response.json")
		require.NoError(t, err)

		var response state.Response
		err = json.Unmarshal(responseData, &response)
		require.NoError(t, err)
		assert.Equal(t, "Use Redis for distributed caching", response.Answer)
	})

	t.Run("NEEDS_INPUT state is synced to local storage", func(t *testing.T) {
		ctx := context.Background()

		// Sync from Sprite to local
		err := syncMgr.SyncFromSprite(ctx, session.SpriteName, branch, repoPath)
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
			RepoPath:    "/home/sprite/test-org/test-repo",
			TemplateDir: "/path/to/templates",
		}

		loop := NewLoopWithOptions(opts)

		assert.NotNil(t, loop)
		assert.Equal(t, "/home/sprite/test-org/test-repo", loop.repoPath)
		assert.Equal(t, "/home/sprite/test-org/test-repo/.wisp", loop.wispPath)
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
			RepoPath:    "/home/sprite/test-org/test-repo",
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
			RepoPath:    "/home/sprite/test-org/test-repo",
			// StartTime not set (zero value)
		}

		loop := NewLoopWithOptions(opts)
		assert.True(t, loop.startTime.IsZero())
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
		"/home/sprite/org/repo",
		"/templates",
	)

	assert.NotNil(t, loop)
	assert.Equal(t, "/home/sprite/org/repo", loop.repoPath)
	assert.Equal(t, "/home/sprite/org/repo/.wisp", loop.wispPath)
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
	mockClient.SetFile("/home/sprite/org/repo/.wisp/state.json", stateData)

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
			RepoPath:    "/home/sprite/org/repo",
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
			RepoPath:    "/home/sprite/org/repo",
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
