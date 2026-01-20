// Package stream provides shared types and utilities for durable stream
// communication between wisp-sprite (on the Sprite VM) and clients (TUI/web).
package stream

import (
	"encoding/json"
	"fmt"
	"time"
)

// MessageType identifies the type of message in the stream.
type MessageType string

const (
	// Sprite → Client message types

	// MessageTypeSession is a session state update.
	MessageTypeSession MessageType = "session"
	// MessageTypeTask is a task state update.
	MessageTypeTask MessageType = "task"
	// MessageTypeClaudeEvent is a Claude output event.
	MessageTypeClaudeEvent MessageType = "claude_event"
	// MessageTypeInputRequest is a request for user input.
	MessageTypeInputRequest MessageType = "input_request"
	// MessageTypeAck is a command acknowledgment.
	MessageTypeAck MessageType = "ack"

	// Client → Sprite command types

	// MessageTypeCommand is a command from client to Sprite.
	MessageTypeCommand MessageType = "command"
)

// CommandType identifies the type of command sent from client to Sprite.
type CommandType string

const (
	// CommandTypeKill stops the loop and optionally deletes the Sprite.
	CommandTypeKill CommandType = "kill"
	// CommandTypeBackground pauses the loop but keeps the Sprite alive.
	CommandTypeBackground CommandType = "background"
	// CommandTypeInputResponse provides user input in response to an InputRequest.
	CommandTypeInputResponse CommandType = "input_response"
)

// Event represents a message in the durable stream.
// Events are serialized to JSON for storage and transmission.
type Event struct {
	// Seq is the sequence number assigned by the FileStore.
	// Zero for events not yet persisted.
	Seq uint64 `json:"seq,omitempty"`

	// Type identifies what kind of event this is.
	Type MessageType `json:"type"`

	// Timestamp is when the event was created.
	Timestamp time.Time `json:"timestamp"`

	// Data contains the type-specific payload.
	// Use the typed accessor methods to get the concrete type.
	Data json.RawMessage `json:"data"`
}

// NewEvent creates a new Event with the given type and data.
func NewEvent(msgType MessageType, data any) (*Event, error) {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal event data: %w", err)
	}

	return &Event{
		Type:      msgType,
		Timestamp: time.Now().UTC(),
		Data:      dataBytes,
	}, nil
}

// MustNewEvent creates a new Event, panicking on error.
// Use only when the data is known to be serializable.
func MustNewEvent(msgType MessageType, data any) *Event {
	e, err := NewEvent(msgType, data)
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
func (e *Event) SessionData() (*SessionEvent, error) {
	if e.Type != MessageTypeSession {
		return nil, fmt.Errorf("event is not a session event: %s", e.Type)
	}
	var data SessionEvent
	if err := json.Unmarshal(e.Data, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal session data: %w", err)
	}
	return &data, nil
}

// TaskData returns the task data if this is a task event.
func (e *Event) TaskData() (*TaskEvent, error) {
	if e.Type != MessageTypeTask {
		return nil, fmt.Errorf("event is not a task event: %s", e.Type)
	}
	var data TaskEvent
	if err := json.Unmarshal(e.Data, &data); err != nil {
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
	if err := json.Unmarshal(e.Data, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal claude_event data: %w", err)
	}
	return &data, nil
}

// InputRequestData returns the input request data if this is an input_request.
func (e *Event) InputRequestData() (*InputRequestEvent, error) {
	if e.Type != MessageTypeInputRequest {
		return nil, fmt.Errorf("event is not an input_request: %s", e.Type)
	}
	var data InputRequestEvent
	if err := json.Unmarshal(e.Data, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal input_request data: %w", err)
	}
	return &data, nil
}

// CommandData returns the command data if this is a command event.
func (e *Event) CommandData() (*Command, error) {
	if e.Type != MessageTypeCommand {
		return nil, fmt.Errorf("event is not a command: %s", e.Type)
	}
	var data Command
	if err := json.Unmarshal(e.Data, &data); err != nil {
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
	if err := json.Unmarshal(e.Data, &data); err != nil {
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

// SessionEvent contains session state information.
type SessionEvent struct {
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

// TaskEvent contains task state information.
type TaskEvent struct {
	ID          string     `json:"id"`
	SessionID   string     `json:"session_id"`
	Order       int        `json:"order"`
	Category    string     `json:"category"`
	Description string     `json:"description"`
	Status      TaskStatus `json:"status"`
}

// ClaudeEvent contains Claude output event data.
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

// InputRequestEvent contains a request for user input.
type InputRequestEvent struct {
	ID        string  `json:"id"`
	SessionID string  `json:"session_id"`
	Iteration int     `json:"iteration"`
	Question  string  `json:"question"`
	Responded bool    `json:"responded"`
	Response  *string `json:"response,omitempty"`
}

// Command represents a command from client to Sprite.
type Command struct {
	// ID is a unique identifier for this command, used for acknowledgment.
	ID string `json:"id"`

	// Type identifies what kind of command this is.
	Type CommandType `json:"type"`

	// Payload contains type-specific command data.
	// For input_response, this contains the InputResponsePayload.
	// For kill, this may contain a KillPayload with options.
	Payload json.RawMessage `json:"payload,omitempty"`
}

// InputResponsePayload is the payload for input_response commands.
type InputResponsePayload struct {
	// RequestID is the ID of the InputRequestEvent this responds to.
	RequestID string `json:"request_id"`
	// Response is the user's response text.
	Response string `json:"response"`
}

// KillPayload is the payload for kill commands.
type KillPayload struct {
	// DeleteSprite indicates whether to delete the Sprite after stopping.
	DeleteSprite bool `json:"delete_sprite"`
}

// NewInputResponseCommand creates a new input_response command.
func NewInputResponseCommand(id, requestID, response string) (*Command, error) {
	payload, err := json.Marshal(InputResponsePayload{
		RequestID: requestID,
		Response:  response,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input response payload: %w", err)
	}
	return &Command{
		ID:      id,
		Type:    CommandTypeInputResponse,
		Payload: payload,
	}, nil
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

// InputResponsePayloadData returns the input response payload.
func (c *Command) InputResponsePayloadData() (*InputResponsePayload, error) {
	if c.Type != CommandTypeInputResponse {
		return nil, fmt.Errorf("command is not input_response: %s", c.Type)
	}
	var payload InputResponsePayload
	if err := json.Unmarshal(c.Payload, &payload); err != nil {
		return nil, fmt.Errorf("failed to unmarshal input response payload: %w", err)
	}
	return &payload, nil
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
