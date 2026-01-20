package spriteloop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thruflo/wisp/internal/state"
	"github.com/thruflo/wisp/internal/stream"
)

func TestExitReasonString(t *testing.T) {
	tests := []struct {
		reason   ExitReason
		expected string
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
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.reason.String(); got != tt.expected {
				t.Errorf("ExitReason.String() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestDefaultClaudeConfig(t *testing.T) {
	cfg := DefaultClaudeConfig()

	if cfg.MaxTurns != 200 {
		t.Errorf("MaxTurns = %d, want 200", cfg.MaxTurns)
	}
	if cfg.MaxBudget != 0 {
		t.Errorf("MaxBudget = %f, want 0", cfg.MaxBudget)
	}
	if !cfg.Verbose {
		t.Error("Verbose should be true by default")
	}
	if cfg.OutputFormat != "stream-json" {
		t.Errorf("OutputFormat = %q, want %q", cfg.OutputFormat, "stream-json")
	}
}

func TestDefaultLimits(t *testing.T) {
	limits := DefaultLimits()

	if limits.MaxIterations != 100 {
		t.Errorf("MaxIterations = %d, want 100", limits.MaxIterations)
	}
	if limits.MaxBudgetUSD != 20.0 {
		t.Errorf("MaxBudgetUSD = %f, want 20.0", limits.MaxBudgetUSD)
	}
	if limits.MaxDurationHours != 8.0 {
		t.Errorf("MaxDurationHours = %f, want 8.0", limits.MaxDurationHours)
	}
	if limits.NoProgressThreshold != 5 {
		t.Errorf("NoProgressThreshold = %d, want 5", limits.NoProgressThreshold)
	}
}

func TestDetectStuck(t *testing.T) {
	tests := []struct {
		name      string
		history   []state.History
		threshold int
		expected  bool
	}{
		{
			name:      "no history",
			history:   nil,
			threshold: 3,
			expected:  false,
		},
		{
			name: "progress made",
			history: []state.History{
				{Iteration: 1, TasksCompleted: 0},
				{Iteration: 2, TasksCompleted: 1},
				{Iteration: 3, TasksCompleted: 2},
			},
			threshold: 3,
			expected:  false,
		},
		{
			name: "stuck - no progress",
			history: []state.History{
				{Iteration: 1, TasksCompleted: 2},
				{Iteration: 2, TasksCompleted: 2},
				{Iteration: 3, TasksCompleted: 2},
			},
			threshold: 3,
			expected:  true,
		},
		{
			name: "not enough history for threshold",
			history: []state.History{
				{Iteration: 1, TasksCompleted: 2},
				{Iteration: 2, TasksCompleted: 2},
			},
			threshold: 3,
			expected:  false,
		},
		{
			name: "progress at start then stuck",
			history: []state.History{
				{Iteration: 1, TasksCompleted: 0},
				{Iteration: 2, TasksCompleted: 1},
				{Iteration: 3, TasksCompleted: 1},
				{Iteration: 4, TasksCompleted: 1},
				{Iteration: 5, TasksCompleted: 1},
			},
			threshold: 3,
			expected:  true,
		},
		{
			name: "recent progress after being stuck",
			history: []state.History{
				{Iteration: 1, TasksCompleted: 1},
				{Iteration: 2, TasksCompleted: 1},
				{Iteration: 3, TasksCompleted: 1},
				{Iteration: 4, TasksCompleted: 2},
				{Iteration: 5, TasksCompleted: 2},
			},
			threshold: 3,
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := detectStuck(tt.history, tt.threshold); got != tt.expected {
				t.Errorf("detectStuck() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestBuildClaudeArgs(t *testing.T) {
	loop := &Loop{
		templateDir: "/var/local/wisp/templates",
		claudeCfg: ClaudeConfig{
			MaxTurns:     100,
			MaxBudget:    10.0,
			Verbose:      true,
			OutputFormat: "stream-json",
		},
		limits: Limits{
			MaxBudgetUSD: 20.0,
		},
	}

	args := loop.buildClaudeArgs()

	// Check that essential args are present
	foundClaude := false
	foundPrompt := false
	foundVerbose := false
	foundOutput := false
	foundMaxTurns := false
	foundBudget := false

	for i, arg := range args {
		switch arg {
		case "claude":
			foundClaude = true
		case "-p":
			foundPrompt = true
		case "--verbose":
			foundVerbose = true
		case "--output-format":
			if i+1 < len(args) && args[i+1] == "stream-json" {
				foundOutput = true
			}
		case "--max-turns":
			if i+1 < len(args) && args[i+1] == "100" {
				foundMaxTurns = true
			}
		case "--max-budget-usd":
			if i+1 < len(args) && args[i+1] == "10.00" {
				foundBudget = true
			}
		}
	}

	if !foundClaude {
		t.Error("Expected 'claude' in args")
	}
	if !foundPrompt {
		t.Error("Expected '-p' in args")
	}
	if !foundVerbose {
		t.Error("Expected '--verbose' in args")
	}
	if !foundOutput {
		t.Error("Expected '--output-format stream-json' in args")
	}
	if !foundMaxTurns {
		t.Error("Expected '--max-turns 100' in args")
	}
	if !foundBudget {
		t.Error("Expected '--max-budget-usd 10.00' in args")
	}
}

func TestLoopRunContextCancel(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	os.MkdirAll(sessionDir, 0755)

	// Create a mock executor that blocks until context is cancelled
	executor := &MockExecutor{
		ExecuteFunc: func(ctx context.Context, dir string, args []string, eventCallback func(string), commandCallback func() error) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}

	loop := NewLoop(LoopOptions{
		SessionID:   "test-session",
		RepoPath:    tmpDir,
		SessionDir:  sessionDir,
		TemplateDir: filepath.Join(tmpDir, "templates"),
		Executor:    executor,
		Limits: Limits{
			MaxIterations: 10,
		},
	})

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	result := loop.Run(ctx)

	if result.Reason != ExitReasonBackground {
		t.Errorf("Expected ExitReasonBackground, got %v", result.Reason)
	}
}

func TestLoopRunMaxIterations(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	os.MkdirAll(sessionDir, 0755)

	// Write a state.json that says CONTINUE
	stateData := state.State{
		Status:  state.StatusContinue,
		Summary: "Working on it",
	}
	data, _ := json.Marshal(stateData)
	os.WriteFile(filepath.Join(sessionDir, "state.json"), data, 0644)

	// Write empty tasks
	os.WriteFile(filepath.Join(sessionDir, "tasks.json"), []byte("[]"), 0644)

	iteration := 0
	executor := &MockExecutor{
		ExecuteFunc: func(ctx context.Context, dir string, args []string, eventCallback func(string), commandCallback func() error) error {
			iteration++
			return nil
		},
	}

	loop := NewLoop(LoopOptions{
		SessionID:   "test-session",
		RepoPath:    tmpDir,
		SessionDir:  sessionDir,
		TemplateDir: filepath.Join(tmpDir, "templates"),
		Executor:    executor,
		Limits: Limits{
			MaxIterations: 3,
		},
	})

	result := loop.Run(context.Background())

	if result.Reason != ExitReasonMaxIterations {
		t.Errorf("Expected ExitReasonMaxIterations, got %v", result.Reason)
	}
	if result.Iterations != 3 {
		t.Errorf("Expected 3 iterations, got %d", result.Iterations)
	}
}

func TestLoopRunDone(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	os.MkdirAll(sessionDir, 0755)

	// Write a state.json that says DONE
	stateData := state.State{
		Status:  state.StatusDone,
		Summary: "All done",
	}
	data, _ := json.Marshal(stateData)
	os.WriteFile(filepath.Join(sessionDir, "state.json"), data, 0644)

	// Write tasks with all passes: true
	tasks := []state.Task{
		{Category: "feature", Description: "Task 1", Passes: true},
		{Category: "feature", Description: "Task 2", Passes: true},
	}
	tasksData, _ := json.Marshal(tasks)
	os.WriteFile(filepath.Join(sessionDir, "tasks.json"), tasksData, 0644)

	executor := &MockExecutor{}

	loop := NewLoop(LoopOptions{
		SessionID:   "test-session",
		RepoPath:    tmpDir,
		SessionDir:  sessionDir,
		TemplateDir: filepath.Join(tmpDir, "templates"),
		Executor:    executor,
		Limits: Limits{
			MaxIterations: 10,
		},
	})

	result := loop.Run(context.Background())

	if result.Reason != ExitReasonDone {
		t.Errorf("Expected ExitReasonDone, got %v", result.Reason)
	}
}

func TestLoopRunBlocked(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	os.MkdirAll(sessionDir, 0755)

	// Write a state.json that says BLOCKED
	stateData := state.State{
		Status:  state.StatusBlocked,
		Summary: "I'm stuck",
		Error:   "Missing dependency",
	}
	data, _ := json.Marshal(stateData)
	os.WriteFile(filepath.Join(sessionDir, "state.json"), data, 0644)

	// Write tasks
	os.WriteFile(filepath.Join(sessionDir, "tasks.json"), []byte("[]"), 0644)

	executor := &MockExecutor{}

	loop := NewLoop(LoopOptions{
		SessionID:   "test-session",
		RepoPath:    tmpDir,
		SessionDir:  sessionDir,
		TemplateDir: filepath.Join(tmpDir, "templates"),
		Executor:    executor,
		Limits: Limits{
			MaxIterations: 10,
		},
	})

	result := loop.Run(context.Background())

	if result.Reason != ExitReasonBlocked {
		t.Errorf("Expected ExitReasonBlocked, got %v", result.Reason)
	}
	if result.State == nil {
		t.Error("Expected State to be set")
	} else if result.State.Error != "Missing dependency" {
		t.Errorf("Expected error 'Missing dependency', got %q", result.State.Error)
	}
}

func TestLoopRunKillCommand(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	os.MkdirAll(sessionDir, 0755)

	// Write a state.json that says CONTINUE
	stateData := state.State{
		Status:  state.StatusContinue,
		Summary: "Working on it",
	}
	data, _ := json.Marshal(stateData)
	os.WriteFile(filepath.Join(sessionDir, "state.json"), data, 0644)
	os.WriteFile(filepath.Join(sessionDir, "tasks.json"), []byte("[]"), 0644)

	// Create file store for event publishing
	streamPath := filepath.Join(tmpDir, "stream.ndjson")
	fs, _ := stream.NewFileStore(streamPath)
	defer fs.Close()

	// Use a channel to coordinate between the test and executor
	executorStarted := make(chan struct{})

	executor := &MockExecutor{
		ExecuteFunc: func(ctx context.Context, dir string, args []string, eventCallback func(string), commandCallback func() error) error {
			// Signal that executor has started
			close(executorStarted)
			// Poll for commands until we get one
			for i := 0; i < 100; i++ {
				if err := commandCallback(); err != nil {
					return err
				}
				time.Sleep(10 * time.Millisecond)
			}
			return nil
		},
	}

	loop := NewLoop(LoopOptions{
		SessionID:   "test-session",
		RepoPath:    tmpDir,
		SessionDir:  sessionDir,
		TemplateDir: filepath.Join(tmpDir, "templates"),
		Executor:    executor,
		FileStore:   fs,
		Limits: Limits{
			MaxIterations: 10,
		},
	})

	// Send kill command after executor starts
	go func() {
		<-executorStarted
		time.Sleep(20 * time.Millisecond)
		cmd, _ := stream.NewKillCommand("cmd-1", false)
		loop.commandCh <- cmd
	}()

	result := loop.Run(context.Background())

	if result.Reason != ExitReasonUserKill {
		t.Errorf("Expected ExitReasonUserKill, got %v", result.Reason)
	}
}

func TestLoopRunDurationLimit(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	os.MkdirAll(sessionDir, 0755)

	// Write state and tasks
	stateData := state.State{Status: state.StatusContinue}
	data, _ := json.Marshal(stateData)
	os.WriteFile(filepath.Join(sessionDir, "state.json"), data, 0644)
	os.WriteFile(filepath.Join(sessionDir, "tasks.json"), []byte("[]"), 0644)

	executor := &MockExecutor{}

	loop := NewLoop(LoopOptions{
		SessionID:   "test-session",
		RepoPath:    tmpDir,
		SessionDir:  sessionDir,
		TemplateDir: filepath.Join(tmpDir, "templates"),
		Executor:    executor,
		Limits: Limits{
			MaxIterations:    100,
			MaxDurationHours: 0.0001, // Very short duration
		},
		StartTime: time.Now().Add(-time.Hour), // Start time in the past
	})

	result := loop.Run(context.Background())

	if result.Reason != ExitReasonMaxDuration {
		t.Errorf("Expected ExitReasonMaxDuration, got %v", result.Reason)
	}
}

func TestLoopRunStuck(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	os.MkdirAll(sessionDir, 0755)

	// Write state
	stateData := state.State{Status: state.StatusContinue}
	data, _ := json.Marshal(stateData)
	os.WriteFile(filepath.Join(sessionDir, "state.json"), data, 0644)

	// Write tasks with one incomplete
	tasks := []state.Task{
		{Category: "feature", Description: "Task 1", Passes: false},
	}
	tasksData, _ := json.Marshal(tasks)
	os.WriteFile(filepath.Join(sessionDir, "tasks.json"), tasksData, 0644)

	// Write history showing no progress for 5 iterations
	history := []state.History{
		{Iteration: 1, TasksCompleted: 0},
		{Iteration: 2, TasksCompleted: 0},
		{Iteration: 3, TasksCompleted: 0},
		{Iteration: 4, TasksCompleted: 0},
		{Iteration: 5, TasksCompleted: 0},
	}
	historyData, _ := json.Marshal(history)
	os.WriteFile(filepath.Join(sessionDir, "history.json"), historyData, 0644)

	executor := &MockExecutor{}

	loop := NewLoop(LoopOptions{
		SessionID:   "test-session",
		RepoPath:    tmpDir,
		SessionDir:  sessionDir,
		TemplateDir: filepath.Join(tmpDir, "templates"),
		Executor:    executor,
		Limits: Limits{
			MaxIterations:       100,
			NoProgressThreshold: 3,
		},
	})

	result := loop.Run(context.Background())

	if result.Reason != ExitReasonStuck {
		t.Errorf("Expected ExitReasonStuck, got %v", result.Reason)
	}
}

func TestLoopPublishEvents(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	os.MkdirAll(sessionDir, 0755)

	// Write state
	stateData := state.State{Status: state.StatusDone, Summary: "All done"}
	data, _ := json.Marshal(stateData)
	os.WriteFile(filepath.Join(sessionDir, "state.json"), data, 0644)

	// Write tasks
	tasks := []state.Task{
		{Category: "feature", Description: "Task 1", Passes: true},
	}
	tasksData, _ := json.Marshal(tasks)
	os.WriteFile(filepath.Join(sessionDir, "tasks.json"), tasksData, 0644)

	// Create file store
	streamPath := filepath.Join(tmpDir, "stream.ndjson")
	fs, _ := stream.NewFileStore(streamPath)
	defer fs.Close()

	executor := &MockExecutor{
		ExecuteFunc: func(ctx context.Context, dir string, args []string, eventCallback func(string), commandCallback func() error) error {
			// Simulate Claude output
			eventCallback(`{"type":"assistant","message":{"content":[{"type":"text","text":"Hello"}]}}`)
			return nil
		},
	}

	loop := NewLoop(LoopOptions{
		SessionID:   "test-session",
		RepoPath:    tmpDir,
		SessionDir:  sessionDir,
		TemplateDir: filepath.Join(tmpDir, "templates"),
		Executor:    executor,
		FileStore:   fs,
		Limits: Limits{
			MaxIterations: 10,
		},
	})

	loop.Run(context.Background())

	// Read events from file store
	events, err := fs.Read(0)
	if err != nil {
		t.Fatalf("Failed to read events: %v", err)
	}

	// Should have session events, task events, and claude events
	foundSession := false
	foundTask := false
	foundClaude := false

	for _, e := range events {
		switch e.Type {
		case stream.MessageTypeSession:
			foundSession = true
		case stream.MessageTypeTask:
			foundTask = true
		case stream.MessageTypeClaudeEvent:
			foundClaude = true
		}
	}

	if !foundSession {
		t.Error("Expected session event to be published")
	}
	if !foundTask {
		t.Error("Expected task event to be published")
	}
	if !foundClaude {
		t.Error("Expected claude event to be published")
	}
}
