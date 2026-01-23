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
		key     string
		value   any
		wantErr bool
	}{
		{
			name:    "session event",
			msgType: MessageTypeSession,
			key:     "session:sess-123",
			value: Session{
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
			key:     "task:task-1",
			value: Task{
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
			key:     "claude_event:claude-1",
			value: ClaudeEvent{
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
			key:     "input_request:input-1",
			value: InputRequest{
				ID:        "input-1",
				SessionID: "sess-123",
				Iteration: 1,
				Question:  "What should I do?",
			},
			wantErr: false,
		},
		{
			name:    "input response event",
			msgType: MessageTypeInputResponse,
			key:     "input_response:resp-1",
			value: InputResponse{
				ID:        "resp-1",
				RequestID: "input-1",
				Response:  "Please continue",
			},
			wantErr: false,
		},
		{
			name:    "command event",
			msgType: MessageTypeCommand,
			key:     "command:cmd-1",
			value: Command{
				ID:   "cmd-1",
				Type: CommandTypeKill,
			},
			wantErr: false,
		},
		{
			name:    "ack event",
			msgType: MessageTypeAck,
			key:     "ack:cmd-1",
			value: Ack{
				CommandID: "cmd-1",
				Status:    AckStatusSuccess,
			},
			wantErr: false,
		},
		{
			name:    "unmarshallable value",
			msgType: MessageTypeSession,
			key:     "session:bad",
			value:   make(chan int), // channels cannot be marshaled
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			event, err := NewEvent(tt.msgType, tt.key, tt.value)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, event)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, event)
			assert.Equal(t, tt.msgType, event.Type)
			assert.Equal(t, tt.key, event.Key)
			assert.False(t, event.Headers.Timestamp.IsZero())
			assert.Equal(t, OperationInsert, event.Headers.Operation)
			assert.NotEmpty(t, event.Value)
		})
	}
}

func TestNewEventWithOp(t *testing.T) {
	t.Parallel()

	session := Session{ID: "test", Status: SessionStatusRunning}
	event, err := NewEventWithOp(MessageTypeSession, "session:test", session, OperationUpdate)
	require.NoError(t, err)
	assert.Equal(t, OperationUpdate, event.Headers.Operation)
}

func TestMustNewEvent(t *testing.T) {
	t.Parallel()

	t.Run("valid value", func(t *testing.T) {
		t.Parallel()
		event := MustNewEvent(MessageTypeSession, "session:test", Session{ID: "test"})
		assert.NotNil(t, event)
		assert.Equal(t, MessageTypeSession, event.Type)
		assert.Equal(t, "session:test", event.Key)
	})

	t.Run("invalid value panics", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() {
			MustNewEvent(MessageTypeSession, "session:bad", make(chan int))
		})
	})
}

func TestEventMarshalUnmarshal(t *testing.T) {
	t.Parallel()

	original := MustNewEvent(MessageTypeSession, "session:sess-123", Session{
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
	assert.Equal(t, original.Key, restored.Key)
	assert.Equal(t, original.Headers.Operation, restored.Headers.Operation)
	assert.Equal(t, original.Headers.Timestamp.UTC(), restored.Headers.Timestamp.UTC())
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

		originalData := Session{
			ID:        "sess-123",
			Repo:      "owner/repo",
			Branch:    "main",
			Status:    SessionStatusDone,
			Iteration: 10,
		}
		event := MustNewEvent(MessageTypeSession, "session:sess-123", originalData)

		data, err := event.SessionData()
		require.NoError(t, err)
		assert.Equal(t, originalData.ID, data.ID)
		assert.Equal(t, originalData.Repo, data.Repo)
		assert.Equal(t, originalData.Status, data.Status)

		// Wrong type should error
		wrongEvent := MustNewEvent(MessageTypeTask, "task:1", Task{})
		_, err = wrongEvent.SessionData()
		assert.Error(t, err)
	})

	t.Run("TaskData", func(t *testing.T) {
		t.Parallel()

		originalData := Task{
			ID:          "task-1",
			SessionID:   "sess-123",
			Order:       2,
			Category:    "bugfix",
			Description: "Fix the bug",
			Status:      TaskStatusCompleted,
		}
		event := MustNewEvent(MessageTypeTask, "task:task-1", originalData)

		data, err := event.TaskData()
		require.NoError(t, err)
		assert.Equal(t, originalData.ID, data.ID)
		assert.Equal(t, originalData.Description, data.Description)
		assert.Equal(t, originalData.Status, data.Status)

		// Wrong type should error
		wrongEvent := MustNewEvent(MessageTypeSession, "session:1", Session{})
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
		event := MustNewEvent(MessageTypeClaudeEvent, "claude_event:claude-1", originalData)

		data, err := event.ClaudeEventData()
		require.NoError(t, err)
		assert.Equal(t, originalData.ID, data.ID)
		assert.Equal(t, originalData.Sequence, data.Sequence)

		// Wrong type should error
		wrongEvent := MustNewEvent(MessageTypeSession, "session:1", Session{})
		_, err = wrongEvent.ClaudeEventData()
		assert.Error(t, err)
	})

	t.Run("InputRequestData", func(t *testing.T) {
		t.Parallel()

		originalData := InputRequest{
			ID:        "input-1",
			SessionID: "sess-123",
			Iteration: 2,
			Question:  "Should I continue?",
		}
		event := MustNewEvent(MessageTypeInputRequest, "input_request:input-1", originalData)

		data, err := event.InputRequestData()
		require.NoError(t, err)
		assert.Equal(t, originalData.ID, data.ID)
		assert.Equal(t, originalData.Question, data.Question)

		// Wrong type should error
		wrongEvent := MustNewEvent(MessageTypeSession, "session:1", Session{})
		_, err = wrongEvent.InputRequestData()
		assert.Error(t, err)
	})

	t.Run("InputResponseData", func(t *testing.T) {
		t.Parallel()

		originalData := InputResponse{
			ID:        "resp-1",
			RequestID: "input-1",
			Response:  "Yes, continue",
		}
		event := MustNewEvent(MessageTypeInputResponse, "input_response:resp-1", originalData)

		data, err := event.InputResponseData()
		require.NoError(t, err)
		assert.Equal(t, originalData.ID, data.ID)
		assert.Equal(t, originalData.RequestID, data.RequestID)
		assert.Equal(t, originalData.Response, data.Response)

		// Wrong type should error
		wrongEvent := MustNewEvent(MessageTypeSession, "session:1", Session{})
		_, err = wrongEvent.InputResponseData()
		assert.Error(t, err)
	})

	t.Run("CommandData", func(t *testing.T) {
		t.Parallel()

		originalCmd := Command{
			ID:   "cmd-1",
			Type: CommandTypeKill,
		}
		event := MustNewEvent(MessageTypeCommand, "command:cmd-1", originalCmd)

		data, err := event.CommandData()
		require.NoError(t, err)
		assert.Equal(t, originalCmd.ID, data.ID)
		assert.Equal(t, originalCmd.Type, data.Type)

		// Wrong type should error
		wrongEvent := MustNewEvent(MessageTypeSession, "session:1", Session{})
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
		event := MustNewEvent(MessageTypeAck, "ack:cmd-1", originalAck)

		data, err := event.AckData()
		require.NoError(t, err)
		assert.Equal(t, originalAck.CommandID, data.CommandID)
		assert.Equal(t, originalAck.Status, data.Status)
		assert.Equal(t, originalAck.Error, data.Error)

		// Wrong type should error
		wrongEvent := MustNewEvent(MessageTypeSession, "session:1", Session{})
		_, err = wrongEvent.AckData()
		assert.Error(t, err)
	})
}

func TestCommandCreators(t *testing.T) {
	t.Parallel()

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

	t.Run("KillPayload wrong type", func(t *testing.T) {
		t.Parallel()
		cmd := &Command{ID: "1", Type: CommandTypeBackground}
		_, err := cmd.KillPayloadData()
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
	assert.Equal(t, MessageType("input_response"), MessageTypeInputResponse)
	assert.Equal(t, MessageType("ack"), MessageTypeAck)
	assert.Equal(t, MessageType("command"), MessageTypeCommand)
}

func TestCommandTypeConstants(t *testing.T) {
	t.Parallel()

	// Verify command type constants
	assert.Equal(t, CommandType("kill"), CommandTypeKill)
	assert.Equal(t, CommandType("background"), CommandTypeBackground)
}

func TestOperationConstants(t *testing.T) {
	t.Parallel()

	// Verify operation constants
	assert.Equal(t, Operation("insert"), OperationInsert)
	assert.Equal(t, Operation("update"), OperationUpdate)
	assert.Equal(t, Operation("delete"), OperationDelete)
}

func TestEventJSONRoundTrip(t *testing.T) {
	t.Parallel()

	// Create a complex event and verify it survives JSON round-trip
	session := Session{
		ID:        "sess-abc123",
		Repo:      "owner/repo-name",
		Branch:    "feature/my-branch",
		Spec:      "# RFC\n\nThis is the spec.",
		Status:    SessionStatusRunning,
		Iteration: 42,
		StartedAt: time.Date(2025, 6, 15, 14, 30, 0, 0, time.UTC),
	}

	event := MustNewEvent(MessageTypeSession, "session:sess-abc123", session)
	event.Seq = 100
	event.Headers.TxID = "tx-456"

	// Marshal to JSON
	jsonData, err := json.Marshal(event)
	require.NoError(t, err)

	// Unmarshal back
	var restored Event
	err = json.Unmarshal(jsonData, &restored)
	require.NoError(t, err)

	assert.Equal(t, event.Seq, restored.Seq)
	assert.Equal(t, event.Type, restored.Type)
	assert.Equal(t, event.Key, restored.Key)
	assert.Equal(t, event.Headers.Operation, restored.Headers.Operation)
	assert.Equal(t, event.Headers.TxID, restored.Headers.TxID)

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

func TestStateProtocolFormat(t *testing.T) {
	t.Parallel()

	// Verify the JSON format matches State Protocol specification
	session := Session{
		ID:     "test-123",
		Repo:   "owner/repo",
		Branch: "main",
		Status: SessionStatusRunning,
	}

	event, err := NewSessionEvent(&session)
	require.NoError(t, err)

	jsonData, err := event.Marshal()
	require.NoError(t, err)

	// Parse into map to verify structure
	var raw map[string]any
	err = json.Unmarshal(jsonData, &raw)
	require.NoError(t, err)

	// Verify State Protocol required fields
	assert.Equal(t, "session", raw["type"])
	assert.Equal(t, "session:test-123", raw["key"])
	assert.NotNil(t, raw["value"])
	assert.NotNil(t, raw["headers"])

	// Verify headers structure
	headers := raw["headers"].(map[string]any)
	assert.Equal(t, "insert", headers["operation"])
	assert.NotEmpty(t, headers["timestamp"])
}

func TestHelperEventCreators(t *testing.T) {
	t.Parallel()

	t.Run("NewSessionEvent", func(t *testing.T) {
		t.Parallel()
		session := &Session{ID: "s1", Status: SessionStatusRunning}
		event, err := NewSessionEvent(session)
		require.NoError(t, err)
		assert.Equal(t, MessageTypeSession, event.Type)
		assert.Equal(t, "session:s1", event.Key)
	})

	t.Run("NewSessionEventWithOp", func(t *testing.T) {
		t.Parallel()
		session := &Session{ID: "s1", Status: SessionStatusRunning}
		event, err := NewSessionEventWithOp(session, OperationUpdate)
		require.NoError(t, err)
		assert.Equal(t, OperationUpdate, event.Headers.Operation)
	})

	t.Run("NewTaskEvent", func(t *testing.T) {
		t.Parallel()
		task := &Task{ID: "t1", Status: TaskStatusPending}
		event, err := NewTaskEvent(task)
		require.NoError(t, err)
		assert.Equal(t, MessageTypeTask, event.Type)
		assert.Equal(t, "task:t1", event.Key)
	})

	t.Run("NewClaudeEventEvent", func(t *testing.T) {
		t.Parallel()
		ce := &ClaudeEvent{ID: "ce1", Message: "test"}
		event, err := NewClaudeEventEvent(ce)
		require.NoError(t, err)
		assert.Equal(t, MessageTypeClaudeEvent, event.Type)
		assert.Equal(t, "claude_event:ce1", event.Key)
	})

	t.Run("NewInputRequestEvent", func(t *testing.T) {
		t.Parallel()
		ir := &InputRequest{ID: "ir1", Question: "test?"}
		event, err := NewInputRequestEvent(ir)
		require.NoError(t, err)
		assert.Equal(t, MessageTypeInputRequest, event.Type)
		assert.Equal(t, "input_request:ir1", event.Key)
	})

	t.Run("NewInputResponseEvent", func(t *testing.T) {
		t.Parallel()
		ir := &InputResponse{ID: "resp1", RequestID: "ir1", Response: "yes"}
		event, err := NewInputResponseEvent(ir)
		require.NoError(t, err)
		assert.Equal(t, MessageTypeInputResponse, event.Type)
		assert.Equal(t, "input_response:resp1", event.Key)
	})

	t.Run("NewCommandEvent", func(t *testing.T) {
		t.Parallel()
		cmd := &Command{ID: "cmd1", Type: CommandTypeKill}
		event, err := NewCommandEvent(cmd)
		require.NoError(t, err)
		assert.Equal(t, MessageTypeCommand, event.Type)
		assert.Equal(t, "command:cmd1", event.Key)
	})

	t.Run("NewAckEvent", func(t *testing.T) {
		t.Parallel()
		ack := &Ack{CommandID: "cmd1", Status: AckStatusSuccess}
		event, err := NewAckEvent(ack)
		require.NoError(t, err)
		assert.Equal(t, MessageTypeAck, event.Type)
		assert.Equal(t, "ack:cmd1", event.Key)
	})
}

func TestLegacyAliases(t *testing.T) {
	t.Parallel()

	// Verify legacy type aliases work
	var session SessionEvent = Session{ID: "test"}
	assert.Equal(t, "test", session.ID)

	var task TaskEvent = Task{ID: "task1"}
	assert.Equal(t, "task1", task.ID)

	var input InputRequestEvent = InputRequest{ID: "input1"}
	assert.Equal(t, "input1", input.ID)
}
