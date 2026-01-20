package stream

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		msgType MessageType
		data    any
		wantErr bool
	}{
		{
			name:    "session event",
			msgType: MessageTypeSession,
			data: SessionEvent{
				ID:        "sess-123",
				Repo:      "owner/repo",
				Branch:    "feature-branch",
				Status:    SessionStatusRunning,
				Iteration: 1,
				StartedAt: time.Now().UTC(),
			},
			wantErr: false,
		},
		{
			name:    "task event",
			msgType: MessageTypeTask,
			data: TaskEvent{
				ID:          "task-1",
				SessionID:   "sess-123",
				Order:       0,
				Category:    "feature",
				Description: "Implement feature X",
				Status:      TaskStatusPending,
			},
			wantErr: false,
		},
		{
			name:    "claude event",
			msgType: MessageTypeClaudeEvent,
			data: ClaudeEvent{
				ID:        "claude-1",
				SessionID: "sess-123",
				Iteration: 1,
				Sequence:  0,
				Message:   map[string]any{"type": "assistant", "content": "Hello"},
				Timestamp: time.Now().UTC(),
			},
			wantErr: false,
		},
		{
			name:    "input request event",
			msgType: MessageTypeInputRequest,
			data: InputRequestEvent{
				ID:        "input-1",
				SessionID: "sess-123",
				Iteration: 1,
				Question:  "What should I do?",
				Responded: false,
			},
			wantErr: false,
		},
		{
			name:    "command event",
			msgType: MessageTypeCommand,
			data: Command{
				ID:   "cmd-1",
				Type: CommandTypeKill,
			},
			wantErr: false,
		},
		{
			name:    "ack event",
			msgType: MessageTypeAck,
			data: Ack{
				CommandID: "cmd-1",
				Status:    AckStatusSuccess,
			},
			wantErr: false,
		},
		{
			name:    "unmarshallable data",
			msgType: MessageTypeSession,
			data:    make(chan int), // channels cannot be marshaled
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			event, err := NewEvent(tt.msgType, tt.data)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, event)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, event)
			assert.Equal(t, tt.msgType, event.Type)
			assert.False(t, event.Timestamp.IsZero())
			assert.NotEmpty(t, event.Data)
		})
	}
}

func TestMustNewEvent(t *testing.T) {
	t.Parallel()

	t.Run("valid data", func(t *testing.T) {
		t.Parallel()
		event := MustNewEvent(MessageTypeSession, SessionEvent{ID: "test"})
		assert.NotNil(t, event)
		assert.Equal(t, MessageTypeSession, event.Type)
	})

	t.Run("invalid data panics", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() {
			MustNewEvent(MessageTypeSession, make(chan int))
		})
	})
}

func TestEventMarshalUnmarshal(t *testing.T) {
	t.Parallel()

	original := MustNewEvent(MessageTypeSession, SessionEvent{
		ID:        "sess-123",
		Repo:      "owner/repo",
		Branch:    "main",
		Status:    SessionStatusRunning,
		Iteration: 5,
		StartedAt: time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC),
	})
	original.Seq = 42

	// Marshal
	data, err := original.Marshal()
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Unmarshal
	restored, err := UnmarshalEvent(data)
	require.NoError(t, err)
	require.NotNil(t, restored)

	assert.Equal(t, original.Seq, restored.Seq)
	assert.Equal(t, original.Type, restored.Type)
	assert.Equal(t, original.Timestamp.UTC(), restored.Timestamp.UTC())
}

func TestUnmarshalEventInvalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"invalid json", []byte("{invalid")},
		{"not an object", []byte("[1,2,3]")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := UnmarshalEvent(tt.data)
			assert.Error(t, err)
		})
	}
}

func TestEventDataAccessors(t *testing.T) {
	t.Parallel()

	t.Run("SessionData", func(t *testing.T) {
		t.Parallel()

		originalData := SessionEvent{
			ID:        "sess-123",
			Repo:      "owner/repo",
			Branch:    "main",
			Status:    SessionStatusDone,
			Iteration: 10,
		}
		event := MustNewEvent(MessageTypeSession, originalData)

		data, err := event.SessionData()
		require.NoError(t, err)
		assert.Equal(t, originalData.ID, data.ID)
		assert.Equal(t, originalData.Repo, data.Repo)
		assert.Equal(t, originalData.Status, data.Status)

		// Wrong type should error
		wrongEvent := MustNewEvent(MessageTypeTask, TaskEvent{})
		_, err = wrongEvent.SessionData()
		assert.Error(t, err)
	})

	t.Run("TaskData", func(t *testing.T) {
		t.Parallel()

		originalData := TaskEvent{
			ID:          "task-1",
			SessionID:   "sess-123",
			Order:       2,
			Category:    "bugfix",
			Description: "Fix the bug",
			Status:      TaskStatusCompleted,
		}
		event := MustNewEvent(MessageTypeTask, originalData)

		data, err := event.TaskData()
		require.NoError(t, err)
		assert.Equal(t, originalData.ID, data.ID)
		assert.Equal(t, originalData.Description, data.Description)
		assert.Equal(t, originalData.Status, data.Status)

		// Wrong type should error
		wrongEvent := MustNewEvent(MessageTypeSession, SessionEvent{})
		_, err = wrongEvent.TaskData()
		assert.Error(t, err)
	})

	t.Run("ClaudeEventData", func(t *testing.T) {
		t.Parallel()

		originalData := ClaudeEvent{
			ID:        "claude-1",
			SessionID: "sess-123",
			Iteration: 3,
			Sequence:  5,
			Message:   map[string]any{"type": "result", "output": "done"},
		}
		event := MustNewEvent(MessageTypeClaudeEvent, originalData)

		data, err := event.ClaudeEventData()
		require.NoError(t, err)
		assert.Equal(t, originalData.ID, data.ID)
		assert.Equal(t, originalData.Sequence, data.Sequence)

		// Wrong type should error
		wrongEvent := MustNewEvent(MessageTypeSession, SessionEvent{})
		_, err = wrongEvent.ClaudeEventData()
		assert.Error(t, err)
	})

	t.Run("InputRequestData", func(t *testing.T) {
		t.Parallel()

		response := "Yes, proceed"
		originalData := InputRequestEvent{
			ID:        "input-1",
			SessionID: "sess-123",
			Iteration: 2,
			Question:  "Should I continue?",
			Responded: true,
			Response:  &response,
		}
		event := MustNewEvent(MessageTypeInputRequest, originalData)

		data, err := event.InputRequestData()
		require.NoError(t, err)
		assert.Equal(t, originalData.ID, data.ID)
		assert.Equal(t, originalData.Question, data.Question)
		assert.True(t, data.Responded)
		require.NotNil(t, data.Response)
		assert.Equal(t, response, *data.Response)

		// Wrong type should error
		wrongEvent := MustNewEvent(MessageTypeSession, SessionEvent{})
		_, err = wrongEvent.InputRequestData()
		assert.Error(t, err)
	})

	t.Run("CommandData", func(t *testing.T) {
		t.Parallel()

		originalCmd := Command{
			ID:   "cmd-1",
			Type: CommandTypeKill,
		}
		event := MustNewEvent(MessageTypeCommand, originalCmd)

		data, err := event.CommandData()
		require.NoError(t, err)
		assert.Equal(t, originalCmd.ID, data.ID)
		assert.Equal(t, originalCmd.Type, data.Type)

		// Wrong type should error
		wrongEvent := MustNewEvent(MessageTypeSession, SessionEvent{})
		_, err = wrongEvent.CommandData()
		assert.Error(t, err)
	})

	t.Run("AckData", func(t *testing.T) {
		t.Parallel()

		originalAck := Ack{
			CommandID: "cmd-1",
			Status:    AckStatusError,
			Error:     "something went wrong",
		}
		event := MustNewEvent(MessageTypeAck, originalAck)

		data, err := event.AckData()
		require.NoError(t, err)
		assert.Equal(t, originalAck.CommandID, data.CommandID)
		assert.Equal(t, originalAck.Status, data.Status)
		assert.Equal(t, originalAck.Error, data.Error)

		// Wrong type should error
		wrongEvent := MustNewEvent(MessageTypeSession, SessionEvent{})
		_, err = wrongEvent.AckData()
		assert.Error(t, err)
	})
}

func TestCommandCreators(t *testing.T) {
	t.Parallel()

	t.Run("NewInputResponseCommand", func(t *testing.T) {
		t.Parallel()

		cmd, err := NewInputResponseCommand("cmd-1", "req-1", "my response")
		require.NoError(t, err)
		assert.Equal(t, "cmd-1", cmd.ID)
		assert.Equal(t, CommandTypeInputResponse, cmd.Type)

		payload, err := cmd.InputResponsePayloadData()
		require.NoError(t, err)
		assert.Equal(t, "req-1", payload.RequestID)
		assert.Equal(t, "my response", payload.Response)
	})

	t.Run("NewKillCommand", func(t *testing.T) {
		t.Parallel()

		cmd, err := NewKillCommand("cmd-2", true)
		require.NoError(t, err)
		assert.Equal(t, "cmd-2", cmd.ID)
		assert.Equal(t, CommandTypeKill, cmd.Type)

		payload, err := cmd.KillPayloadData()
		require.NoError(t, err)
		assert.True(t, payload.DeleteSprite)
	})

	t.Run("NewKillCommand without delete", func(t *testing.T) {
		t.Parallel()

		cmd, err := NewKillCommand("cmd-3", false)
		require.NoError(t, err)

		payload, err := cmd.KillPayloadData()
		require.NoError(t, err)
		assert.False(t, payload.DeleteSprite)
	})

	t.Run("NewBackgroundCommand", func(t *testing.T) {
		t.Parallel()

		cmd := NewBackgroundCommand("cmd-4")
		assert.Equal(t, "cmd-4", cmd.ID)
		assert.Equal(t, CommandTypeBackground, cmd.Type)
		assert.Empty(t, cmd.Payload)
	})
}

func TestKillPayloadWithEmptyPayload(t *testing.T) {
	t.Parallel()

	cmd := &Command{
		ID:      "cmd-1",
		Type:    CommandTypeKill,
		Payload: nil, // Empty payload
	}

	payload, err := cmd.KillPayloadData()
	require.NoError(t, err)
	assert.False(t, payload.DeleteSprite) // Default value
}

func TestCommandPayloadErrors(t *testing.T) {
	t.Parallel()

	t.Run("InputResponsePayload wrong type", func(t *testing.T) {
		t.Parallel()
		cmd := &Command{ID: "1", Type: CommandTypeKill}
		_, err := cmd.InputResponsePayloadData()
		assert.Error(t, err)
	})

	t.Run("KillPayload wrong type", func(t *testing.T) {
		t.Parallel()
		cmd := &Command{ID: "1", Type: CommandTypeInputResponse}
		_, err := cmd.KillPayloadData()
		assert.Error(t, err)
	})

	t.Run("InputResponsePayload invalid json", func(t *testing.T) {
		t.Parallel()
		cmd := &Command{ID: "1", Type: CommandTypeInputResponse, Payload: json.RawMessage("{invalid")}
		_, err := cmd.InputResponsePayloadData()
		assert.Error(t, err)
	})

	t.Run("KillPayload invalid json", func(t *testing.T) {
		t.Parallel()
		cmd := &Command{ID: "1", Type: CommandTypeKill, Payload: json.RawMessage("{invalid")}
		_, err := cmd.KillPayloadData()
		assert.Error(t, err)
	})
}

func TestAckCreators(t *testing.T) {
	t.Parallel()

	t.Run("NewSuccessAck", func(t *testing.T) {
		t.Parallel()

		ack := NewSuccessAck("cmd-1")
		assert.Equal(t, "cmd-1", ack.CommandID)
		assert.Equal(t, AckStatusSuccess, ack.Status)
		assert.Empty(t, ack.Error)
	})

	t.Run("NewErrorAck", func(t *testing.T) {
		t.Parallel()

		ack := NewErrorAck("cmd-2", assert.AnError)
		assert.Equal(t, "cmd-2", ack.CommandID)
		assert.Equal(t, AckStatusError, ack.Status)
		assert.Equal(t, assert.AnError.Error(), ack.Error)
	})
}

func TestSessionStatusConstants(t *testing.T) {
	t.Parallel()

	// Verify status constants match expected string values
	assert.Equal(t, SessionStatus("running"), SessionStatusRunning)
	assert.Equal(t, SessionStatus("needs_input"), SessionStatusNeedsInput)
	assert.Equal(t, SessionStatus("blocked"), SessionStatusBlocked)
	assert.Equal(t, SessionStatus("done"), SessionStatusDone)
	assert.Equal(t, SessionStatus("paused"), SessionStatusPaused)
}

func TestTaskStatusConstants(t *testing.T) {
	t.Parallel()

	// Verify status constants match expected string values
	assert.Equal(t, TaskStatus("pending"), TaskStatusPending)
	assert.Equal(t, TaskStatus("in_progress"), TaskStatusInProgress)
	assert.Equal(t, TaskStatus("completed"), TaskStatusCompleted)
}

func TestMessageTypeConstants(t *testing.T) {
	t.Parallel()

	// Verify message type constants
	assert.Equal(t, MessageType("session"), MessageTypeSession)
	assert.Equal(t, MessageType("task"), MessageTypeTask)
	assert.Equal(t, MessageType("claude_event"), MessageTypeClaudeEvent)
	assert.Equal(t, MessageType("input_request"), MessageTypeInputRequest)
	assert.Equal(t, MessageType("ack"), MessageTypeAck)
	assert.Equal(t, MessageType("command"), MessageTypeCommand)
}

func TestCommandTypeConstants(t *testing.T) {
	t.Parallel()

	// Verify command type constants
	assert.Equal(t, CommandType("kill"), CommandTypeKill)
	assert.Equal(t, CommandType("background"), CommandTypeBackground)
	assert.Equal(t, CommandType("input_response"), CommandTypeInputResponse)
}

func TestEventJSONRoundTrip(t *testing.T) {
	t.Parallel()

	// Create a complex event and verify it survives JSON round-trip
	session := SessionEvent{
		ID:        "sess-abc123",
		Repo:      "owner/repo-name",
		Branch:    "feature/my-branch",
		Spec:      "# RFC\n\nThis is the spec.",
		Status:    SessionStatusRunning,
		Iteration: 42,
		StartedAt: time.Date(2025, 6, 15, 14, 30, 0, 0, time.UTC),
	}

	event := MustNewEvent(MessageTypeSession, session)
	event.Seq = 100

	// Marshal to JSON
	jsonData, err := json.Marshal(event)
	require.NoError(t, err)

	// Unmarshal back
	var restored Event
	err = json.Unmarshal(jsonData, &restored)
	require.NoError(t, err)

	assert.Equal(t, event.Seq, restored.Seq)
	assert.Equal(t, event.Type, restored.Type)

	// Extract and verify session data
	restoredSession, err := restored.SessionData()
	require.NoError(t, err)
	assert.Equal(t, session.ID, restoredSession.ID)
	assert.Equal(t, session.Repo, restoredSession.Repo)
	assert.Equal(t, session.Branch, restoredSession.Branch)
	assert.Equal(t, session.Spec, restoredSession.Spec)
	assert.Equal(t, session.Status, restoredSession.Status)
	assert.Equal(t, session.Iteration, restoredSession.Iteration)
	assert.Equal(t, session.StartedAt.UTC(), restoredSession.StartedAt.UTC())
}
