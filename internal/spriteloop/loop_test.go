package spriteloop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thruflo/wisp/internal/config"
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

func TestLimitsFromConfig(t *testing.T) {
	cfg := config.Limits{
		MaxIterations:       50,
		MaxBudgetUSD:        15.0,
		MaxDurationHours:    4.0,
		NoProgressThreshold: 3,
	}

	limits := LimitsFromConfig(cfg)

	if limits.MaxIterations != 50 {
		t.Errorf("MaxIterations = %d, want 50", limits.MaxIterations)
	}
	if limits.MaxBudgetUSD != 15.0 {
		t.Errorf("MaxBudgetUSD = %f, want 15.0", limits.MaxBudgetUSD)
	}
	if limits.MaxDurationHours != 4.0 {
		t.Errorf("MaxDurationHours = %f, want 4.0", limits.MaxDurationHours)
	}
	if limits.NoProgressThreshold != 3 {
		t.Errorf("NoProgressThreshold = %d, want 3", limits.NoProgressThreshold)
	}
}

func TestLoopCommandCh(t *testing.T) {
	loop := NewLoop(LoopOptions{
		SessionID: "test-session",
	})

	ch := loop.CommandCh()
	if ch == nil {
		t.Error("Expected non-nil command channel")
	}

	// Test that we can send a command
	go func() {
		cmd := &stream.Command{ID: "test-cmd", Type: stream.CommandTypeKill}
		ch <- cmd
	}()

	select {
	case cmd := <-loop.commandCh:
		if cmd.ID != "test-cmd" {
			t.Errorf("Expected command ID 'test-cmd', got %q", cmd.ID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Command not received")
	}
}

func TestLoopWriteResponse(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	os.MkdirAll(sessionDir, 0755)

	loop := &Loop{
		sessionDir: sessionDir,
	}

	// Write a response
	err := loop.writeResponse("test response")
	if err != nil {
		t.Fatalf("writeResponse failed: %v", err)
	}

	// Read the response file
	responsePath := filepath.Join(sessionDir, "response.json")
	data, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatalf("Failed to read response file: %v", err)
	}

	var response string
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response != "test response" {
		t.Errorf("Expected 'test response', got %q", response)
	}
}

func TestLoopNeedsInputWithResponse(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	os.MkdirAll(sessionDir, 0755)

	// Create file store
	streamPath := filepath.Join(tmpDir, "stream.ndjson")
	fs, _ := stream.NewFileStore(streamPath)
	defer fs.Close()

	loop := &Loop{
		sessionID:  "test-session",
		sessionDir: sessionDir,
		fileStore:  fs,
		iteration:  1,
		inputCh:    make(chan string, 1),
		commandCh:  make(chan *stream.Command, 10),
	}

	// Create a state that needs input
	st := &state.State{
		Status:   state.StatusNeedsInput,
		Question: "What is your answer?",
	}

	// Send input response in a goroutine
	go func() {
		time.Sleep(50 * time.Millisecond)
		loop.inputCh <- "my answer"
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	result := loop.handleNeedsInput(ctx, st)

	// Should return unknown (continue loop) after receiving input
	if result.Reason != ExitReasonUnknown {
		t.Errorf("Expected ExitReasonUnknown, got %v", result.Reason)
	}

	// Check that response file was written
	responsePath := filepath.Join(sessionDir, "response.json")
	data, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatalf("Failed to read response file: %v", err)
	}

	var response string
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response != "my answer" {
		t.Errorf("Expected 'my answer', got %q", response)
	}
}

func TestLoopNeedsInputWithKillCommand(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	os.MkdirAll(sessionDir, 0755)

	// Create file store
	streamPath := filepath.Join(tmpDir, "stream.ndjson")
	fs, _ := stream.NewFileStore(streamPath)
	defer fs.Close()

	loop := &Loop{
		sessionID:  "test-session",
		sessionDir: sessionDir,
		fileStore:  fs,
		iteration:  1,
		inputCh:    make(chan string, 1),
		commandCh:  make(chan *stream.Command, 10),
	}

	st := &state.State{
		Status:   state.StatusNeedsInput,
		Question: "What is your answer?",
	}

	// Send kill command in a goroutine
	go func() {
		time.Sleep(50 * time.Millisecond)
		cmd, _ := stream.NewKillCommand("cmd-kill", false)
		loop.commandCh <- cmd
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	result := loop.handleNeedsInput(ctx, st)

	// Should return UserKill
	if result.Reason != ExitReasonUserKill {
		t.Errorf("Expected ExitReasonUserKill, got %v", result.Reason)
	}
}

func TestLoopNeedsInputWithBackgroundCommand(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	os.MkdirAll(sessionDir, 0755)

	// Create file store
	streamPath := filepath.Join(tmpDir, "stream.ndjson")
	fs, _ := stream.NewFileStore(streamPath)
	defer fs.Close()

	loop := &Loop{
		sessionID:  "test-session",
		sessionDir: sessionDir,
		fileStore:  fs,
		iteration:  1,
		inputCh:    make(chan string, 1),
		commandCh:  make(chan *stream.Command, 10),
	}

	st := &state.State{
		Status:   state.StatusNeedsInput,
		Question: "What is your answer?",
	}

	// Send background command in a goroutine
	go func() {
		time.Sleep(50 * time.Millisecond)
		cmd := stream.NewBackgroundCommand("cmd-bg")
		loop.commandCh <- cmd
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	result := loop.handleNeedsInput(ctx, st)

	// Should return Background
	if result.Reason != ExitReasonBackground {
		t.Errorf("Expected ExitReasonBackground, got %v", result.Reason)
	}
}

func TestLoopNeedsInputWithInputResponseCommand(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	os.MkdirAll(sessionDir, 0755)

	// Create file store
	streamPath := filepath.Join(tmpDir, "stream.ndjson")
	fs, _ := stream.NewFileStore(streamPath)
	defer fs.Close()

	loop := &Loop{
		sessionID:  "test-session",
		sessionDir: sessionDir,
		fileStore:  fs,
		iteration:  1,
		inputCh:    make(chan string, 1),
		commandCh:  make(chan *stream.Command, 10),
	}

	st := &state.State{
		Status:   state.StatusNeedsInput,
		Question: "What is your answer?",
	}

	// The request ID format used by handleNeedsInput
	expectedRequestID := "test-session-1-input"

	// Send input response command in a goroutine
	go func() {
		time.Sleep(50 * time.Millisecond)
		cmd, _ := stream.NewInputResponseCommand("cmd-input", expectedRequestID, "command response")
		loop.commandCh <- cmd
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	result := loop.handleNeedsInput(ctx, st)

	// Should return unknown (continue loop) after receiving input via command
	if result.Reason != ExitReasonUnknown {
		t.Errorf("Expected ExitReasonUnknown, got %v", result.Reason)
	}
}

func TestLoopNeedsInputContextCancel(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	os.MkdirAll(sessionDir, 0755)

	// Create file store
	streamPath := filepath.Join(tmpDir, "stream.ndjson")
	fs, _ := stream.NewFileStore(streamPath)
	defer fs.Close()

	loop := &Loop{
		sessionID:  "test-session",
		sessionDir: sessionDir,
		fileStore:  fs,
		iteration:  1,
		inputCh:    make(chan string, 1),
		commandCh:  make(chan *stream.Command, 10),
	}

	st := &state.State{
		Status:   state.StatusNeedsInput,
		Question: "What is your answer?",
	}

	// Cancel context immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := loop.handleNeedsInput(ctx, st)

	// Should return Background due to context cancellation
	if result.Reason != ExitReasonBackground {
		t.Errorf("Expected ExitReasonBackground, got %v", result.Reason)
	}
}

func TestLoopCheckCommandsWithKill(t *testing.T) {
	loop := &Loop{
		commandCh: make(chan *stream.Command, 10),
	}

	// Send a kill command
	cmd, _ := stream.NewKillCommand("cmd-1", false)
	loop.commandCh <- cmd

	result := loop.checkCommands()

	if result.Reason != ExitReasonUserKill {
		t.Errorf("Expected ExitReasonUserKill, got %v", result.Reason)
	}
}

func TestLoopCheckCommandsWithBackground(t *testing.T) {
	loop := &Loop{
		commandCh: make(chan *stream.Command, 10),
	}

	// Send a background command
	cmd := stream.NewBackgroundCommand("cmd-2")
	loop.commandCh <- cmd

	result := loop.checkCommands()

	if result.Reason != ExitReasonBackground {
		t.Errorf("Expected ExitReasonBackground, got %v", result.Reason)
	}
}

func TestLoopCheckCommandsEmpty(t *testing.T) {
	loop := &Loop{
		commandCh: make(chan *stream.Command, 10),
	}

	result := loop.checkCommands()

	if result.Reason != ExitReasonUnknown {
		t.Errorf("Expected ExitReasonUnknown, got %v", result.Reason)
	}
}

func TestLoopHandleCommand(t *testing.T) {
	tmpDir := t.TempDir()
	streamPath := filepath.Join(tmpDir, "stream.ndjson")
	fs, _ := stream.NewFileStore(streamPath)
	defer fs.Close()

	loop := &Loop{
		fileStore: fs,
	}

	t.Run("kill command", func(t *testing.T) {
		cmd, _ := stream.NewKillCommand("cmd-1", false)
		err := loop.handleCommand(cmd)
		if err != errUserKill {
			t.Errorf("Expected errUserKill, got %v", err)
		}
	})

	t.Run("background command", func(t *testing.T) {
		cmd := stream.NewBackgroundCommand("cmd-2")
		err := loop.handleCommand(cmd)
		if err != errUserBackground {
			t.Errorf("Expected errUserBackground, got %v", err)
		}
	})

	t.Run("input response command", func(t *testing.T) {
		cmd, _ := stream.NewInputResponseCommand("cmd-3", "req-1", "response")
		err := loop.handleCommand(cmd)
		// Input response is handled elsewhere, returns nil
		if err != nil {
			t.Errorf("Expected nil error, got %v", err)
		}
	})

	t.Run("unknown command", func(t *testing.T) {
		cmd := &stream.Command{
			ID:   "cmd-4",
			Type: stream.CommandType("unknown"),
		}
		err := loop.handleCommand(cmd)
		if err != nil {
			t.Errorf("Expected nil error for unknown command, got %v", err)
		}
	})
}

func TestLoopBuildClaudeArgsWithLimitsBudget(t *testing.T) {
	// Test that limits.MaxBudgetUSD is used when claudeCfg.MaxBudget is 0
	loop := &Loop{
		templateDir: "/var/local/wisp/templates",
		claudeCfg: ClaudeConfig{
			MaxTurns:     100,
			MaxBudget:    0, // Not set
			Verbose:      true,
			OutputFormat: "stream-json",
		},
		limits: Limits{
			MaxBudgetUSD: 25.0, // Should use this
		},
	}

	args := loop.buildClaudeArgs()

	foundBudget := false
	for i, arg := range args {
		if arg == "--max-budget-usd" && i+1 < len(args) {
			if args[i+1] == "25.00" {
				foundBudget = true
			}
		}
	}

	if !foundBudget {
		t.Error("Expected '--max-budget-usd 25.00' in args")
	}
}

func TestLoopReadTasksError(t *testing.T) {
	loop := &Loop{
		sessionDir: "/nonexistent/path",
	}

	// Should return empty slice for non-existent file
	tasks, err := loop.readTasks()
	if err == nil && tasks != nil && len(tasks) > 0 {
		t.Error("Expected empty tasks for non-existent file")
	}
}

func TestLoopReadTasksInvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	os.MkdirAll(sessionDir, 0755)

	// Write invalid JSON
	os.WriteFile(filepath.Join(sessionDir, "tasks.json"), []byte("{invalid"), 0644)

	loop := &Loop{
		sessionDir: sessionDir,
	}

	_, err := loop.readTasks()
	if err == nil {
		t.Error("Expected error for invalid JSON")
	}
}

func TestLoopAllTasksCompleteEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	os.MkdirAll(sessionDir, 0755)

	// Write empty tasks
	os.WriteFile(filepath.Join(sessionDir, "tasks.json"), []byte("[]"), 0644)

	loop := &Loop{
		sessionDir: sessionDir,
	}

	// Empty tasks should return false
	if loop.allTasksComplete() {
		t.Error("Expected false for empty tasks")
	}
}

func TestLoopPublishEventNoFileStore(t *testing.T) {
	loop := &Loop{
		fileStore: nil,
	}

	// Should not panic
	loop.publishEvent(stream.MessageTypeSession, &stream.SessionEvent{})
}

func TestLoopPublishClaudeEventInvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	streamPath := filepath.Join(tmpDir, "stream.ndjson")
	fs, _ := stream.NewFileStore(streamPath)
	defer fs.Close()

	loop := &Loop{
		fileStore: fs,
		sessionID: "test",
		iteration: 1,
	}

	// Should not panic with invalid JSON
	loop.publishClaudeEvent("not valid json")

	// Verify no event was published
	events, _ := fs.Read(0)
	if len(events) > 0 {
		t.Error("Expected no events for invalid JSON")
	}
}

func TestLoopPublishClaudeEventEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	streamPath := filepath.Join(tmpDir, "stream.ndjson")
	fs, _ := stream.NewFileStore(streamPath)
	defer fs.Close()

	loop := &Loop{
		fileStore: fs,
	}

	// Should not panic with empty line
	loop.publishClaudeEvent("")

	// Verify no event was published
	events, _ := fs.Read(0)
	if len(events) > 0 {
		t.Error("Expected no events for empty line")
	}
}
