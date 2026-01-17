package loop

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/sprite"
	"github.com/thruflo/wisp/internal/state"
	"github.com/thruflo/wisp/internal/tui"
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
// This allows tests to override production defaults and ensures
// consistent command building across the codebase.
type ClaudeConfig struct {
	MaxTurns     int     // Maximum number of Claude turns per iteration
	MaxBudget    float64 // Maximum budget in USD (0 for no limit)
	Verbose      bool    // Enable verbose output (required for stream-json with -p)
	OutputFormat string  // Output format (e.g., "stream-json")
}

// DefaultClaudeConfig returns production defaults for Claude execution.
func DefaultClaudeConfig() ClaudeConfig {
	return ClaudeConfig{
		MaxTurns:     100,
		MaxBudget:    0, // No limit by default; use config.Limits.MaxBudgetUSD for session budget
		Verbose:      true,
		OutputFormat: "stream-json",
	}
}

// Loop manages the Claude Code iteration loop.
type Loop struct {
	client      sprite.Client
	sync        *state.SyncManager
	store       *state.Store
	cfg         *config.Config
	session     *config.Session
	tui         *tui.TUI
	repoPath    string // Path on Sprite: /home/sprite/<org>/<repo>
	wispPath    string // Path on Sprite: <repoPath>/.wisp
	iteration   int
	startTime   time.Time
	templateDir string       // Local path to templates
	claudeCfg   ClaudeConfig // Claude command configuration
}

// LoopOptions holds configuration for creating a Loop instance.
// This struct enables test-friendly construction with explicit dependencies.
type LoopOptions struct {
	Client       sprite.Client
	SyncManager  *state.SyncManager
	Store        *state.Store
	Config       *config.Config
	Session      *config.Session
	TUI          *tui.TUI
	RepoPath     string
	TemplateDir  string
	StartTime    time.Time    // Optional: for deterministic time-based testing
	ClaudeConfig ClaudeConfig // Optional: Claude command config (defaults used if zero)
}

// NewLoop creates a new Loop instance.
func NewLoop(
	client sprite.Client,
	syncMgr *state.SyncManager,
	store *state.Store,
	cfg *config.Config,
	session *config.Session,
	t *tui.TUI,
	repoPath string,
	templateDir string,
) *Loop {
	return NewLoopWithOptions(LoopOptions{
		Client:      client,
		SyncManager: syncMgr,
		Store:       store,
		Config:      cfg,
		Session:     session,
		TUI:         t,
		RepoPath:    repoPath,
		TemplateDir: templateDir,
	})
}

// NewLoopWithOptions creates a Loop with explicit options.
// This allows tests to inject dependencies and control behavior.
// If ClaudeConfig is zero-valued, production defaults are used.
func NewLoopWithOptions(opts LoopOptions) *Loop {
	claudeCfg := opts.ClaudeConfig
	if claudeCfg == (ClaudeConfig{}) {
		claudeCfg = DefaultClaudeConfig()
	}

	return &Loop{
		client:      opts.Client,
		sync:        opts.SyncManager,
		store:       opts.Store,
		cfg:         opts.Config,
		session:     opts.Session,
		tui:         opts.TUI,
		repoPath:    opts.RepoPath,
		wispPath:    filepath.Join(opts.RepoPath, ".wisp"),
		templateDir: opts.TemplateDir,
		startTime:   opts.StartTime,
		claudeCfg:   claudeCfg,
	}
}

// Run executes the iteration loop until an exit condition is met.
// It returns a Result indicating why the loop stopped.
func (l *Loop) Run(ctx context.Context) Result {
	// Use injected start time if set, otherwise use current time
	if l.startTime.IsZero() {
		l.startTime = time.Now()
	}
	l.iteration = l.getStartingIteration()

	// Main loop
	for {
		// Check context cancellation
		if ctx.Err() != nil {
			return Result{Reason: ExitReasonBackground, Iterations: l.iteration}
		}

		// Check duration limit
		if l.checkDurationLimit() {
			return Result{Reason: ExitReasonMaxDuration, Iterations: l.iteration}
		}

		// Check iteration limit
		if l.iteration >= l.cfg.Limits.MaxIterations {
			return Result{Reason: ExitReasonMaxIterations, Iterations: l.iteration}
		}

		// Run one iteration
		l.iteration++
		l.updateTUIState()

		iterResult, err := l.runIteration(ctx)
		if err != nil {
			// Check for user actions
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

		// Sync state from Sprite to local storage
		if err := l.sync.SyncFromSprite(ctx, l.session.SpriteName, l.session.Branch); err != nil {
			return Result{
				Reason:     ExitReasonCrash,
				Iterations: l.iteration,
				Error:      fmt.Errorf("failed to sync state: %w", err),
			}
		}

		// Record history
		if err := l.recordHistory(ctx, iterResult); err != nil {
			// Non-fatal, continue
		}

		// Check exit conditions based on state
		switch iterResult.Status {
		case state.StatusDone:
			// Verify all tasks pass
			if l.allTasksComplete() {
				return Result{
					Reason:     ExitReasonDone,
					Iterations: l.iteration,
					State:      iterResult,
				}
			}
			// Not actually done, continue

		case state.StatusNeedsInput:
			// Handle user input
			inputResult := l.handleNeedsInput(ctx, iterResult)
			if inputResult.Reason != ExitReasonUnknown {
				return inputResult
			}
			// Input provided, continue loop

		case state.StatusBlocked:
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

	// Execute on Sprite
	cmd, err := l.client.Execute(ctx, l.session.SpriteName, l.repoPath, nil, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to start Claude: %w", err)
	}

	// Stream output to TUI
	errCh := make(chan error, 2)
	go func() {
		errCh <- l.streamOutput(ctx, cmd.Stdout)
	}()
	go func() {
		errCh <- l.streamOutput(ctx, cmd.Stderr)
	}()

	// Create a channel to monitor user actions while streaming
	actionCh := l.tui.Actions()
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	// Wait for completion or user action
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()

		case action := <-actionCh:
			switch action.Action {
			case tui.ActionKill:
				return nil, errUserKill
			case tui.ActionBackground, tui.ActionQuit:
				return nil, errUserBackground
			}

		case err := <-waitCh:
			// Command completed
			<-errCh // Wait for stdout
			<-errCh // Wait for stderr

			if err != nil {
				// Check exit code - non-zero might be okay if state.json exists
				if cmd.ExitCode() != 0 {
					// Try to read state anyway
				}
			}

			// Read state.json from Sprite
			st, err := l.readStateFromSprite(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to read state after iteration: %w", err)
			}
			return st, nil
		}
	}
}

// buildClaudeArgs constructs the Claude command line arguments.
// Returns args suitable for client.Execute, wrapped in bash with proper HOME for credentials.
func (l *Loop) buildClaudeArgs() []string {
	iteratePath := filepath.Join(sprite.TemplatesDir, "iterate.md")
	contextPath := filepath.Join(sprite.TemplatesDir, "context.md")

	claudeArgs := []string{
		"claude",
		"-p", fmt.Sprintf("\"$(cat %s)\"", iteratePath),
		"--append-system-prompt-file", contextPath,
		"--dangerously-skip-permissions",
	}

	// Add verbose flag if configured (required when using -p with --output-format stream-json)
	if l.claudeCfg.Verbose {
		claudeArgs = append(claudeArgs, "--verbose")
	}

	// Add output format
	if l.claudeCfg.OutputFormat != "" {
		claudeArgs = append(claudeArgs, "--output-format", l.claudeCfg.OutputFormat)
	}

	// Add max turns
	if l.claudeCfg.MaxTurns > 0 {
		claudeArgs = append(claudeArgs, "--max-turns", fmt.Sprintf("%d", l.claudeCfg.MaxTurns))
	}

	// Add budget limit from ClaudeConfig if set, otherwise fall back to config.Limits
	if l.claudeCfg.MaxBudget > 0 {
		claudeArgs = append(claudeArgs, "--max-budget-usd", fmt.Sprintf("%.2f", l.claudeCfg.MaxBudget))
	} else if l.cfg.Limits.MaxBudgetUSD > 0 {
		claudeArgs = append(claudeArgs, "--max-budget-usd", fmt.Sprintf("%.2f", l.cfg.Limits.MaxBudgetUSD))
	}

	// Wrap in bash with proper HOME for credentials
	return sprite.ClaudeCommand(claudeArgs)
}

// streamOutput reads from a reader and sends lines to the TUI.
func (l *Loop) streamOutput(ctx context.Context, r io.ReadCloser) error {
	if r == nil {
		return nil
	}
	defer r.Close()

	scanner := bufio.NewScanner(r)
	// Set a larger buffer for potentially long JSON lines
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Text()
		// Parse stream-json format and extract display text
		displayLine := l.parseStreamJSON(line)
		if displayLine != "" {
			l.tui.AppendTailLine(displayLine)
			l.tui.Update()
		}
	}

	return scanner.Err()
}

// StreamEvent represents a Claude stream-json event (top-level).
// Format: {"type":"assistant|user|result|system","message":{...},"subtype":"..."}
type StreamEvent struct {
	Type    string          `json:"type"`
	Subtype string          `json:"subtype,omitempty"`
	Message json.RawMessage `json:"message,omitempty"`
}

// StreamMessageContent represents content items within a message.
type StreamMessageContent struct {
	Type      string          `json:"type"`                 // "text", "tool_use", "tool_result"
	Text      string          `json:"text,omitempty"`       // For type="text"
	Name      string          `json:"name,omitempty"`       // For type="tool_use"
	ID        string          `json:"id,omitempty"`         // For type="tool_use"
	Input     json.RawMessage `json:"input,omitempty"`      // For type="tool_use"
	ToolUseID string          `json:"tool_use_id,omitempty"`// For type="tool_result"
	Content   string          `json:"content,omitempty"`    // For type="tool_result"
}

// StreamMessageWrapper wraps the message content array.
type StreamMessageWrapper struct {
	Content []StreamMessageContent `json:"content"`
}

// parseStreamJSON extracts display text from a stream-json line.
// Claude's stream-json format is:
//   {"type":"assistant","message":{"content":[{"type":"text","text":"..."}]}}
//   {"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{...}}]}}
//   {"type":"user","message":{"content":[{"type":"tool_result","content":"..."}]}}
//   {"type":"result","subtype":"success",...}
//   {"type":"system","subtype":"init",...}
func (l *Loop) parseStreamJSON(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}

	// Try to parse as JSON
	var event StreamEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		// Not JSON, return as-is
		return line
	}

	switch event.Type {
	case "assistant", "user":
		// Parse the nested message wrapper
		var wrapper StreamMessageWrapper
		if err := json.Unmarshal(event.Message, &wrapper); err != nil {
			return ""
		}
		return formatMessageContent(wrapper.Content, event.Type)

	case "result":
		if event.Subtype == "success" {
			return "[Session completed]"
		}
		return fmt.Sprintf("[Result: %s]", event.Subtype)

	case "system":
		if event.Subtype == "init" {
			return "[Session started]"
		}
		return ""
	}

	return ""
}

// formatMessageContent formats content items for display.
func formatMessageContent(content []StreamMessageContent, eventType string) string {
	var parts []string

	for _, item := range content {
		switch item.Type {
		case "text":
			if item.Text != "" {
				parts = append(parts, item.Text)
			}
		case "tool_use":
			// Extract command preview for Bash, or just show tool name
			desc := extractToolDescription(item.Name, item.Input)
			parts = append(parts, fmt.Sprintf("[%s] %s", item.Name, desc))
		case "tool_result":
			// Clean up and truncate tool results
			result := normalizeToolResult(item.Content)
			if result != "" {
				parts = append(parts, result)
			}
		}
	}

	return strings.Join(parts, "\n")
}

// normalizeToolResult cleans up tool result content for display.
// Removes cat -n style line numbers, collapses whitespace, and truncates.
func normalizeToolResult(content string) string {
	if content == "" {
		return ""
	}

	// Split into lines and process each
	lines := strings.Split(content, "\n")
	var cleanLines []string

	for _, line := range lines {
		// Trim whitespace
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Remove cat -n style line number prefixes (e.g., "    1→", "   12→")
		// Pattern: optional spaces, digits, arrow/tab, then content
		if idx := strings.Index(line, "→"); idx != -1 && idx < 10 {
			// Check if everything before → is spaces and digits
			prefix := line[:idx]
			isLineNum := true
			for _, c := range prefix {
				if c != ' ' && (c < '0' || c > '9') {
					isLineNum = false
					break
				}
			}
			if isLineNum {
				line = strings.TrimSpace(line[idx+len("→"):])
			}
		}

		if line != "" {
			cleanLines = append(cleanLines, line)
		}
	}

	// Join back and truncate
	result := strings.Join(cleanLines, " ")

	// Collapse multiple spaces
	for strings.Contains(result, "  ") {
		result = strings.ReplaceAll(result, "  ", " ")
	}

	// Truncate
	if len(result) > 200 {
		result = result[:200] + "..."
	}

	return result
}

// extractToolDescription gets a short description of the tool input.
func extractToolDescription(toolName string, input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}

	// For Bash, try to extract the command
	if toolName == "Bash" {
		var bashInput struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(input, &bashInput); err == nil && bashInput.Command != "" {
			cmd := bashInput.Command
			if len(cmd) > 60 {
				cmd = cmd[:60] + "..."
			}
			return cmd
		}
	}

	// For Read/Write/Edit, try to extract the file path
	if toolName == "Read" || toolName == "Write" || toolName == "Edit" {
		var fileInput struct {
			FilePath string `json:"file_path"`
		}
		if err := json.Unmarshal(input, &fileInput); err == nil && fileInput.FilePath != "" {
			return fileInput.FilePath
		}
	}

	return ""
}

// readStateFromSprite reads state.json from the Sprite.
func (l *Loop) readStateFromSprite(ctx context.Context) (*state.State, error) {
	statePath := filepath.Join(sprite.SessionDir, "state.json")
	data, err := l.client.ReadFile(ctx, l.session.SpriteName, statePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read state.json: %w", err)
	}

	var st state.State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("failed to parse state.json: %w", err)
	}

	return &st, nil
}

// recordHistory appends a history entry for the current iteration.
func (l *Loop) recordHistory(ctx context.Context, st *state.State) error {
	tasks, err := l.store.LoadTasks(l.session.Branch)
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

	return l.store.AppendHistory(l.session.Branch, entry)
}

// handleNeedsInput handles the NEEDS_INPUT state.
func (l *Loop) handleNeedsInput(ctx context.Context, st *state.State) Result {
	// Show input view
	l.tui.ShowInput(st.Question)
	l.tui.Bell()
	l.tui.Update()

	// Wait for user input
	for {
		select {
		case <-ctx.Done():
			return Result{Reason: ExitReasonBackground, Iterations: l.iteration}

		case action := <-l.tui.Actions():
			switch action.Action {
			case tui.ActionSubmitInput:
				// Write response to Sprite
				if err := l.sync.WriteResponseToSprite(ctx, l.session.SpriteName, action.Input); err != nil {
					return Result{
						Reason:     ExitReasonCrash,
						Iterations: l.iteration,
						Error:      fmt.Errorf("failed to write response: %w", err),
					}
				}
				// Continue loop
				return Result{Reason: ExitReasonUnknown}

			case tui.ActionCancelInput:
				// Stay in NEEDS_INPUT state, user cancelled
				l.tui.SetView(tui.ViewSummary)
				l.tui.Update()
				return Result{Reason: ExitReasonNeedsInput, Iterations: l.iteration, State: st}

			case tui.ActionKill:
				return Result{Reason: ExitReasonUserKill, Iterations: l.iteration}

			case tui.ActionBackground, tui.ActionQuit:
				return Result{Reason: ExitReasonBackground, Iterations: l.iteration}
			}
		}
	}
}

// getStartingIteration returns the iteration number to start from.
// This is based on the history length if resuming.
func (l *Loop) getStartingIteration() int {
	history, err := l.store.LoadHistory(l.session.Branch)
	if err != nil || len(history) == 0 {
		return 0
	}
	return history[len(history)-1].Iteration
}

// updateTUIState updates the TUI with current state.
func (l *Loop) updateTUIState() {
	tasks, _ := l.store.LoadTasks(l.session.Branch)
	completed := 0
	for _, t := range tasks {
		if t.Passes {
			completed++
		}
	}

	lastState, _ := l.store.LoadState(l.session.Branch)
	summary := ""
	status := "RUNNING"
	errMsg := ""
	if lastState != nil {
		summary = lastState.Summary
		status = lastState.Status
		errMsg = lastState.Error
	}

	viewState := tui.ViewState{
		Branch:         l.session.Branch,
		Iteration:      l.iteration,
		MaxIterations:  l.cfg.Limits.MaxIterations,
		Status:         status,
		CompletedTasks: completed,
		TotalTasks:     len(tasks),
		LastSummary:    summary,
		Error:          errMsg,
	}

	l.tui.SetState(viewState)
	l.tui.Update()
}

// checkDurationLimit checks if the max duration has been exceeded.
func (l *Loop) checkDurationLimit() bool {
	if l.cfg.Limits.MaxDurationHours <= 0 {
		return false
	}
	maxDuration := time.Duration(l.cfg.Limits.MaxDurationHours * float64(time.Hour))
	return time.Since(l.startTime) >= maxDuration
}

// allTasksComplete checks if all tasks have passes: true.
func (l *Loop) allTasksComplete() bool {
	tasks, err := l.store.LoadTasks(l.session.Branch)
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
	if l.cfg.Limits.NoProgressThreshold <= 0 {
		return false
	}

	history, err := l.store.LoadHistory(l.session.Branch)
	if err != nil || len(history) < l.cfg.Limits.NoProgressThreshold {
		return false
	}

	return DetectStuck(history, l.cfg.Limits.NoProgressThreshold)
}

// Sentinel errors for user actions.
var (
	errUserKill       = errors.New("user killed session")
	errUserBackground = errors.New("user backgrounded session")
)
