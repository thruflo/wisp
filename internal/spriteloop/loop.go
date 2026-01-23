package spriteloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/state"
	"github.com/thruflo/wisp/internal/stream"
)

// ExitReason indicates why the loop stopped.
type ExitReason int

const (
	ExitReasonUnknown       ExitReason = iota
	ExitReasonDone                     // All tasks completed
	ExitReasonNeedsInput               // Waiting for user input
	ExitReasonBlocked                  // Agent reported blockage
	ExitReasonMaxIterations            // Hit iteration limit
	ExitReasonMaxBudget                // Hit budget limit
	ExitReasonMaxDuration              // Hit duration limit
	ExitReasonStuck                    // No progress for N iterations
	ExitReasonUserKill                 // User killed session
	ExitReasonBackground               // User backgrounded session
	ExitReasonCrash                    // Claude crashed without state.json
)

// String returns a human-readable description of the exit reason.
func (r ExitReason) String() string {
	switch r {
	case ExitReasonDone:
		return "completed"
	case ExitReasonNeedsInput:
		return "needs input"
	case ExitReasonBlocked:
		return "blocked"
	case ExitReasonMaxIterations:
		return "max iterations"
	case ExitReasonMaxBudget:
		return "max budget"
	case ExitReasonMaxDuration:
		return "max duration"
	case ExitReasonStuck:
		return "stuck"
	case ExitReasonUserKill:
		return "user killed"
	case ExitReasonBackground:
		return "backgrounded"
	case ExitReasonCrash:
		return "crash"
	default:
		return "unknown"
	}
}

// Result contains the outcome of a loop execution.
type Result struct {
	Reason     ExitReason
	Iterations int
	State      *state.State
	Error      error
}

// ClaudeConfig holds configuration for Claude command execution.
type ClaudeConfig struct {
	MaxTurns     int     // Maximum number of Claude turns per iteration
	MaxBudget    float64 // Maximum budget in USD (0 for no limit)
	Verbose      bool    // Enable verbose output (required for stream-json with -p)
	OutputFormat string  // Output format (e.g., "stream-json")
}

// DefaultClaudeConfig returns production defaults for Claude execution.
func DefaultClaudeConfig() ClaudeConfig {
	return ClaudeConfig{
		MaxTurns:     200,
		MaxBudget:    0, // No limit by default
		Verbose:      true,
		OutputFormat: "stream-json",
	}
}

// Limits defines operational boundaries for the loop.
type Limits struct {
	MaxIterations       int
	MaxBudgetUSD        float64
	MaxDurationHours    float64
	NoProgressThreshold int
}

// DefaultLimits returns sensible defaults for loop limits.
func DefaultLimits() Limits {
	return Limits{
		MaxIterations:       100,
		MaxBudgetUSD:        20.0,
		MaxDurationHours:    8.0,
		NoProgressThreshold: 5,
	}
}

// LimitsFromConfig creates Limits from a config.Limits struct.
func LimitsFromConfig(cfg config.Limits) Limits {
	return Limits{
		MaxIterations:       cfg.MaxIterations,
		MaxBudgetUSD:        cfg.MaxBudgetUSD,
		MaxDurationHours:    cfg.MaxDurationHours,
		NoProgressThreshold: cfg.NoProgressThreshold,
	}
}

// Loop manages the Claude Code iteration loop on the Sprite.
type Loop struct {
	// Configuration
	sessionID   string // Branch name used as session identifier
	repoPath    string // Path to the repo: /var/local/wisp/repos/<org>/<repo>
	sessionDir  string // Path to session files: /var/local/wisp/session
	templateDir string // Path to templates: /var/local/wisp/templates
	limits      Limits
	claudeCfg   ClaudeConfig

	// State
	iteration int
	startTime time.Time
	eventSeq  int // Sequence counter for Claude events within an iteration

	// Dependencies
	fileStore *stream.FileStore
	executor  ClaudeExecutor // Interface for Claude execution (allows testing)

	// Command handling
	commandCh chan *stream.Command // Channel for receiving commands
	inputCh   chan string          // Channel for receiving user input
}

// LoopOptions holds configuration for creating a Loop instance.
type LoopOptions struct {
	SessionID   string
	RepoPath    string
	SessionDir  string
	TemplateDir string
	Limits      Limits
	ClaudeConfig ClaudeConfig
	FileStore    *stream.FileStore
	Executor     ClaudeExecutor
	StartTime    time.Time // For testing
}

// NewLoop creates a new Loop with the given options.
func NewLoop(opts LoopOptions) *Loop {
	claudeCfg := opts.ClaudeConfig
	if claudeCfg == (ClaudeConfig{}) {
		claudeCfg = DefaultClaudeConfig()
	}

	limits := opts.Limits
	if limits == (Limits{}) {
		limits = DefaultLimits()
	}

	return &Loop{
		sessionID:   opts.SessionID,
		repoPath:    opts.RepoPath,
		sessionDir:  opts.SessionDir,
		templateDir: opts.TemplateDir,
		limits:      limits,
		claudeCfg:   claudeCfg,
		fileStore:   opts.FileStore,
		executor:    opts.Executor,
		startTime:   opts.StartTime,
		commandCh:   make(chan *stream.Command, 10),
		inputCh:     make(chan string, 1),
	}
}

// CommandCh returns the channel for sending commands to the loop.
func (l *Loop) CommandCh() chan<- *stream.Command {
	return l.commandCh
}

// Run executes the iteration loop until an exit condition is met.
func (l *Loop) Run(ctx context.Context) Result {
	if l.startTime.IsZero() {
		l.startTime = time.Now()
	}
	l.iteration = l.getStartingIteration()

	// Publish initial session state
	l.publishSessionState(stream.SessionStatusRunning)

	// Main loop
	for {
		// Check context cancellation
		if ctx.Err() != nil {
			return Result{Reason: ExitReasonBackground, Iterations: l.iteration}
		}

		// Check for pending commands
		if result := l.checkCommands(); result.Reason != ExitReasonUnknown {
			return result
		}

		// Check duration limit
		if l.checkDurationLimit() {
			return Result{Reason: ExitReasonMaxDuration, Iterations: l.iteration}
		}

		// Check iteration limit
		if l.iteration >= l.limits.MaxIterations {
			return Result{Reason: ExitReasonMaxIterations, Iterations: l.iteration}
		}

		// Run one iteration
		l.iteration++
		l.eventSeq = 0 // Reset event sequence for new iteration
		l.publishSessionState(stream.SessionStatusRunning)

		iterResult, err := l.runIteration(ctx)
		if err != nil {
			// Check for user actions from command channel
			if errors.Is(err, errUserKill) {
				return Result{Reason: ExitReasonUserKill, Iterations: l.iteration}
			}
			if errors.Is(err, errUserBackground) {
				return Result{Reason: ExitReasonBackground, Iterations: l.iteration}
			}

			// Claude crash or other error
			return Result{
				Reason:     ExitReasonCrash,
				Iterations: l.iteration,
				Error:      err,
			}
		}

		// Record history
		if err := l.recordHistory(iterResult); err != nil {
			// Non-fatal, continue
		}

		// Publish task state
		l.publishTaskState()

		// Check exit conditions based on state
		switch iterResult.Status {
		case state.StatusDone:
			if l.allTasksComplete() {
				l.publishSessionState(stream.SessionStatusDone)
				return Result{
					Reason:     ExitReasonDone,
					Iterations: l.iteration,
					State:      iterResult,
				}
			}
			// Not actually done, continue

		case state.StatusNeedsInput:
			inputResult := l.handleNeedsInput(ctx, iterResult)
			if inputResult.Reason != ExitReasonUnknown {
				return inputResult
			}
			// Input provided, continue loop

		case state.StatusBlocked:
			l.publishSessionState(stream.SessionStatusBlocked)
			return Result{
				Reason:     ExitReasonBlocked,
				Iterations: l.iteration,
				State:      iterResult,
			}
		}

		// Check stuck detection
		if l.isStuck() {
			return Result{
				Reason:     ExitReasonStuck,
				Iterations: l.iteration,
				State:      iterResult,
			}
		}
	}
}

// runIteration executes a single Claude Code invocation.
func (l *Loop) runIteration(ctx context.Context) (*state.State, error) {
	// Build Claude command
	args := l.buildClaudeArgs()

	// Create callback to publish Claude events to the stream
	eventCallback := func(line string) {
		l.publishClaudeEvent(line)
	}

	// Create callback to check for commands
	commandCallback := func() error {
		select {
		case cmd := <-l.commandCh:
			return l.handleCommand(cmd)
		default:
			return nil
		}
	}

	// Execute Claude locally
	err := l.executor.Execute(ctx, l.repoPath, args, eventCallback, commandCallback)
	if err != nil {
		// Check for context cancellation (backgrounded)
		if ctx.Err() != nil {
			return nil, errUserBackground
		}
		return nil, err
	}

	// Read state.json from local filesystem
	st, err := l.readState()
	if err != nil {
		return nil, fmt.Errorf("failed to read state after iteration: %w", err)
	}
	return st, nil
}

// buildClaudeArgs constructs the Claude command line arguments.
func (l *Loop) buildClaudeArgs() []string {
	iteratePath := filepath.Join(l.templateDir, "iterate.md")
	contextPath := filepath.Join(l.templateDir, "context.md")

	args := []string{
		"claude",
		"-p", fmt.Sprintf("$(cat %s)", iteratePath),
		"--append-system-prompt-file", contextPath,
		"--dangerously-skip-permissions",
	}

	if l.claudeCfg.Verbose {
		args = append(args, "--verbose")
	}

	if l.claudeCfg.OutputFormat != "" {
		args = append(args, "--output-format", l.claudeCfg.OutputFormat)
	}

	if l.claudeCfg.MaxTurns > 0 {
		args = append(args, "--max-turns", fmt.Sprintf("%d", l.claudeCfg.MaxTurns))
	}

	if l.claudeCfg.MaxBudget > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%.2f", l.claudeCfg.MaxBudget))
	} else if l.limits.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%.2f", l.limits.MaxBudgetUSD))
	}

	return args
}

// readState reads state.json from the session directory.
func (l *Loop) readState() (*state.State, error) {
	statePath := filepath.Join(l.sessionDir, "state.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read state.json: %w", err)
	}

	var st state.State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("failed to parse state.json: %w", err)
	}

	return &st, nil
}

// readTasks reads tasks.json from the session directory.
func (l *Loop) readTasks() ([]state.Task, error) {
	tasksPath := filepath.Join(l.sessionDir, "tasks.json")
	data, err := os.ReadFile(tasksPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read tasks.json: %w", err)
	}

	var tasks []state.Task
	if err := json.Unmarshal(data, &tasks); err != nil {
		return nil, fmt.Errorf("failed to parse tasks.json: %w", err)
	}

	return tasks, nil
}

// readHistory reads history.json from the session directory.
func (l *Loop) readHistory() ([]state.History, error) {
	historyPath := filepath.Join(l.sessionDir, "history.json")
	data, err := os.ReadFile(historyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read history.json: %w", err)
	}

	var history []state.History
	if err := json.Unmarshal(data, &history); err != nil {
		return nil, fmt.Errorf("failed to parse history.json: %w", err)
	}

	return history, nil
}

// writeResponse writes a response file for the agent to read.
func (l *Loop) writeResponse(response string) error {
	responsePath := filepath.Join(l.sessionDir, "response.json")
	data, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("failed to marshal response: %w", err)
	}

	if err := os.WriteFile(responsePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write response.json: %w", err)
	}

	return nil
}

// recordHistory appends a history entry for the current iteration.
func (l *Loop) recordHistory(st *state.State) error {
	tasks, err := l.readTasks()
	if err != nil {
		return err
	}

	completed := 0
	for _, t := range tasks {
		if t.Passes {
			completed++
		}
	}

	entry := state.History{
		Iteration:      l.iteration,
		Summary:        st.Summary,
		TasksCompleted: completed,
		Status:         st.Status,
	}

	// Read existing history
	history, err := l.readHistory()
	if err != nil {
		return err
	}

	// Append new entry
	history = append(history, entry)

	// Write back
	historyPath := filepath.Join(l.sessionDir, "history.json")
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal history: %w", err)
	}

	if err := os.WriteFile(historyPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write history.json: %w", err)
	}

	return nil
}

// handleNeedsInput handles the NEEDS_INPUT state.
func (l *Loop) handleNeedsInput(ctx context.Context, st *state.State) Result {
	// Publish input request event
	requestID := fmt.Sprintf("%s-%d-input", l.sessionID, l.iteration)
	inputReq := &stream.InputRequest{
		ID:        requestID,
		SessionID: l.sessionID,
		Iteration: l.iteration,
		Question:  st.Question,
	}
	l.publishInputRequest(inputReq)
	l.publishSessionState(stream.SessionStatusNeedsInput)

	// Wait for input response
	for {
		select {
		case <-ctx.Done():
			return Result{Reason: ExitReasonBackground, Iterations: l.iteration}

		case response := <-l.inputCh:
			// Write response for the agent
			if err := l.writeResponse(response); err != nil {
				return Result{
					Reason:     ExitReasonCrash,
					Iterations: l.iteration,
					Error:      fmt.Errorf("failed to write response: %w", err),
				}
			}
			// Publish input response event (State Protocol pattern)
			l.publishInputResponse(requestID, response)
			return Result{Reason: ExitReasonUnknown}

		case cmd := <-l.commandCh:
			// Handle commands (input responses now come via inputCh)
			if err := l.handleCommand(cmd); err != nil {
				if errors.Is(err, errUserKill) {
					return Result{Reason: ExitReasonUserKill, Iterations: l.iteration}
				}
				if errors.Is(err, errUserBackground) {
					return Result{Reason: ExitReasonBackground, Iterations: l.iteration}
				}
			}

		case <-time.After(100 * time.Millisecond):
			// Poll periodically
			continue
		}
	}
}

// checkCommands checks for and processes any pending commands.
func (l *Loop) checkCommands() Result {
	select {
	case cmd := <-l.commandCh:
		if err := l.handleCommand(cmd); err != nil {
			if errors.Is(err, errUserKill) {
				return Result{Reason: ExitReasonUserKill, Iterations: l.iteration}
			}
			if errors.Is(err, errUserBackground) {
				return Result{Reason: ExitReasonBackground, Iterations: l.iteration}
			}
		}
	default:
		// No commands pending
	}
	return Result{Reason: ExitReasonUnknown}
}

// handleCommand processes a command from the command channel.
func (l *Loop) handleCommand(cmd *stream.Command) error {
	switch cmd.Type {
	case stream.CommandTypeKill:
		l.publishAck(cmd.ID, nil)
		return errUserKill
	case stream.CommandTypeBackground:
		l.publishAck(cmd.ID, nil)
		return errUserBackground
	default:
		l.publishAck(cmd.ID, fmt.Errorf("unknown command type: %s", cmd.Type))
		return nil
	}
}

// getStartingIteration returns the iteration number to start from.
func (l *Loop) getStartingIteration() int {
	history, err := l.readHistory()
	if err != nil || len(history) == 0 {
		return 0
	}
	return history[len(history)-1].Iteration
}

// checkDurationLimit checks if the max duration has been exceeded.
func (l *Loop) checkDurationLimit() bool {
	if l.limits.MaxDurationHours <= 0 {
		return false
	}
	maxDuration := time.Duration(l.limits.MaxDurationHours * float64(time.Hour))
	return time.Since(l.startTime) >= maxDuration
}

// allTasksComplete checks if all tasks have passes: true.
func (l *Loop) allTasksComplete() bool {
	tasks, err := l.readTasks()
	if err != nil {
		return false
	}
	for _, t := range tasks {
		if !t.Passes {
			return false
		}
	}
	return len(tasks) > 0
}

// isStuck checks if the loop is stuck (no progress for N iterations).
func (l *Loop) isStuck() bool {
	if l.limits.NoProgressThreshold <= 0 {
		return false
	}

	history, err := l.readHistory()
	if err != nil || len(history) < l.limits.NoProgressThreshold {
		return false
	}

	return detectStuck(history, l.limits.NoProgressThreshold)
}

// detectStuck checks if the loop is stuck by analyzing history.
// A loop is considered stuck if tasks_completed hasn't increased
// for the last N iterations where N is the threshold.
func detectStuck(history []state.History, threshold int) bool {
	if threshold <= 0 || len(history) < threshold {
		return false
	}

	// Get the last N entries
	recent := history[len(history)-threshold:]

	// Check if tasks_completed is the same across all recent entries
	firstCompleted := recent[0].TasksCompleted
	for _, entry := range recent[1:] {
		if entry.TasksCompleted != firstCompleted {
			// Progress was made at some point
			return false
		}
	}

	// No progress in the last N iterations
	return true
}

// Sentinel errors for user actions.
var (
	errUserKill       = errors.New("user killed session")
	errUserBackground = errors.New("user backgrounded session")
)


// publishSessionState publishes the current session state.
func (l *Loop) publishSessionState(status stream.SessionStatus) {
	session := &stream.Session{
		ID:        l.sessionID,
		Repo:      "", // Will be populated by caller if needed
		Branch:    l.sessionID,
		Spec:      "",
		Status:    status,
		Iteration: l.iteration,
		StartedAt: l.startTime,
	}
	l.publishSession(session)
}

// publishSession publishes a session event to the FileStore.
func (l *Loop) publishSession(session *stream.Session) {
	if l.fileStore == nil {
		return
	}
	event, err := stream.NewSessionEvent(session)
	if err != nil {
		return
	}
	l.fileStore.Append(event)
}

// publishTaskState publishes the current task states.
func (l *Loop) publishTaskState() {
	tasks, err := l.readTasks()
	if err != nil {
		return
	}

	for i, t := range tasks {
		var taskStatus stream.TaskStatus
		if t.Passes {
			taskStatus = stream.TaskStatusCompleted
		} else {
			// The first incomplete task is considered in progress
			foundIncomplete := false
			for j := 0; j < i; j++ {
				if !tasks[j].Passes {
					foundIncomplete = true
					break
				}
			}
			if !foundIncomplete && !t.Passes {
				taskStatus = stream.TaskStatusInProgress
			} else {
				taskStatus = stream.TaskStatusPending
			}
		}

		task := &stream.Task{
			ID:          fmt.Sprintf("%s-task-%d", l.sessionID, i),
			SessionID:   l.sessionID,
			Order:       i,
			Category:    t.Category,
			Description: t.Description,
			Status:      taskStatus,
		}
		l.publishTask(task)
	}
}

// publishTask publishes a task event to the FileStore.
func (l *Loop) publishTask(task *stream.Task) {
	if l.fileStore == nil {
		return
	}
	event, err := stream.NewTaskEvent(task)
	if err != nil {
		return
	}
	l.fileStore.Append(event)
}

// publishClaudeEvent publishes a Claude output line to the stream.
func (l *Loop) publishClaudeEvent(line string) {
	if l.fileStore == nil || line == "" {
		return
	}

	// Try to parse as JSON to get the raw SDK message
	var sdkMessage any
	if err := json.Unmarshal([]byte(line), &sdkMessage); err != nil {
		// Not valid JSON, skip
		return
	}

	l.eventSeq++
	ce := &stream.ClaudeEvent{
		ID:        fmt.Sprintf("%s-%d-%d", l.sessionID, l.iteration, l.eventSeq),
		SessionID: l.sessionID,
		Iteration: l.iteration,
		Sequence:  l.eventSeq,
		Message:   sdkMessage,
		Timestamp: time.Now(),
	}

	event, err := stream.NewClaudeEventEvent(ce)
	if err != nil {
		return
	}
	l.fileStore.Append(event)
}

// publishAck publishes a command acknowledgment.
func (l *Loop) publishAck(commandID string, err error) {
	if l.fileStore == nil {
		return
	}
	var ack *stream.Ack
	if err != nil {
		ack = stream.NewErrorAck(commandID, err)
	} else {
		ack = stream.NewSuccessAck(commandID)
	}
	event, eventErr := stream.NewAckEvent(ack)
	if eventErr != nil {
		return
	}
	l.fileStore.Append(event)
}

// publishInputRequest publishes an input request event.
func (l *Loop) publishInputRequest(ir *stream.InputRequest) {
	if l.fileStore == nil {
		return
	}
	event, err := stream.NewInputRequestEvent(ir)
	if err != nil {
		return
	}
	l.fileStore.Append(event)
}

// publishInputResponse publishes an input response event.
// This follows the State Protocol pattern for durable mutations.
func (l *Loop) publishInputResponse(requestID, response string) {
	if l.fileStore == nil {
		return
	}
	ir := &stream.InputResponse{
		ID:        fmt.Sprintf("%s-response", requestID),
		RequestID: requestID,
		Response:  response,
	}
	event, err := stream.NewInputResponseEvent(ir)
	if err != nil {
		return
	}
	l.fileStore.Append(event)
}
