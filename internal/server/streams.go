package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/durable-streams/durable-streams/packages/caddy-plugin/store"
	"github.com/thruflo/wisp/internal/stream"
)

const (
	// streamPath is the default path for the wisp event stream.
	streamPath = "/wisp/events"
	// streamContentType is the content type for the event stream.
	streamContentType = "application/json"
)

// MessageType identifies the type of message in the stream.
type MessageType string

const (
	// MessageTypeSession is a session state update.
	MessageTypeSession MessageType = "session"
	// MessageTypeTask is a task state update.
	MessageTypeTask MessageType = "task"
	// MessageTypeClaudeEvent is a Claude output event.
	MessageTypeClaudeEvent MessageType = "claude_event"
	// MessageTypeInputRequest is an input request.
	MessageTypeInputRequest MessageType = "input_request"
	// MessageTypeDelete indicates a deletion.
	MessageTypeDelete MessageType = "delete"
)

// StreamMessage represents a message in the durable stream.
type StreamMessage struct {
	Type MessageType `json:"type"`
	Data any         `json:"data,omitempty"`
	// For delete operations:
	Collection string `json:"collection,omitempty"`
	ID         string `json:"id,omitempty"`
}

// Session represents a wisp session for streaming to clients.
type Session struct {
	ID        string        `json:"id"`
	Repo      string        `json:"repo"`
	Branch    string        `json:"branch"`
	Spec      string        `json:"spec"`
	Status    SessionStatus `json:"status"`
	Iteration int           `json:"iteration"`
	StartedAt string        `json:"started_at"`
}

// SessionStatus represents the status of a session.
type SessionStatus string

const (
	SessionStatusRunning    SessionStatus = "running"
	SessionStatusNeedsInput SessionStatus = "needs_input"
	SessionStatusBlocked    SessionStatus = "blocked"
	SessionStatusDone       SessionStatus = "done"
)

// Task represents a task within a session.
type Task struct {
	ID        string     `json:"id"`
	SessionID string     `json:"session_id"`
	Order     int        `json:"order"`
	Content   string     `json:"content"`
	Status    TaskStatus `json:"status"`
}

// TaskStatus represents the status of a task.
type TaskStatus string

const (
	TaskStatusPending    TaskStatus = "pending"
	TaskStatusInProgress TaskStatus = "in_progress"
	TaskStatusCompleted  TaskStatus = "completed"
)

// ClaudeEvent represents a Claude output event.
// The Message field contains the raw SDK message (assistant, result, system, user).
type ClaudeEvent struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Iteration int    `json:"iteration"`
	Sequence  int    `json:"sequence"`
	Message   any    `json:"message"` // Raw SDKMessage from Claude stream-json output
	Timestamp string `json:"timestamp"`
}

// InputRequest represents a request for user input.
type InputRequest struct {
	ID        string  `json:"id"`
	SessionID string  `json:"session_id"`
	Iteration int     `json:"iteration"`
	Question  string  `json:"question"`
	Responded bool    `json:"responded"`
	Response  *string `json:"response"` // nil if not responded
}

// StreamManager wraps a MemoryStore for managing the event stream.
// It can operate in two modes:
// 1. Local mode: Events are stored locally (for testing or single-machine setup)
// 2. Relay mode: Events are relayed from a remote Sprite via StreamClient
type StreamManager struct {
	store *store.MemoryStore
	mu    sync.RWMutex

	// Track current state for initial sync
	sessions      map[string]*Session
	tasks         map[string]*Task
	inputRequests map[string]*InputRequest

	// Relay mode: connection to Sprite stream server
	spriteClient *stream.StreamClient
	relayCancel  context.CancelFunc
	relayWg      sync.WaitGroup
}

// NewStreamManager creates a new StreamManager with an initialized MemoryStore.
// This creates the manager in local mode (events stored locally).
func NewStreamManager() (*StreamManager, error) {
	memStore := store.NewMemoryStore()

	// Create the stream
	_, _, err := memStore.Create(streamPath, store.CreateOptions{
		ContentType: streamContentType,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create stream: %w", err)
	}

	return &StreamManager{
		store:         memStore,
		sessions:      make(map[string]*Session),
		tasks:         make(map[string]*Task),
		inputRequests: make(map[string]*InputRequest),
	}, nil
}

// NewRelayStreamManager creates a StreamManager that relays events from a Sprite.
// The spriteURL should be the base URL of the Sprite's stream server (e.g., "http://localhost:8374").
// The authToken is optional authentication for the Sprite connection.
func NewRelayStreamManager(spriteURL, authToken string) (*StreamManager, error) {
	memStore := store.NewMemoryStore()

	// Create the stream
	_, _, err := memStore.Create(streamPath, store.CreateOptions{
		ContentType: streamContentType,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create stream: %w", err)
	}

	// Create options for the stream client
	var opts []stream.ClientOption
	if authToken != "" {
		opts = append(opts, stream.WithAuthToken(authToken))
	}

	client := stream.NewStreamClient(spriteURL, opts...)

	sm := &StreamManager{
		store:         memStore,
		sessions:      make(map[string]*Session),
		tasks:         make(map[string]*Task),
		inputRequests: make(map[string]*InputRequest),
		spriteClient:  client,
	}

	return sm, nil
}

// StartRelay starts the relay loop that forwards events from the Sprite to local clients.
// This should be called after creating a relay StreamManager.
// The relay runs in the background until StopRelay or Close is called.
func (sm *StreamManager) StartRelay(ctx context.Context) error {
	if sm.spriteClient == nil {
		return errors.New("not in relay mode: no sprite client configured")
	}

	// Test connection
	if err := sm.spriteClient.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to sprite: %w", err)
	}

	// Get initial state from Sprite
	state, err := sm.spriteClient.GetState(ctx)
	if err != nil {
		return fmt.Errorf("failed to get initial state: %w", err)
	}

	// Populate local state from snapshot
	if err := sm.populateFromSnapshot(state); err != nil {
		return fmt.Errorf("failed to populate initial state: %w", err)
	}

	// Create cancelable context for relay
	relayCtx, cancel := context.WithCancel(ctx)
	sm.relayCancel = cancel

	// Start relay goroutine
	sm.relayWg.Add(1)
	go sm.relayLoop(relayCtx, state.LastSeq)

	return nil
}

// StopRelay stops the relay loop.
func (sm *StreamManager) StopRelay() {
	if sm.relayCancel != nil {
		sm.relayCancel()
		sm.relayWg.Wait()
		sm.relayCancel = nil
	}
}

// populateFromSnapshot initializes local state from a Sprite state snapshot.
func (sm *StreamManager) populateFromSnapshot(state *stream.StateSnapshot) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Convert and store session
	if state.Session != nil {
		session := convertSessionEventToSession(state.Session)
		sm.sessions[session.ID] = session
		if err := sm.appendUnlocked(StreamMessage{
			Type: MessageTypeSession,
			Data: session,
		}); err != nil {
			return err
		}
	}

	// Convert and store tasks
	for _, taskEvent := range state.Tasks {
		task := convertTaskEventToTask(taskEvent)
		sm.tasks[task.ID] = task
		if err := sm.appendUnlocked(StreamMessage{
			Type: MessageTypeTask,
			Data: task,
		}); err != nil {
			return err
		}
	}

	// Convert and store input request
	if state.InputRequest != nil {
		inputReq := convertInputRequestEventToInputRequest(state.InputRequest)
		sm.inputRequests[inputReq.ID] = inputReq
		if err := sm.appendUnlocked(StreamMessage{
			Type: MessageTypeInputRequest,
			Data: inputReq,
		}); err != nil {
			return err
		}
	}

	return nil
}

// relayLoop continuously reads events from the Sprite and broadcasts them locally.
func (sm *StreamManager) relayLoop(ctx context.Context, fromSeq uint64) {
	defer sm.relayWg.Done()

	eventCh, errCh := sm.spriteClient.Subscribe(ctx, fromSeq+1)

	for {
		select {
		case <-ctx.Done():
			return
		case err := <-errCh:
			if err != nil && ctx.Err() == nil {
				// Log error but don't crash - client will attempt reconnection
				// In production, this would log properly
				_ = err
			}
			return
		case event, ok := <-eventCh:
			if !ok {
				return
			}
			sm.handleRelayedEvent(event)
		}
	}
}

// handleRelayedEvent processes an event received from the Sprite and broadcasts it locally.
func (sm *StreamManager) handleRelayedEvent(event *stream.Event) {
	switch event.Type {
	case stream.MessageTypeSession:
		sessionData, err := event.SessionData()
		if err != nil {
			return
		}
		session := convertSessionEventToSession(sessionData)
		_ = sm.BroadcastSession(session)

	case stream.MessageTypeTask:
		taskData, err := event.TaskData()
		if err != nil {
			return
		}
		task := convertTaskEventToTask(taskData)
		_ = sm.BroadcastTask(task)

	case stream.MessageTypeClaudeEvent:
		claudeData, err := event.ClaudeEventData()
		if err != nil {
			return
		}
		claudeEvent := convertClaudeEventToClaudeEvent(claudeData)
		_ = sm.BroadcastClaudeEvent(claudeEvent)

	case stream.MessageTypeInputRequest:
		inputData, err := event.InputRequestData()
		if err != nil {
			return
		}
		inputReq := convertInputRequestEventToInputRequest(inputData)
		_ = sm.BroadcastInputRequest(inputReq)

	case stream.MessageTypeAck:
		// Ack events are not relayed to web clients directly
		// They are handled by the command sender
	}
}

// convertSessionEventToSession converts a stream.SessionEvent to a server.Session.
func convertSessionEventToSession(se *stream.SessionEvent) *Session {
	return &Session{
		ID:        se.ID,
		Repo:      se.Repo,
		Branch:    se.Branch,
		Spec:      se.Spec,
		Status:    SessionStatus(se.Status),
		Iteration: se.Iteration,
		StartedAt: se.StartedAt.Format(time.RFC3339),
	}
}

// convertTaskEventToTask converts a stream.TaskEvent to a server.Task.
func convertTaskEventToTask(te *stream.TaskEvent) *Task {
	return &Task{
		ID:        te.ID,
		SessionID: te.SessionID,
		Order:     te.Order,
		Content:   te.Description,
		Status:    TaskStatus(te.Status),
	}
}

// convertClaudeEventToClaudeEvent converts a stream.ClaudeEvent to a server.ClaudeEvent.
func convertClaudeEventToClaudeEvent(ce *stream.ClaudeEvent) *ClaudeEvent {
	return &ClaudeEvent{
		ID:        ce.ID,
		SessionID: ce.SessionID,
		Iteration: ce.Iteration,
		Sequence:  ce.Sequence,
		Message:   ce.Message,
		Timestamp: ce.Timestamp.Format(time.RFC3339),
	}
}

// convertInputRequestEventToInputRequest converts a stream.InputRequestEvent to a server.InputRequest.
func convertInputRequestEventToInputRequest(ire *stream.InputRequestEvent) *InputRequest {
	return &InputRequest{
		ID:        ire.ID,
		SessionID: ire.SessionID,
		Iteration: ire.Iteration,
		Question:  ire.Question,
		Responded: ire.Responded,
		Response:  ire.Response,
	}
}

// Store returns the underlying MemoryStore.
func (sm *StreamManager) Store() *store.MemoryStore {
	return sm.store
}

// StreamPath returns the path for the event stream.
func (sm *StreamManager) StreamPath() string {
	return streamPath
}

// append serializes and appends a message to the stream.
func (sm *StreamManager) append(msg StreamMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	_, err = sm.store.Append(streamPath, data, store.AppendOptions{})
	if err != nil {
		return fmt.Errorf("failed to append message: %w", err)
	}

	return nil
}

// appendUnlocked is like append but doesn't acquire any locks.
// Caller must hold sm.mu.Lock().
func (sm *StreamManager) appendUnlocked(msg StreamMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	_, err = sm.store.Append(streamPath, data, store.AppendOptions{})
	if err != nil {
		return fmt.Errorf("failed to append message: %w", err)
	}

	return nil
}

// BroadcastSession broadcasts a session state update.
func (sm *StreamManager) BroadcastSession(session *Session) error {
	if session == nil {
		return errors.New("session is nil")
	}

	sm.mu.Lock()
	sm.sessions[session.ID] = session
	sm.mu.Unlock()

	return sm.append(StreamMessage{
		Type: MessageTypeSession,
		Data: session,
	})
}

// BroadcastTask broadcasts a task state update.
func (sm *StreamManager) BroadcastTask(task *Task) error {
	if task == nil {
		return errors.New("task is nil")
	}

	sm.mu.Lock()
	sm.tasks[task.ID] = task
	sm.mu.Unlock()

	return sm.append(StreamMessage{
		Type: MessageTypeTask,
		Data: task,
	})
}

// BroadcastClaudeEvent broadcasts a Claude output event.
func (sm *StreamManager) BroadcastClaudeEvent(event *ClaudeEvent) error {
	if event == nil {
		return errors.New("event is nil")
	}

	// Claude events are not tracked for initial sync (too many)
	return sm.append(StreamMessage{
		Type: MessageTypeClaudeEvent,
		Data: event,
	})
}

// BroadcastInputRequest broadcasts an input request.
func (sm *StreamManager) BroadcastInputRequest(req *InputRequest) error {
	if req == nil {
		return errors.New("input request is nil")
	}

	sm.mu.Lock()
	sm.inputRequests[req.ID] = req
	sm.mu.Unlock()

	return sm.append(StreamMessage{
		Type: MessageTypeInputRequest,
		Data: req,
	})
}

// BroadcastDelete broadcasts a deletion message.
func (sm *StreamManager) BroadcastDelete(collection, id string) error {
	if collection == "" || id == "" {
		return errors.New("collection and id are required")
	}

	// Remove from tracked state
	sm.mu.Lock()
	switch collection {
	case "sessions":
		delete(sm.sessions, id)
	case "tasks":
		delete(sm.tasks, id)
	case "input_requests":
		delete(sm.inputRequests, id)
	}
	sm.mu.Unlock()

	return sm.append(StreamMessage{
		Type:       MessageTypeDelete,
		Collection: collection,
		ID:         id,
	})
}

// GetCurrentOffset returns the current tail offset of the stream.
func (sm *StreamManager) GetCurrentOffset() (store.Offset, error) {
	return sm.store.GetCurrentOffset(streamPath)
}

// Read reads messages from the stream starting at the given offset.
func (sm *StreamManager) Read(offset store.Offset) ([]store.Message, bool, error) {
	return sm.store.Read(streamPath, offset)
}

// WaitForMessages waits for new messages after the given offset.
func (sm *StreamManager) WaitForMessages(ctx context.Context, offset store.Offset, timeout time.Duration) ([]store.Message, bool, error) {
	return sm.store.WaitForMessages(ctx, streamPath, offset, timeout)
}

// GetCurrentState returns all currently tracked state for initial sync.
func (sm *StreamManager) GetCurrentState() (sessions []*Session, tasks []*Task, inputRequests []*InputRequest) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	sessions = make([]*Session, 0, len(sm.sessions))
	for _, s := range sm.sessions {
		sessions = append(sessions, s)
	}

	tasks = make([]*Task, 0, len(sm.tasks))
	for _, t := range sm.tasks {
		tasks = append(tasks, t)
	}

	inputRequests = make([]*InputRequest, 0, len(sm.inputRequests))
	for _, r := range sm.inputRequests {
		inputRequests = append(inputRequests, r)
	}

	return
}

// Close releases resources held by the StreamManager.
func (sm *StreamManager) Close() error {
	// Stop relay if running
	sm.StopRelay()

	return sm.store.Close()
}

// SpriteClient returns the underlying StreamClient for relay mode.
// Returns nil if not in relay mode.
func (sm *StreamManager) SpriteClient() *stream.StreamClient {
	return sm.spriteClient
}

// IsRelayMode returns true if the StreamManager is in relay mode.
func (sm *StreamManager) IsRelayMode() bool {
	return sm.spriteClient != nil
}

// SendCommandToSprite forwards a command to the Sprite.
// This only works in relay mode.
func (sm *StreamManager) SendCommandToSprite(ctx context.Context, cmd *stream.Command) (*stream.Ack, error) {
	if sm.spriteClient == nil {
		return nil, errors.New("not in relay mode: no sprite client configured")
	}
	return sm.spriteClient.SendCommand(ctx, cmd)
}

// SendInputResponseToSprite sends an input response to the Sprite.
// This only works in relay mode.
func (sm *StreamManager) SendInputResponseToSprite(ctx context.Context, commandID, requestID, response string) (*stream.Ack, error) {
	if sm.spriteClient == nil {
		return nil, errors.New("not in relay mode: no sprite client configured")
	}
	return sm.spriteClient.SendInputResponse(ctx, commandID, requestID, response)
}

// SendKillCommandToSprite sends a kill command to the Sprite.
// This only works in relay mode.
func (sm *StreamManager) SendKillCommandToSprite(ctx context.Context, commandID string, deleteSprite bool) (*stream.Ack, error) {
	if sm.spriteClient == nil {
		return nil, errors.New("not in relay mode: no sprite client configured")
	}
	return sm.spriteClient.SendKillCommand(ctx, commandID, deleteSprite)
}

// SendBackgroundCommandToSprite sends a background command to the Sprite.
// This only works in relay mode.
func (sm *StreamManager) SendBackgroundCommandToSprite(ctx context.Context, commandID string) (*stream.Ack, error) {
	if sm.spriteClient == nil {
		return nil, errors.New("not in relay mode: no sprite client configured")
	}
	return sm.spriteClient.SendBackgroundCommand(ctx, commandID)
}
