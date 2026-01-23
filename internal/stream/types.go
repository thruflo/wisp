// Package stream provides shared types and utilities for durable stream
// communication between wisp-sprite (on the Sprite VM) and clients (TUI/web).
//
// This package implements types compatible with the durable-streams State Protocol
// (https://github.com/durable-streams/durable-streams). Events follow the State
// Protocol schema with type, key, value, and headers fields.
package stream

import (
	"encoding/json"
	"fmt"
	"time"
)

// MessageType identifies the type/collection of an entity in the stream.
// This corresponds to the "type" field in the State Protocol.
type MessageType string

const (
	// Sprite → Client entity types (collections)

	// MessageTypeSession is a session state entity.
	MessageTypeSession MessageType = "session"
	// MessageTypeTask is a task state entity.
	MessageTypeTask MessageType = "task"
	// MessageTypeClaudeEvent is a Claude output event.
	MessageTypeClaudeEvent MessageType = "claude_event"
	// MessageTypeInputRequest is a request for user input.
	MessageTypeInputRequest MessageType = "input_request"
	// MessageTypeInputResponse is a response to an input request.
	MessageTypeInputResponse MessageType = "input_response"
	// MessageTypeAck is a command acknowledgment.
	MessageTypeAck MessageType = "ack"

	// Client → Sprite command types

	// MessageTypeCommand is a command from client to Sprite.
	MessageTypeCommand MessageType = "command"
)

// Operation indicates the CRUD operation for a State Protocol event.
type Operation string

const (
	// OperationInsert indicates a new entity was created.
	OperationInsert Operation = "insert"
	// OperationUpdate indicates an existing entity was modified.
	OperationUpdate Operation = "update"
	// OperationDelete indicates an entity was removed.
	OperationDelete Operation = "delete"
)

// Headers contains metadata for a State Protocol event.
type Headers struct {
	// Operation indicates the CRUD operation (insert, update, delete).
	Operation Operation `json:"operation"`
	// TxID is an optional transaction identifier for grouping related changes.
	TxID string `json:"txid,omitempty"`
	// Timestamp is when the event was created (ISO 8601 format).
	Timestamp time.Time `json:"timestamp"`
}

// Event represents a message in the durable stream following the State Protocol.
// Events are serialized to JSON for storage and transmission.
//
// State Protocol format:
//
//	{
//	  "type": "session",
//	  "key": "session:abc123",
//	  "value": { ... entity data ... },
//	  "headers": { "operation": "insert", "timestamp": "..." }
//	}
type Event struct {
	// Seq is the sequence number assigned by the FileStore.
	// This is a wisp-specific extension for catch-up/resume support.
	// Zero for events not yet persisted.
	Seq uint64 `json:"seq,omitempty"`

	// Type identifies the entity collection (e.g., "session", "task").
	Type MessageType `json:"type"`

	// Key is the unique identifier for the entity (e.g., "session:abc123").
	Key string `json:"key"`

	// Value contains the entity data.
	// Use the typed accessor methods to get the concrete type.
	Value json.RawMessage `json:"value"`

	// Headers contains operation metadata (operation, txid, timestamp).
	Headers Headers `json:"headers"`
}

// NewEvent creates a new Event with the given type, key, and value.
// The operation defaults to "insert" for new events.
func NewEvent(msgType MessageType, key string, value any) (*Event, error) {
	return NewEventWithOp(msgType, key, value, OperationInsert)
}

// NewEventWithOp creates a new Event with a specific operation.
func NewEventWithOp(msgType MessageType, key string, value any, op Operation) (*Event, error) {
	valueBytes, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal event value: %w", err)
	}

	return &Event{
		Type:  msgType,
		Key:   key,
		Value: valueBytes,
		Headers: Headers{
			Operation: op,
			Timestamp: time.Now().UTC(),
		},
	}, nil
}

// MustNewEvent creates a new Event, panicking on error.
// Use only when the value is known to be serializable.
func MustNewEvent(msgType MessageType, key string, value any) *Event {
	e, err := NewEvent(msgType, key, value)
	if err != nil {
		panic(err)
	}
	return e
}

// Marshal serializes the event to JSON bytes.
func (e *Event) Marshal() ([]byte, error) {
	return json.Marshal(e)
}

// UnmarshalEvent deserializes an Event from JSON bytes.
func UnmarshalEvent(data []byte) (*Event, error) {
	var e Event
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("failed to unmarshal event: %w", err)
	}
	return &e, nil
}

// SessionData returns the session data if this is a session event.
func (e *Event) SessionData() (*Session, error) {
	if e.Type != MessageTypeSession {
		return nil, fmt.Errorf("event is not a session event: %s", e.Type)
	}
	var data Session
	if err := json.Unmarshal(e.Value, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal session data: %w", err)
	}
	return &data, nil
}

// TaskData returns the task data if this is a task event.
func (e *Event) TaskData() (*Task, error) {
	if e.Type != MessageTypeTask {
		return nil, fmt.Errorf("event is not a task event: %s", e.Type)
	}
	var data Task
	if err := json.Unmarshal(e.Value, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal task data: %w", err)
	}
	return &data, nil
}

// ClaudeEventData returns the Claude event data if this is a claude_event.
func (e *Event) ClaudeEventData() (*ClaudeEvent, error) {
	if e.Type != MessageTypeClaudeEvent {
		return nil, fmt.Errorf("event is not a claude_event: %s", e.Type)
	}
	var data ClaudeEvent
	if err := json.Unmarshal(e.Value, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal claude_event data: %w", err)
	}
	return &data, nil
}

// InputRequestData returns the input request data if this is an input_request.
func (e *Event) InputRequestData() (*InputRequest, error) {
	if e.Type != MessageTypeInputRequest {
		return nil, fmt.Errorf("event is not an input_request: %s", e.Type)
	}
	var data InputRequest
	if err := json.Unmarshal(e.Value, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal input_request data: %w", err)
	}
	return &data, nil
}

// InputResponseData returns the input response data if this is an input_response.
func (e *Event) InputResponseData() (*InputResponse, error) {
	if e.Type != MessageTypeInputResponse {
		return nil, fmt.Errorf("event is not an input_response: %s", e.Type)
	}
	var data InputResponse
	if err := json.Unmarshal(e.Value, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal input_response data: %w", err)
	}
	return &data, nil
}

// CommandData returns the command data if this is a command event.
func (e *Event) CommandData() (*Command, error) {
	if e.Type != MessageTypeCommand {
		return nil, fmt.Errorf("event is not a command: %s", e.Type)
	}
	var data Command
	if err := json.Unmarshal(e.Value, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal command data: %w", err)
	}
	return &data, nil
}

// AckData returns the ack data if this is an ack event.
func (e *Event) AckData() (*Ack, error) {
	if e.Type != MessageTypeAck {
		return nil, fmt.Errorf("event is not an ack: %s", e.Type)
	}
	var data Ack
	if err := json.Unmarshal(e.Value, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal ack data: %w", err)
	}
	return &data, nil
}

// SessionStatus represents the status of a session.
type SessionStatus string

const (
	SessionStatusRunning    SessionStatus = "running"
	SessionStatusNeedsInput SessionStatus = "needs_input"
	SessionStatusBlocked    SessionStatus = "blocked"
	SessionStatusDone       SessionStatus = "done"
	SessionStatusPaused     SessionStatus = "paused"
)

// Session contains session state information.
// This is the value type for "session" events in the State Protocol.
type Session struct {
	ID        string        `json:"id"`
	Repo      string        `json:"repo"`
	Branch    string        `json:"branch"`
	Spec      string        `json:"spec"`
	Status    SessionStatus `json:"status"`
	Iteration int           `json:"iteration"`
	StartedAt time.Time     `json:"started_at"`
}

// TaskStatus represents the status of a task.
type TaskStatus string

const (
	TaskStatusPending    TaskStatus = "pending"
	TaskStatusInProgress TaskStatus = "in_progress"
	TaskStatusCompleted  TaskStatus = "completed"
)

// Task contains task state information.
// This is the value type for "task" events in the State Protocol.
type Task struct {
	ID          string     `json:"id"`
	SessionID   string     `json:"session_id"`
	Order       int        `json:"order"`
	Category    string     `json:"category"`
	Description string     `json:"description"`
	Status      TaskStatus `json:"status"`
}

// ClaudeEvent contains Claude output event data.
// This is the value type for "claude_event" events in the State Protocol.
type ClaudeEvent struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Iteration int    `json:"iteration"`
	Sequence  int    `json:"sequence"`
	// Message contains the raw SDK message from Claude stream-json output.
	// This is typed as any to preserve the original structure.
	Message   any       `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

// InputRequest contains a request for user input.
// This is the value type for "input_request" events in the State Protocol.
type InputRequest struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Iteration int    `json:"iteration"`
	Question  string `json:"question"`
}

// InputResponse contains a response to an input request.
// This is the value type for "input_response" events in the State Protocol.
// InputResponse is stored as a durable event, enabling transaction confirmation
// via txid and awaitTxId() per the State Protocol mutation pattern.
type InputResponse struct {
	ID        string `json:"id"`
	RequestID string `json:"request_id"`
	Response  string `json:"response"`
}

// CommandType identifies the type of command sent from client to Sprite.
type CommandType string

const (
	// CommandTypeKill stops the loop and optionally deletes the Sprite.
	CommandTypeKill CommandType = "kill"
	// CommandTypeBackground pauses the loop but keeps the Sprite alive.
	CommandTypeBackground CommandType = "background"
)

// Command represents a command from client to Sprite.
// This is the value type for "command" events in the State Protocol.
type Command struct {
	// ID is a unique identifier for this command, used for acknowledgment.
	ID string `json:"id"`

	// Type identifies what kind of command this is.
	Type CommandType `json:"type"`

	// Payload contains type-specific command data.
	// For kill, this may contain a KillPayload with options.
	Payload json.RawMessage `json:"payload,omitempty"`
}

// KillPayload is the payload for kill commands.
type KillPayload struct {
	// DeleteSprite indicates whether to delete the Sprite after stopping.
	DeleteSprite bool `json:"delete_sprite"`
}

// NewKillCommand creates a new kill command.
func NewKillCommand(id string, deleteSprite bool) (*Command, error) {
	payload, err := json.Marshal(KillPayload{
		DeleteSprite: deleteSprite,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal kill payload: %w", err)
	}
	return &Command{
		ID:      id,
		Type:    CommandTypeKill,
		Payload: payload,
	}, nil
}

// NewBackgroundCommand creates a new background command.
func NewBackgroundCommand(id string) *Command {
	return &Command{
		ID:   id,
		Type: CommandTypeBackground,
	}
}

// KillPayloadData returns the kill payload.
func (c *Command) KillPayloadData() (*KillPayload, error) {
	if c.Type != CommandTypeKill {
		return nil, fmt.Errorf("command is not kill: %s", c.Type)
	}
	// Payload may be empty for kill commands
	if len(c.Payload) == 0 {
		return &KillPayload{}, nil
	}
	var payload KillPayload
	if err := json.Unmarshal(c.Payload, &payload); err != nil {
		return nil, fmt.Errorf("failed to unmarshal kill payload: %w", err)
	}
	return &payload, nil
}

// AckStatus represents the result of command processing.
type AckStatus string

const (
	AckStatusSuccess AckStatus = "success"
	AckStatusError   AckStatus = "error"
)

// Ack represents an acknowledgment of a command.
// This is the value type for "ack" events in the State Protocol.
type Ack struct {
	// CommandID is the ID of the command being acknowledged.
	CommandID string `json:"command_id"`
	// Status indicates whether the command succeeded or failed.
	Status AckStatus `json:"status"`
	// Error contains the error message if Status is "error".
	Error string `json:"error,omitempty"`
}

// NewSuccessAck creates a success acknowledgment.
func NewSuccessAck(commandID string) *Ack {
	return &Ack{
		CommandID: commandID,
		Status:    AckStatusSuccess,
	}
}

// NewErrorAck creates an error acknowledgment.
func NewErrorAck(commandID string, err error) *Ack {
	return &Ack{
		CommandID: commandID,
		Status:    AckStatusError,
		Error:     err.Error(),
	}
}

// Helper functions for creating events with proper keys

// NewSessionEvent creates a session event with the proper key format.
func NewSessionEvent(session *Session) (*Event, error) {
	return NewEvent(MessageTypeSession, "session:"+session.ID, session)
}

// NewSessionEventWithOp creates a session event with a specific operation.
func NewSessionEventWithOp(session *Session, op Operation) (*Event, error) {
	return NewEventWithOp(MessageTypeSession, "session:"+session.ID, session, op)
}

// NewTaskEvent creates a task event with the proper key format.
func NewTaskEvent(task *Task) (*Event, error) {
	return NewEvent(MessageTypeTask, "task:"+task.ID, task)
}

// NewTaskEventWithOp creates a task event with a specific operation.
func NewTaskEventWithOp(task *Task, op Operation) (*Event, error) {
	return NewEventWithOp(MessageTypeTask, "task:"+task.ID, task, op)
}

// NewClaudeEventEvent creates a claude_event event with the proper key format.
func NewClaudeEventEvent(ce *ClaudeEvent) (*Event, error) {
	return NewEvent(MessageTypeClaudeEvent, "claude_event:"+ce.ID, ce)
}

// NewInputRequestEvent creates an input_request event with the proper key format.
func NewInputRequestEvent(ir *InputRequest) (*Event, error) {
	return NewEvent(MessageTypeInputRequest, "input_request:"+ir.ID, ir)
}

// NewInputResponseEvent creates an input_response event with the proper key format.
func NewInputResponseEvent(ir *InputResponse) (*Event, error) {
	return NewEvent(MessageTypeInputResponse, "input_response:"+ir.ID, ir)
}

// NewCommandEvent creates a command event with the proper key format.
func NewCommandEvent(cmd *Command) (*Event, error) {
	return NewEvent(MessageTypeCommand, "command:"+cmd.ID, cmd)
}

// NewAckEvent creates an ack event with the proper key format.
func NewAckEvent(ack *Ack) (*Event, error) {
	return NewEvent(MessageTypeAck, "ack:"+ack.CommandID, ack)
}

// Legacy compatibility aliases
// These are deprecated and will be removed in a future version.

// SessionEvent is an alias for Session for backward compatibility.
// Deprecated: Use Session instead.
type SessionEvent = Session

// TaskEvent is an alias for Task for backward compatibility.
// Deprecated: Use Task instead.
type TaskEvent = Task

// InputRequestEvent is an alias for InputRequest for backward compatibility.
// Deprecated: Use InputRequest instead.
type InputRequestEvent = InputRequest
