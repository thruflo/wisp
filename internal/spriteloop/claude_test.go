package spriteloop

import (
	"testing"
)

func TestParseStreamEvent(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantNil bool
		wantErr bool
		check   func(t *testing.T, e *StreamEvent)
	}{
		{
			name:    "empty line",
			line:    "",
			wantNil: true,
		},
		{
			name:    "invalid json",
			line:    "{invalid",
			wantErr: true,
		},
		{
			name: "system init event",
			line: `{"type":"system","subtype":"init","session_id":"abc123","tools":["Bash","Read","Edit"]}`,
			check: func(t *testing.T, e *StreamEvent) {
				if e.Type != EventTypeSystem {
					t.Errorf("Type = %q, want %q", e.Type, EventTypeSystem)
				}
				if e.Subtype != "init" {
					t.Errorf("Subtype = %q, want %q", e.Subtype, "init")
				}
				if e.SessionID != "abc123" {
					t.Errorf("SessionID = %q, want %q", e.SessionID, "abc123")
				}
				if len(e.Tools) != 3 {
					t.Errorf("len(Tools) = %d, want 3", len(e.Tools))
				}
				if !e.IsInitEvent() {
					t.Error("expected IsInitEvent() to be true")
				}
			},
		},
		{
			name: "assistant text message",
			line: `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello, world!"}]}}`,
			check: func(t *testing.T, e *StreamEvent) {
				if e.Type != EventTypeAssistant {
					t.Errorf("Type = %q, want %q", e.Type, EventTypeAssistant)
				}
				if e.Message == nil {
					t.Fatal("Message is nil")
				}
				if len(e.Message.Content) != 1 {
					t.Errorf("len(Content) = %d, want 1", len(e.Message.Content))
				}
				if e.GetText() != "Hello, world!" {
					t.Errorf("GetText() = %q, want %q", e.GetText(), "Hello, world!")
				}
				if e.HasToolUse() {
					t.Error("expected HasToolUse() to be false")
				}
			},
		},
		{
			name: "assistant tool use",
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_123","name":"Bash","input":{"command":"ls -la"}}]}}`,
			check: func(t *testing.T, e *StreamEvent) {
				if e.Type != EventTypeAssistant {
					t.Errorf("Type = %q, want %q", e.Type, EventTypeAssistant)
				}
				if !e.HasToolUse() {
					t.Error("expected HasToolUse() to be true")
				}
				tools := e.GetToolUses()
				if len(tools) != 1 {
					t.Errorf("len(GetToolUses()) = %d, want 1", len(tools))
				}
				if tools[0].Name != "Bash" {
					t.Errorf("tool Name = %q, want %q", tools[0].Name, "Bash")
				}
				if tools[0].ID != "toolu_123" {
					t.Errorf("tool ID = %q, want %q", tools[0].ID, "toolu_123")
				}
			},
		},
		{
			name: "user tool result",
			line: `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_123","content":"file1.txt\nfile2.txt"}]}}`,
			check: func(t *testing.T, e *StreamEvent) {
				if e.Type != EventTypeUser {
					t.Errorf("Type = %q, want %q", e.Type, EventTypeUser)
				}
				if !e.HasToolResult() {
					t.Error("expected HasToolResult() to be true")
				}
				results := e.GetToolResults()
				if len(results) != 1 {
					t.Errorf("len(GetToolResults()) = %d, want 1", len(results))
				}
				if results[0].ToolUseID != "toolu_123" {
					t.Errorf("ToolUseID = %q, want %q", results[0].ToolUseID, "toolu_123")
				}
			},
		},
		{
			name: "user tool result with error",
			line: `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_456","content":"command failed","is_error":true}]}}`,
			check: func(t *testing.T, e *StreamEvent) {
				if !e.HasErrorResult() {
					t.Error("expected HasErrorResult() to be true")
				}
				results := e.GetToolResults()
				if !results[0].IsError {
					t.Error("expected IsError to be true")
				}
			},
		},
		{
			name: "result success event",
			line: `{"type":"result","subtype":"success","session_id":"abc123","cost_usd":2.50,"num_turns":15}`,
			check: func(t *testing.T, e *StreamEvent) {
				if e.Type != EventTypeResult {
					t.Errorf("Type = %q, want %q", e.Type, EventTypeResult)
				}
				if !e.IsResultEvent() {
					t.Error("expected IsResultEvent() to be true")
				}
				if !e.IsSuccess() {
					t.Error("expected IsSuccess() to be true")
				}
				if e.IsError() {
					t.Error("expected IsError() to be false")
				}
				if e.CostUSD != 2.50 {
					t.Errorf("CostUSD = %f, want 2.50", e.CostUSD)
				}
				if e.NumTurns != 15 {
					t.Errorf("NumTurns = %d, want 15", e.NumTurns)
				}
			},
		},
		{
			name: "result error event",
			line: `{"type":"result","subtype":"error","session_id":"abc123","cost_usd":0.50,"num_turns":2}`,
			check: func(t *testing.T, e *StreamEvent) {
				if !e.IsResultEvent() {
					t.Error("expected IsResultEvent() to be true")
				}
				if e.IsSuccess() {
					t.Error("expected IsSuccess() to be false")
				}
				if !e.IsError() {
					t.Error("expected IsError() to be true")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseStreamEvent(tt.line)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if tt.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %+v", got)
				}
				return
			}

			if got == nil {
				t.Fatal("expected non-nil event")
			}

			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}

func TestStreamEventGetText(t *testing.T) {
	tests := []struct {
		name     string
		event    *StreamEvent
		expected string
	}{
		{
			name:     "nil message",
			event:    &StreamEvent{Type: EventTypeAssistant},
			expected: "",
		},
		{
			name: "single text block",
			event: &StreamEvent{
				Type: EventTypeAssistant,
				Message: &Message{
					Content: []ContentBlock{
						{Type: ContentTypeText, Text: "Hello"},
					},
				},
			},
			expected: "Hello",
		},
		{
			name: "multiple text blocks",
			event: &StreamEvent{
				Type: EventTypeAssistant,
				Message: &Message{
					Content: []ContentBlock{
						{Type: ContentTypeText, Text: "Hello"},
						{Type: ContentTypeToolUse, Name: "Bash"},
						{Type: ContentTypeText, Text: " World"},
					},
				},
			},
			expected: "Hello World",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.event.GetText(); got != tt.expected {
				t.Errorf("GetText() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestStreamStateUpdate(t *testing.T) {
	state := NewStreamState()

	// Process init event
	initEvent, _ := ParseStreamEvent(`{"type":"system","subtype":"init","session_id":"test-session","tools":["Bash","Read"]}`)
	state.Update(initEvent)

	if state.SessionID != "test-session" {
		t.Errorf("SessionID = %q, want %q", state.SessionID, "test-session")
	}
	if len(state.Tools) != 2 {
		t.Errorf("len(Tools) = %d, want 2", len(state.Tools))
	}

	// Process assistant with text
	assistantText, _ := ParseStreamEvent(`{"type":"assistant","message":{"content":[{"type":"text","text":"I'll help you"}]}}`)
	state.Update(assistantText)

	if state.TextBlocks != 1 {
		t.Errorf("TextBlocks = %d, want 1", state.TextBlocks)
	}

	// Process assistant with tool use
	assistantTool, _ := ParseStreamEvent(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}`)
	state.Update(assistantTool)

	if state.ToolCalls != 1 {
		t.Errorf("ToolCalls = %d, want 1", state.ToolCalls)
	}

	// Process user (tool result)
	userResult, _ := ParseStreamEvent(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"file1.txt"}]}}`)
	state.Update(userResult)

	if state.Turns != 1 {
		t.Errorf("Turns = %d, want 1", state.Turns)
	}
	if state.Errors != 0 {
		t.Errorf("Errors = %d, want 0", state.Errors)
	}

	// Process user with error result
	userError, _ := ParseStreamEvent(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":"failed","is_error":true}]}}`)
	state.Update(userError)

	if state.Errors != 1 {
		t.Errorf("Errors = %d, want 1", state.Errors)
	}

	// Process result event
	result, _ := ParseStreamEvent(`{"type":"result","subtype":"success","cost_usd":1.50,"num_turns":5}`)
	state.Update(result)

	if !state.Completed {
		t.Error("expected Completed to be true")
	}
	if !state.Success {
		t.Error("expected Success to be true")
	}
	if state.CostUSD != 1.50 {
		t.Errorf("CostUSD = %f, want 1.50", state.CostUSD)
	}
	if state.Turns != 5 {
		t.Errorf("Turns = %d, want 5", state.Turns)
	}
}

func TestStreamParser(t *testing.T) {
	var events []*StreamEvent
	parser := NewStreamParser(func(e *StreamEvent) {
		events = append(events, e)
	})

	lines := []string{
		`{"type":"system","subtype":"init","session_id":"s1","tools":["Bash"]}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Working..."}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"file.txt"}]}}`,
		`{"type":"result","subtype":"success","cost_usd":0.75,"num_turns":2}`,
	}

	for _, line := range lines {
		parser.ParseLine(line)
	}

	if len(events) != 5 {
		t.Errorf("len(events) = %d, want 5", len(events))
	}

	state := parser.State()
	if state.SessionID != "s1" {
		t.Errorf("SessionID = %q, want %q", state.SessionID, "s1")
	}
	if state.ToolCalls != 1 {
		t.Errorf("ToolCalls = %d, want 1", state.ToolCalls)
	}
	if state.TextBlocks != 1 {
		t.Errorf("TextBlocks = %d, want 1", state.TextBlocks)
	}
	if !parser.IsComplete() {
		t.Error("expected IsComplete() to be true")
	}
	if state.CostUSD != 0.75 {
		t.Errorf("CostUSD = %f, want 0.75", state.CostUSD)
	}
}

func TestStreamParserInvalidLines(t *testing.T) {
	parser := NewStreamParser(nil)

	// Empty line
	if e := parser.ParseLine(""); e != nil {
		t.Error("expected nil for empty line")
	}

	// Invalid JSON
	if e := parser.ParseLine("{invalid}"); e != nil {
		t.Error("expected nil for invalid JSON")
	}

	// State should be empty
	state := parser.State()
	if state.Completed {
		t.Error("expected Completed to be false")
	}
}

func TestContentBlockParseToolInput(t *testing.T) {
	tests := []struct {
		name    string
		block   ContentBlock
		wantErr bool
		check   func(t *testing.T, input *ToolInput)
	}{
		{
			name: "bash command",
			block: ContentBlock{
				Type:  ContentTypeToolUse,
				Name:  "Bash",
				Input: []byte(`{"command":"ls -la"}`),
			},
			check: func(t *testing.T, input *ToolInput) {
				if input.Command != "ls -la" {
					t.Errorf("Command = %q, want %q", input.Command, "ls -la")
				}
			},
		},
		{
			name: "edit command",
			block: ContentBlock{
				Type:  ContentTypeToolUse,
				Name:  "Edit",
				Input: []byte(`{"file_path":"/test.go","old_string":"foo","new_string":"bar"}`),
			},
			check: func(t *testing.T, input *ToolInput) {
				if input.FilePath != "/test.go" {
					t.Errorf("FilePath = %q, want %q", input.FilePath, "/test.go")
				}
				if input.OldString != "foo" {
					t.Errorf("OldString = %q, want %q", input.OldString, "foo")
				}
				if input.NewString != "bar" {
					t.Errorf("NewString = %q, want %q", input.NewString, "bar")
				}
			},
		},
		{
			name: "write command",
			block: ContentBlock{
				Type:  ContentTypeToolUse,
				Name:  "Write",
				Input: []byte(`{"file_path":"/test.txt","content":"hello world"}`),
			},
			check: func(t *testing.T, input *ToolInput) {
				if input.FilePath != "/test.txt" {
					t.Errorf("FilePath = %q, want %q", input.FilePath, "/test.txt")
				}
				if input.Content != "hello world" {
					t.Errorf("Content = %q, want %q", input.Content, "hello world")
				}
			},
		},
		{
			name: "grep command",
			block: ContentBlock{
				Type:  ContentTypeToolUse,
				Name:  "Grep",
				Input: []byte(`{"pattern":"TODO","path":"."}`),
			},
			check: func(t *testing.T, input *ToolInput) {
				if input.Pattern != "TODO" {
					t.Errorf("Pattern = %q, want %q", input.Pattern, "TODO")
				}
				if input.Path != "." {
					t.Errorf("Path = %q, want %q", input.Path, ".")
				}
			},
		},
		{
			name: "not tool_use type",
			block: ContentBlock{
				Type: ContentTypeText,
				Text: "hello",
			},
			wantErr: true,
		},
		{
			name: "empty input",
			block: ContentBlock{
				Type:  ContentTypeToolUse,
				Name:  "Bash",
				Input: nil,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input, err := tt.block.ParseToolInput()

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if tt.check != nil {
				tt.check(t, input)
			}
		})
	}
}

func TestStreamEventNonMatchingTypes(t *testing.T) {
	// Test methods on wrong event types
	userEvent := &StreamEvent{Type: EventTypeUser}
	if userEvent.HasToolUse() {
		t.Error("HasToolUse should return false for user event")
	}
	if tools := userEvent.GetToolUses(); tools != nil {
		t.Error("GetToolUses should return nil for user event")
	}

	assistantEvent := &StreamEvent{Type: EventTypeAssistant}
	if assistantEvent.HasToolResult() {
		t.Error("HasToolResult should return false for assistant event")
	}
	if results := assistantEvent.GetToolResults(); results != nil {
		t.Error("GetToolResults should return nil for assistant event")
	}

	systemEvent := &StreamEvent{Type: EventTypeSystem, Subtype: "other"}
	if systemEvent.IsInitEvent() {
		t.Error("IsInitEvent should return false for non-init system event")
	}
}
