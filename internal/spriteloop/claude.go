// Package spriteloop provides the iteration loop logic for wisp-sprite.
//
// This file contains types and functions for parsing Claude Code stream-json output.
// When Claude runs with --output-format stream-json, it emits newline-delimited JSON
// events that describe the conversation progress.
package spriteloop

import (
	"encoding/json"
	"fmt"
)

// EventType identifies the type of Claude stream-json event.
type EventType string

const (
	// EventTypeSystem is a system event (e.g., init, result).
	EventTypeSystem EventType = "system"
	// EventTypeAssistant is an assistant message (text or tool use).
	EventTypeAssistant EventType = "assistant"
	// EventTypeUser is a user message (typically tool results).
	EventTypeUser EventType = "user"
	// EventTypeResult is the final result event when Claude completes.
	EventTypeResult EventType = "result"
)

// ContentType identifies the type of content in a message.
type ContentType string

const (
	ContentTypeText       ContentType = "text"
	ContentTypeToolUse    ContentType = "tool_use"
	ContentTypeToolResult ContentType = "tool_result"
)

// StreamEvent represents a parsed Claude stream-json event.
// The actual structure varies by event type.
type StreamEvent struct {
	// Type is the event type (system, assistant, user, result).
	Type EventType `json:"type"`

	// Subtype is present for system events (init, result subtypes).
	Subtype string `json:"subtype,omitempty"`

	// SessionID is the Claude session ID.
	SessionID string `json:"session_id,omitempty"`

	// Message is present for assistant and user events.
	Message *Message `json:"message,omitempty"`

	// Result fields (present when Type == "result")
	CostUSD  float64 `json:"cost_usd,omitempty"`
	NumTurns int     `json:"num_turns,omitempty"`

	// Tools is present in init events.
	Tools []string `json:"tools,omitempty"`
}

// Message represents a Claude message with content blocks.
type Message struct {
	Content []ContentBlock `json:"content"`
}

// ContentBlock represents a piece of content in a message.
// It can be text, tool_use, or tool_result.
type ContentBlock struct {
	Type ContentType `json:"type"`

	// Text fields (when Type == "text")
	Text string `json:"text,omitempty"`

	// Tool use fields (when Type == "tool_use")
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// Tool result fields (when Type == "tool_result")
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

// ParseStreamEvent parses a line of Claude stream-json output into a StreamEvent.
// Returns nil, nil if the line is empty or not valid JSON.
func ParseStreamEvent(line string) (*StreamEvent, error) {
	if line == "" {
		return nil, nil
	}

	var event StreamEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return nil, fmt.Errorf("failed to parse stream event: %w", err)
	}

	return &event, nil
}

// IsInitEvent returns true if this is a system init event.
func (e *StreamEvent) IsInitEvent() bool {
	return e.Type == EventTypeSystem && e.Subtype == "init"
}

// IsResultEvent returns true if this is a result event (Claude finished).
func (e *StreamEvent) IsResultEvent() bool {
	return e.Type == EventTypeResult
}

// IsSuccess returns true if this is a successful result event.
func (e *StreamEvent) IsSuccess() bool {
	return e.Type == EventTypeResult && e.Subtype == "success"
}

// IsError returns true if this is an error result event.
func (e *StreamEvent) IsError() bool {
	return e.Type == EventTypeResult && e.Subtype == "error"
}

// HasToolUse returns true if this assistant event contains tool use.
func (e *StreamEvent) HasToolUse() bool {
	if e.Type != EventTypeAssistant || e.Message == nil {
		return false
	}
	for _, block := range e.Message.Content {
		if block.Type == ContentTypeToolUse {
			return true
		}
	}
	return false
}

// GetToolUses returns all tool use blocks from an assistant message.
func (e *StreamEvent) GetToolUses() []ContentBlock {
	if e.Type != EventTypeAssistant || e.Message == nil {
		return nil
	}
	var tools []ContentBlock
	for _, block := range e.Message.Content {
		if block.Type == ContentTypeToolUse {
			tools = append(tools, block)
		}
	}
	return tools
}

// GetText returns concatenated text content from a message.
func (e *StreamEvent) GetText() string {
	if e.Message == nil {
		return ""
	}
	var text string
	for _, block := range e.Message.Content {
		if block.Type == ContentTypeText {
			text += block.Text
		}
	}
	return text
}

// HasToolResult returns true if this user event contains tool results.
func (e *StreamEvent) HasToolResult() bool {
	if e.Type != EventTypeUser || e.Message == nil {
		return false
	}
	for _, block := range e.Message.Content {
		if block.Type == ContentTypeToolResult {
			return true
		}
	}
	return false
}

// GetToolResults returns all tool result blocks from a user message.
func (e *StreamEvent) GetToolResults() []ContentBlock {
	if e.Type != EventTypeUser || e.Message == nil {
		return nil
	}
	var results []ContentBlock
	for _, block := range e.Message.Content {
		if block.Type == ContentTypeToolResult {
			results = append(results, block)
		}
	}
	return results
}

// HasErrorResult returns true if any tool result is an error.
func (e *StreamEvent) HasErrorResult() bool {
	for _, result := range e.GetToolResults() {
		if result.IsError {
			return true
		}
	}
	return false
}

// StreamState tracks the state accumulated from parsing stream events.
// This is useful for monitoring Claude's progress during execution.
type StreamState struct {
	SessionID  string
	Tools      []string
	Turns      int
	ToolCalls  int
	TextBlocks int
	Errors     int
	CostUSD    float64
	Completed  bool
	Success    bool
}

// NewStreamState creates a new empty StreamState.
func NewStreamState() *StreamState {
	return &StreamState{}
}

// Update processes a stream event and updates the state accordingly.
func (s *StreamState) Update(event *StreamEvent) {
	if event == nil {
		return
	}

	switch event.Type {
	case EventTypeSystem:
		if event.IsInitEvent() {
			s.SessionID = event.SessionID
			s.Tools = event.Tools
		}

	case EventTypeAssistant:
		if event.Message != nil {
			for _, block := range event.Message.Content {
				switch block.Type {
				case ContentTypeText:
					s.TextBlocks++
				case ContentTypeToolUse:
					s.ToolCalls++
				}
			}
		}

	case EventTypeUser:
		s.Turns++
		for _, result := range event.GetToolResults() {
			if result.IsError {
				s.Errors++
			}
		}

	case EventTypeResult:
		s.Completed = true
		s.Success = event.IsSuccess()
		s.CostUSD = event.CostUSD
		if event.NumTurns > 0 {
			s.Turns = event.NumTurns
		}
	}
}

// StreamParser processes Claude stream-json output and extracts useful information.
type StreamParser struct {
	state    *StreamState
	callback func(*StreamEvent) // Optional callback for each parsed event
}

// NewStreamParser creates a new parser with an optional event callback.
func NewStreamParser(callback func(*StreamEvent)) *StreamParser {
	return &StreamParser{
		state:    NewStreamState(),
		callback: callback,
	}
}

// ParseLine parses a line and updates internal state.
// Returns the parsed event, or nil if the line couldn't be parsed.
func (p *StreamParser) ParseLine(line string) *StreamEvent {
	event, err := ParseStreamEvent(line)
	if err != nil || event == nil {
		return nil
	}

	p.state.Update(event)

	if p.callback != nil {
		p.callback(event)
	}

	return event
}

// State returns the accumulated stream state.
func (p *StreamParser) State() *StreamState {
	return p.state
}

// IsComplete returns true if a result event has been received.
func (p *StreamParser) IsComplete() bool {
	return p.state.Completed
}

// ToolInput represents the parsed input for common tools.
type ToolInput struct {
	// Common fields
	Command string `json:"command,omitempty"` // Bash
	Content string `json:"content,omitempty"` // Write, Edit

	// File operation fields
	FilePath  string `json:"file_path,omitempty"`
	OldString string `json:"old_string,omitempty"` // Edit
	NewString string `json:"new_string,omitempty"` // Edit

	// Glob/Grep fields
	Pattern string `json:"pattern,omitempty"`
	Path    string `json:"path,omitempty"`
}

// ParseToolInput attempts to parse the input JSON for a tool use block.
func (b *ContentBlock) ParseToolInput() (*ToolInput, error) {
	if b.Type != ContentTypeToolUse || len(b.Input) == 0 {
		return nil, fmt.Errorf("not a tool_use block or empty input")
	}

	var input ToolInput
	if err := json.Unmarshal(b.Input, &input); err != nil {
		return nil, fmt.Errorf("failed to parse tool input: %w", err)
	}

	return &input, nil
}
