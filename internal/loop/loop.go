package loop

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/server"
	"github.com/thruflo/wisp/internal/sprite"
	"github.com/thruflo/wisp/internal/state"
	"github.com/thruflo/wisp/internal/stream"
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
		MaxTurns:     200,
		MaxBudget:    0, // No limit by default; use config.Limits.MaxBudgetUSD for session budget
		Verbose:      true,
		OutputFormat: "stream-json",
	}
}

// Loop manages coordination with the wisp-sprite binary running on a Sprite.
// The actual iteration logic runs on the Sprite; this orchestrator handles:
// - Starting/connecting to wisp-sprite
// - Processing stream events and updating TUI
// - Forwarding TUI actions as stream commands
// - Handling session exit conditions
type Loop struct {
	client       sprite.Client
	sync         *state.SyncManager
	store        *state.Store
	cfg          *config.Config
	session      *config.Session
	tui          *tui.TUI
	server       *server.Server   // Optional web server for remote access
	streamClient *stream.StreamClient // Client for communicating with wisp-sprite
	repoPath     string           // Path on Sprite: /var/local/wisp/repos/<org>/<repo>
	iteration    int
	startTime    time.Time
	templateDir  string       // Local path to templates
	claudeCfg    ClaudeConfig // Claude command configuration (for compatibility)
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
	Server       *server.Server       // Optional: web server for remote access
	StreamClient *stream.StreamClient // Optional: pre-configured stream client
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
		client:       opts.Client,
		sync:         opts.SyncManager,
		store:        opts.Store,
		cfg:          opts.Config,
		session:      opts.Session,
		tui:          opts.TUI,
		server:       opts.Server,
		streamClient: opts.StreamClient,
		repoPath:     opts.RepoPath,
		templateDir:  opts.TemplateDir,
		startTime:    opts.StartTime,
		claudeCfg:    claudeCfg,
	}
}

// Run coordinates with wisp-sprite until an exit condition is met.
// It connects to the stream server on the Sprite, processes events,
// and returns a Result indicating why coordination stopped.
func (l *Loop) Run(ctx context.Context) Result {
	// Use injected start time if set, otherwise use current time
	if l.startTime.IsZero() {
		l.startTime = time.Now()
	}
	l.iteration = l.getStartingIteration()

	// Start wisp-sprite if not already running and connect to it
	if err := l.ensureSpriteRunnerAndConnect(ctx); err != nil {
		return Result{
			Reason: ExitReasonCrash,
			Error:  fmt.Errorf("failed to start/connect to wisp-sprite: %w", err),
		}
	}

	// Get initial state snapshot
	snapshot, err := l.streamClient.GetState(ctx)
	if err != nil {
		return Result{
			Reason: ExitReasonCrash,
			Error:  fmt.Errorf("failed to get initial state: %w", err),
		}
	}

	// Update TUI with initial state
	l.tui.UpdateFromSnapshot(snapshot)
	l.tui.Update()

	// Subscribe to stream events
	eventCh, errCh := l.streamClient.Subscribe(ctx, snapshot.LastSeq+1)

	// Get TUI action channel
	actionCh := l.tui.Actions()

	// Main coordination loop
	for {
		select {
		case <-ctx.Done():
			return Result{Reason: ExitReasonBackground, Iterations: l.iteration}

		case err := <-errCh:
			if err != nil {
				return Result{
					Reason:     ExitReasonCrash,
					Iterations: l.iteration,
					Error:      fmt.Errorf("stream error: %w", err),
				}
			}
			// Channel closed, stream ended
			return Result{Reason: ExitReasonBackground, Iterations: l.iteration}

		case event := <-eventCh:
			if event == nil {
				// Channel closed
				return Result{Reason: ExitReasonBackground, Iterations: l.iteration}
			}

			// Process stream event
			result := l.handleStreamEvent(ctx, event)
			if result.Reason != ExitReasonUnknown {
				return result
			}

		case action := <-actionCh:
			// Handle TUI action
			result := l.handleTUIAction(ctx, action)
			if result.Reason != ExitReasonUnknown {
				return result
			}
		}
	}
}

// ensureSpriteRunnerAndConnect starts wisp-sprite if needed and connects to it.
func (l *Loop) ensureSpriteRunnerAndConnect(ctx context.Context) error {
	if l.streamClient != nil {
		// Already have a client, just verify connection
		return l.streamClient.Connect(ctx)
	}

	// Check if wisp-sprite is running
	running, err := l.isSpriteRunnerRunning(ctx)
	if err != nil {
		return fmt.Errorf("failed to check if wisp-sprite is running: %w", err)
	}

	if !running {
		// Start wisp-sprite
		if err := l.startSpriteRunner(ctx); err != nil {
			return fmt.Errorf("failed to start wisp-sprite: %w", err)
		}
	}

	// Wait for and connect to stream server
	streamURL, err := l.waitForSpriteRunner(ctx)
	if err != nil {
		return fmt.Errorf("wisp-sprite not ready: %w", err)
	}

	// Create stream client
	l.streamClient = stream.NewStreamClient(streamURL)
	l.tui.SetStreamClient(l.streamClient)

	return l.streamClient.Connect(ctx)
}

// isSpriteRunnerRunning checks if wisp-sprite is running on the Sprite.
func (l *Loop) isSpriteRunnerRunning(ctx context.Context) (bool, error) {
	const pidPath = "/var/local/wisp/wisp-sprite.pid"

	// Check if PID file exists
	_, _, exitCode, err := l.client.ExecuteOutput(ctx, l.session.SpriteName, "", nil, "test", "-f", pidPath)
	if err != nil {
		return false, err
	}
	if exitCode != 0 {
		return false, nil
	}

	// Check if process is running
	checkCmd := fmt.Sprintf("kill -0 $(cat %s) 2>/dev/null", pidPath)
	_, _, exitCode, err = l.client.ExecuteOutput(ctx, l.session.SpriteName, "", nil, "sh", "-c", checkCmd)
	if err != nil {
		return false, err
	}

	return exitCode == 0, nil
}

// startSpriteRunner starts the wisp-sprite binary on the Sprite.
func (l *Loop) startSpriteRunner(ctx context.Context) error {
	const (
		binaryPath = "/var/local/wisp/bin/wisp-sprite"
		pidPath    = "/var/local/wisp/wisp-sprite.pid"
		logPath    = "/var/local/wisp/wisp-sprite.log"
		port       = 8374
	)

	// Build command arguments
	cmdStr := fmt.Sprintf(
		"nohup %s -port %d -session-id %s -work-dir %s > %s 2>&1 & echo $! > %s",
		binaryPath, port, l.session.Branch, l.repoPath, logPath, pidPath,
	)

	_, stderr, exitCode, err := l.client.ExecuteOutput(ctx, l.session.SpriteName, "", nil, "sh", "-c", cmdStr)
	if err != nil {
		return fmt.Errorf("failed to start wisp-sprite: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("failed to start wisp-sprite (exit %d): %s", exitCode, string(stderr))
	}

	return nil
}

// waitForSpriteRunner waits for wisp-sprite to become ready.
func (l *Loop) waitForSpriteRunner(ctx context.Context) (string, error) {
	const (
		port         = 8374
		timeout      = 30 * time.Second
		pollInterval = 500 * time.Millisecond
	)

	deadline := time.Now().Add(timeout)
	healthCmd := fmt.Sprintf("curl -s -o /dev/null -w '%%{http_code}' http://localhost:%d/health", port)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		stdout, _, exitCode, err := l.client.ExecuteOutput(ctx, l.session.SpriteName, "", nil, "sh", "-c", healthCmd)
		if err == nil && exitCode == 0 && string(stdout) == "200" {
			return fmt.Sprintf("http://localhost:%d", port), nil
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(pollInterval):
		}
	}

	return "", fmt.Errorf("wisp-sprite did not become ready within %v", timeout)
}

// handleStreamEvent processes a stream event and updates state.
func (l *Loop) handleStreamEvent(ctx context.Context, event *stream.Event) Result {
	// Update TUI with the event
	l.tui.HandleStreamEvent(event)

	switch event.Type {
	case stream.MessageTypeSession:
		return l.handleSessionEvent(ctx, event)

	case stream.MessageTypeTask:
		// Task updates are handled by TUI, sync to local store
		l.syncStateFromSprite(ctx)

	case stream.MessageTypeClaudeEvent:
		// Claude events are handled by TUI (tail view)
		// Also broadcast to web clients if server is running
		l.broadcastClaudeEvent(event)

	case stream.MessageTypeInputRequest:
		return l.handleInputRequestEvent(ctx, event)

	case stream.MessageTypeAck:
		// Acknowledgments can be ignored for now
	}

	return Result{Reason: ExitReasonUnknown}
}

// handleSessionEvent processes session state updates.
func (l *Loop) handleSessionEvent(ctx context.Context, event *stream.Event) Result {
	data, err := event.SessionData()
	if err != nil {
		return Result{Reason: ExitReasonUnknown}
	}

	// Update local iteration count
	l.iteration = data.Iteration

	// Sync state from Sprite
	l.syncStateFromSprite(ctx)

	// Check for terminal states
	switch data.Status {
	case stream.SessionStatusDone:
		return Result{
			Reason:     ExitReasonDone,
			Iterations: l.iteration,
		}

	case stream.SessionStatusBlocked:
		return Result{
			Reason:     ExitReasonBlocked,
			Iterations: l.iteration,
		}
	}

	// Broadcast to web clients
	l.broadcastSession(data)

	return Result{Reason: ExitReasonUnknown}
}

// handleInputRequestEvent processes input request events.
func (l *Loop) handleInputRequestEvent(ctx context.Context, event *stream.Event) Result {
	data, err := event.InputRequestData()
	if err != nil {
		return Result{Reason: ExitReasonUnknown}
	}

	// In State Protocol, presence of input_request means it's pending
	// Broadcast to web clients
	l.broadcastInputRequest(data)

	// TUI will show input prompt via HandleStreamEvent
	// User input is handled via TUI actions

	return Result{Reason: ExitReasonUnknown}
}

// handleTUIAction processes a TUI action and sends appropriate command.
func (l *Loop) handleTUIAction(ctx context.Context, action tui.ActionEvent) Result {
	switch action.Action {
	case tui.ActionKill:
		// Send kill command to Sprite
		commandID := fmt.Sprintf("kill-%d", time.Now().UnixNano())
		_, err := l.streamClient.SendKillCommand(ctx, commandID, false)
		if err != nil {
			// Command failed, but still exit
		}
		return Result{Reason: ExitReasonUserKill, Iterations: l.iteration}

	case tui.ActionBackground, tui.ActionQuit:
		// Send background command to Sprite
		commandID := fmt.Sprintf("bg-%d", time.Now().UnixNano())
		_, _ = l.streamClient.SendBackgroundCommand(ctx, commandID)
		return Result{Reason: ExitReasonBackground, Iterations: l.iteration}

	case tui.ActionSubmitInput:
		// Send input response to Sprite
		requestID := l.tui.InputRequestID()
		if requestID != "" {
			commandID := fmt.Sprintf("input-%d", time.Now().UnixNano())
			_, err := l.streamClient.SendInputResponse(ctx, commandID, requestID, action.Input)
			if err != nil {
				// Log error but continue
			}
			// Clear input request ID
			l.tui.SetInputRequestID("")
		}

	case tui.ActionCancelInput:
		// User cancelled input, return to summary view
		// The NEEDS_INPUT state persists on the Sprite
	}

	return Result{Reason: ExitReasonUnknown}
}

// syncStateFromSprite syncs state files from Sprite to local storage.
func (l *Loop) syncStateFromSprite(ctx context.Context) {
	if err := l.sync.SyncFromSprite(ctx, l.session.SpriteName, l.session.Branch); err != nil {
		// Non-fatal, log and continue
	}
}

// getStartingIteration returns the iteration number to start from.
func (l *Loop) getStartingIteration() int {
	history, err := l.store.LoadHistory(l.session.Branch)
	if err != nil || len(history) == 0 {
		return 0
	}
	return history[len(history)-1].Iteration
}

// broadcastClaudeEvent broadcasts a Claude event to web clients.
func (l *Loop) broadcastClaudeEvent(event *stream.Event) {
	if l.server == nil {
		return
	}

	streams := l.server.Streams()
	if streams == nil {
		return
	}

	data, err := event.ClaudeEventData()
	if err != nil {
		return
	}

	webEvent := &server.ClaudeEvent{
		ID:        data.ID,
		SessionID: data.SessionID,
		Iteration: data.Iteration,
		Sequence:  data.Sequence,
		Message:   data.Message,
		Timestamp: data.Timestamp.Format(time.RFC3339),
	}

	streams.BroadcastClaudeEvent(webEvent)
}

// broadcastSession broadcasts session state to web clients.
func (l *Loop) broadcastSession(data *stream.SessionEvent) {
	if l.server == nil {
		return
	}

	streams := l.server.Streams()
	if streams == nil {
		return
	}

	// Map stream status to server status
	var status server.SessionStatus
	switch data.Status {
	case stream.SessionStatusDone:
		status = server.SessionStatusDone
	case stream.SessionStatusNeedsInput:
		status = server.SessionStatusNeedsInput
	case stream.SessionStatusBlocked:
		status = server.SessionStatusBlocked
	default:
		status = server.SessionStatusRunning
	}

	session := &server.Session{
		ID:        data.ID,
		Repo:      data.Repo,
		Branch:    data.Branch,
		Spec:      data.Spec,
		Status:    status,
		Iteration: data.Iteration,
		StartedAt: data.StartedAt.Format(time.RFC3339),
	}

	streams.BroadcastSession(session)
}

// broadcastInputRequest broadcasts an input request to web clients.
func (l *Loop) broadcastInputRequest(data *stream.InputRequestEvent) {
	if l.server == nil {
		return
	}

	streams := l.server.Streams()
	if streams == nil {
		return
	}

	// Convert stream.InputRequest to server.InputRequest
	// In State Protocol, input_request events are always pending (not responded)
	req := &server.InputRequest{
		ID:        data.ID,
		SessionID: data.SessionID,
		Iteration: data.Iteration,
		Question:  data.Question,
		Responded: false,
		Response:  nil,
	}

	streams.BroadcastInputRequest(req)
}

// Sentinel errors for user actions (for compatibility).
var (
	errUserKill       = errors.New("user killed session")
	errUserBackground = errors.New("user backgrounded session")
)
