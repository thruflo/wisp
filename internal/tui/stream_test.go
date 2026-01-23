package tui

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thruflo/wisp/internal/stream"
)

func TestTUI_SetGetStreamClient(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	tui := NewTUI(buf)

	// Initially nil
	assert.Nil(t, tui.GetStreamClient())

	// Set client
	client := stream.NewStreamClient("http://localhost:8374")
	tui.SetStreamClient(client)

	got := tui.GetStreamClient()
	assert.NotNil(t, got)
	assert.Equal(t, "http://localhost:8374", got.BaseURL())
}

func TestTUI_HandleStreamEvent_Session(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	tui := NewTUI(buf)

	sessionData := &stream.SessionEvent{
		ID:        "test-session",
		Repo:      "test/repo",
		Branch:    "main",
		Spec:      "test spec",
		Status:    stream.SessionStatusRunning,
		Iteration: 5,
	}

	event, err := stream.NewSessionEvent(sessionData)
	require.NoError(t, err)

	tui.HandleStreamEvent(event)

	state := tui.GetState()
	assert.Equal(t, "main", state.Branch)
	assert.Equal(t, 5, state.Iteration)
	assert.Equal(t, "running", state.Status)
}

func TestTUI_HandleStreamEvent_Task(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	tui := NewTUI(buf)

	// Send task events for orders 0, 1, 2
	for i := 0; i < 3; i++ {
		taskData := &stream.TaskEvent{
			ID:          "task-" + string(rune('a'+i)),
			SessionID:   "test-session",
			Order:       i,
			Category:    "feature",
			Description: "Task description",
			Status:      stream.TaskStatusPending,
		}
		event, err := stream.NewTaskEvent(taskData)
		require.NoError(t, err)
		tui.HandleStreamEvent(event)
	}

	state := tui.GetState()
	assert.Equal(t, 3, state.TotalTasks)
}

func TestTUI_HandleStreamEvent_Claude(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	tui := NewTUI(buf)

	claudeData := &stream.ClaudeEvent{
		ID:        "event-1",
		SessionID: "test-session",
		Iteration: 1,
		Sequence:  1,
		Message:   "Test output message",
		Timestamp: time.Now(),
	}

	event, err := stream.NewClaudeEventEvent(claudeData)
	require.NoError(t, err)

	tui.HandleStreamEvent(event)

	// Check that the message was appended to tail view
	lines := tui.tailView.Lines()
	assert.Len(t, lines, 1)
	assert.Equal(t, "Test output message", lines[0])
}

func TestTUI_HandleStreamEvent_InputRequest(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	tui := NewTUI(buf)

	inputData := &stream.InputRequestEvent{
		ID:        "input-1",
		SessionID: "test-session",
		Iteration: 1,
		Question:  "What is your name?",
	}

	event, err := stream.NewInputRequestEvent(inputData)
	require.NoError(t, err)

	tui.HandleStreamEvent(event)

	// Check that input view is shown
	assert.Equal(t, ViewInput, tui.GetView())
	assert.Equal(t, "input-1", tui.InputRequestID())
	state := tui.GetState()
	assert.Equal(t, "What is your name?", state.Question)
}

func TestTUI_HandleStreamEvent_InputResponse(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	tui := NewTUI(buf)

	// First, show an input request
	tui.ShowInput("Question?")
	tui.SetInputRequestID("input-1")
	assert.Equal(t, ViewInput, tui.GetView())

	// Then receive an input_response event (separate event type in State Protocol)
	responseData := &stream.InputResponse{
		ID:        "response-1",
		RequestID: "input-1",
		Response:  "Answer",
	}

	event, err := stream.NewInputResponseEvent(responseData)
	require.NoError(t, err)

	tui.HandleStreamEvent(event)

	// Check that we're back to summary view
	assert.Equal(t, ViewSummary, tui.GetView())
	assert.Equal(t, "", tui.InputRequestID())
}

func TestTUI_UpdateFromSnapshot(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	tui := NewTUI(buf)

	snapshot := &stream.StateSnapshot{
		Session: &stream.SessionEvent{
			ID:        "test-session",
			Repo:      "test/repo",
			Branch:    "feature-branch",
			Spec:      "spec",
			Status:    stream.SessionStatusRunning,
			Iteration: 3,
		},
		Tasks: []*stream.TaskEvent{
			{ID: "task-1", Status: stream.TaskStatusCompleted},
			{ID: "task-2", Status: stream.TaskStatusCompleted},
			{ID: "task-3", Status: stream.TaskStatusPending},
			{ID: "task-4", Status: stream.TaskStatusPending},
		},
		LastSeq: 42,
	}

	tui.UpdateFromSnapshot(snapshot)

	state := tui.GetState()
	assert.Equal(t, "feature-branch", state.Branch)
	assert.Equal(t, 3, state.Iteration)
	assert.Equal(t, "running", state.Status)
	assert.Equal(t, 4, state.TotalTasks)
	assert.Equal(t, 2, state.CompletedTasks)
}

func TestTUI_UpdateFromSnapshot_WithPendingInput(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	tui := NewTUI(buf)

	// In State Protocol, presence in snapshot means it's pending (not yet responded)
	snapshot := &stream.StateSnapshot{
		Session: &stream.SessionEvent{
			ID:     "test-session",
			Branch: "main",
			Status: stream.SessionStatusNeedsInput,
		},
		InputRequest: &stream.InputRequestEvent{
			ID:       "input-123",
			Question: "What database?",
		},
		LastSeq: 10,
	}

	tui.UpdateFromSnapshot(snapshot)

	state := tui.GetState()
	assert.Equal(t, "input-123", tui.InputRequestID())
	assert.Equal(t, "What database?", state.Question)
}

func TestTUI_InputRequestID(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	tui := NewTUI(buf)

	// Initially empty
	assert.Equal(t, "", tui.InputRequestID())

	// Set and get
	tui.SetInputRequestID("test-request-id")
	assert.Equal(t, "test-request-id", tui.InputRequestID())

	// Clear
	tui.SetInputRequestID("")
	assert.Equal(t, "", tui.InputRequestID())
}

func TestFormatClaudeEventForDisplay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		message any
		want    string
	}{
		{
			name:    "string message",
			message: "Hello world",
			want:    "Hello world",
		},
		{
			name:    "long string truncated",
			message: string(make([]byte, 300)),
			want:    string(make([]byte, 200)) + "...",
		},
		{
			name: "map with text content",
			message: map[string]any{
				"content": []any{
					map[string]any{
						"type": "text",
						"text": "Extracted text",
					},
				},
			},
			want: "Extracted text",
		},
		{
			name: "map without text content",
			message: map[string]any{
				"content": []any{
					map[string]any{
						"type": "tool_use",
						"name": "Bash",
					},
				},
			},
			want: "",
		},
		{
			name:    "nil message",
			message: nil,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := &stream.ClaudeEvent{
				Message: tt.message,
			}
			got := formatClaudeEventForDisplay(data)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewStreamRunner(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	tui := NewTUI(buf)
	client := stream.NewStreamClient("http://localhost:8374")

	runner := NewStreamRunner(tui, client)

	require.NotNil(t, runner)
	assert.Equal(t, tui, runner.tui)
	assert.Equal(t, client, runner.client)
}

func TestStreamRunner_Run_ConnectsAndSyncs(t *testing.T) {
	// Skip this test as it requires terminal capabilities
	// The core stream handling logic is tested in other tests
	t.Skip("Requires terminal capabilities not available in test environment")
}
