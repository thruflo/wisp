package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/durable-streams/durable-streams/packages/caddy-plugin/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStreamManager(t *testing.T) {
	sm, err := NewStreamManager()
	require.NoError(t, err)
	require.NotNil(t, sm)
	defer sm.Close()

	// Should have created a store
	assert.NotNil(t, sm.Store())

	// Should have a stream path
	assert.Equal(t, "/wisp/events", sm.StreamPath())
}

func TestStreamManager_BroadcastSession(t *testing.T) {
	sm, err := NewStreamManager()
	require.NoError(t, err)
	defer sm.Close()

	session := &Session{
		ID:        "test-session-1",
		Repo:      "user/repo",
		Branch:    "wisp/feature",
		Spec:      "docs/rfc.md",
		Status:    SessionStatusRunning,
		Iteration: 1,
		StartedAt: "2024-01-15T10:00:00Z",
	}

	err = sm.BroadcastSession(session)
	require.NoError(t, err)

	// Read the message back
	messages, _, err := sm.Read(store.ZeroOffset)
	require.NoError(t, err)
	require.Len(t, messages, 1)

	// Verify serialization
	var msg StreamMessage
	err = json.Unmarshal(messages[0].Data, &msg)
	require.NoError(t, err)
	assert.Equal(t, MessageTypeSession, msg.Type)

	// Verify data can be converted to Session
	dataBytes, err := json.Marshal(msg.Data)
	require.NoError(t, err)
	var s Session
	err = json.Unmarshal(dataBytes, &s)
	require.NoError(t, err)
	assert.Equal(t, "test-session-1", s.ID)
	assert.Equal(t, "user/repo", s.Repo)
	assert.Equal(t, SessionStatusRunning, s.Status)
}

func TestStreamManager_BroadcastSession_Nil(t *testing.T) {
	sm, err := NewStreamManager()
	require.NoError(t, err)
	defer sm.Close()

	err = sm.BroadcastSession(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestStreamManager_BroadcastTask(t *testing.T) {
	sm, err := NewStreamManager()
	require.NoError(t, err)
	defer sm.Close()

	task := &Task{
		ID:        "task-1",
		SessionID: "test-session-1",
		Order:     0,
		Content:   "Implement feature X",
		Status:    TaskStatusInProgress,
	}

	err = sm.BroadcastTask(task)
	require.NoError(t, err)

	// Read the message back
	messages, _, err := sm.Read(store.ZeroOffset)
	require.NoError(t, err)
	require.Len(t, messages, 1)

	// Verify serialization
	var msg StreamMessage
	err = json.Unmarshal(messages[0].Data, &msg)
	require.NoError(t, err)
	assert.Equal(t, MessageTypeTask, msg.Type)

	// Verify data
	dataBytes, err := json.Marshal(msg.Data)
	require.NoError(t, err)
	var tsk Task
	err = json.Unmarshal(dataBytes, &tsk)
	require.NoError(t, err)
	assert.Equal(t, "task-1", tsk.ID)
	assert.Equal(t, TaskStatusInProgress, tsk.Status)
}

func TestStreamManager_BroadcastTask_Nil(t *testing.T) {
	sm, err := NewStreamManager()
	require.NoError(t, err)
	defer sm.Close()

	err = sm.BroadcastTask(nil)
	assert.Error(t, err)
}

func TestStreamManager_BroadcastClaudeEvent(t *testing.T) {
	sm, err := NewStreamManager()
	require.NoError(t, err)
	defer sm.Close()

	// Use a map to simulate raw SDK message
	sdkMessage := map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"content": []any{
				map[string]any{
					"type": "text",
					"text": "Hello, world!",
				},
			},
		},
	}

	event := &ClaudeEvent{
		ID:        "session-1-1-42",
		SessionID: "session-1",
		Iteration: 1,
		Sequence:  42,
		Message:   sdkMessage,
		Timestamp: "2024-01-15T10:30:00Z",
	}

	err = sm.BroadcastClaudeEvent(event)
	require.NoError(t, err)

	// Read and verify
	messages, _, err := sm.Read(store.ZeroOffset)
	require.NoError(t, err)
	require.Len(t, messages, 1)

	var msg StreamMessage
	err = json.Unmarshal(messages[0].Data, &msg)
	require.NoError(t, err)
	assert.Equal(t, MessageTypeClaudeEvent, msg.Type)

	// Verify the message field contains the SDK message
	dataBytes, err := json.Marshal(msg.Data)
	require.NoError(t, err)
	var evt ClaudeEvent
	err = json.Unmarshal(dataBytes, &evt)
	require.NoError(t, err)
	assert.Equal(t, "session-1-1-42", evt.ID)
	assert.Equal(t, 42, evt.Sequence)

	// Verify the SDK message is preserved
	msgMap, ok := evt.Message.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "assistant", msgMap["type"])
}

func TestStreamManager_BroadcastClaudeEvent_Nil(t *testing.T) {
	sm, err := NewStreamManager()
	require.NoError(t, err)
	defer sm.Close()

	err = sm.BroadcastClaudeEvent(nil)
	assert.Error(t, err)
}

func TestStreamManager_BroadcastInputRequest(t *testing.T) {
	sm, err := NewStreamManager()
	require.NoError(t, err)
	defer sm.Close()

	response := "yes, continue"
	req := &InputRequest{
		ID:        "input-1",
		SessionID: "session-1",
		Iteration: 2,
		Question:  "Should we continue?",
		Responded: true,
		Response:  &response,
	}

	err = sm.BroadcastInputRequest(req)
	require.NoError(t, err)

	// Read and verify
	messages, _, err := sm.Read(store.ZeroOffset)
	require.NoError(t, err)
	require.Len(t, messages, 1)

	var msg StreamMessage
	err = json.Unmarshal(messages[0].Data, &msg)
	require.NoError(t, err)
	assert.Equal(t, MessageTypeInputRequest, msg.Type)

	// Verify data
	dataBytes, err := json.Marshal(msg.Data)
	require.NoError(t, err)
	var ir InputRequest
	err = json.Unmarshal(dataBytes, &ir)
	require.NoError(t, err)
	assert.Equal(t, "input-1", ir.ID)
	assert.True(t, ir.Responded)
	require.NotNil(t, ir.Response)
	assert.Equal(t, "yes, continue", *ir.Response)
}

func TestStreamManager_BroadcastInputRequest_NotResponded(t *testing.T) {
	sm, err := NewStreamManager()
	require.NoError(t, err)
	defer sm.Close()

	req := &InputRequest{
		ID:        "input-2",
		SessionID: "session-1",
		Iteration: 3,
		Question:  "What should we do?",
		Responded: false,
		Response:  nil,
	}

	err = sm.BroadcastInputRequest(req)
	require.NoError(t, err)

	// Read and verify
	messages, _, err := sm.Read(store.ZeroOffset)
	require.NoError(t, err)
	require.Len(t, messages, 1)

	var msg StreamMessage
	err = json.Unmarshal(messages[0].Data, &msg)
	require.NoError(t, err)

	dataBytes, err := json.Marshal(msg.Data)
	require.NoError(t, err)
	var ir InputRequest
	err = json.Unmarshal(dataBytes, &ir)
	require.NoError(t, err)
	assert.False(t, ir.Responded)
	assert.Nil(t, ir.Response)
}

func TestStreamManager_BroadcastInputRequest_Nil(t *testing.T) {
	sm, err := NewStreamManager()
	require.NoError(t, err)
	defer sm.Close()

	err = sm.BroadcastInputRequest(nil)
	assert.Error(t, err)
}

func TestStreamManager_BroadcastDelete(t *testing.T) {
	sm, err := NewStreamManager()
	require.NoError(t, err)
	defer sm.Close()

	err = sm.BroadcastDelete("sessions", "session-1")
	require.NoError(t, err)

	// Read and verify
	messages, _, err := sm.Read(store.ZeroOffset)
	require.NoError(t, err)
	require.Len(t, messages, 1)

	var msg StreamMessage
	err = json.Unmarshal(messages[0].Data, &msg)
	require.NoError(t, err)
	assert.Equal(t, MessageTypeDelete, msg.Type)
	assert.Equal(t, "sessions", msg.Collection)
	assert.Equal(t, "session-1", msg.ID)
}

func TestStreamManager_BroadcastDelete_Empty(t *testing.T) {
	sm, err := NewStreamManager()
	require.NoError(t, err)
	defer sm.Close()

	err = sm.BroadcastDelete("", "id")
	assert.Error(t, err)

	err = sm.BroadcastDelete("sessions", "")
	assert.Error(t, err)
}

func TestStreamManager_MultipleMessages(t *testing.T) {
	sm, err := NewStreamManager()
	require.NoError(t, err)
	defer sm.Close()

	// Broadcast multiple messages
	session := &Session{
		ID:        "sess-1",
		Repo:      "user/repo",
		Branch:    "main",
		Spec:      "spec.md",
		Status:    SessionStatusRunning,
		Iteration: 1,
		StartedAt: "2024-01-15T10:00:00Z",
	}
	require.NoError(t, sm.BroadcastSession(session))

	task1 := &Task{
		ID:        "task-1",
		SessionID: "sess-1",
		Order:     0,
		Content:   "Task one",
		Status:    TaskStatusPending,
	}
	require.NoError(t, sm.BroadcastTask(task1))

	task2 := &Task{
		ID:        "task-2",
		SessionID: "sess-1",
		Order:     1,
		Content:   "Task two",
		Status:    TaskStatusPending,
	}
	require.NoError(t, sm.BroadcastTask(task2))

	// Read all messages
	messages, _, err := sm.Read(store.ZeroOffset)
	require.NoError(t, err)
	assert.Len(t, messages, 3)

	// Verify message order and types
	var msg0, msg1, msg2 StreamMessage
	require.NoError(t, json.Unmarshal(messages[0].Data, &msg0))
	require.NoError(t, json.Unmarshal(messages[1].Data, &msg1))
	require.NoError(t, json.Unmarshal(messages[2].Data, &msg2))

	assert.Equal(t, MessageTypeSession, msg0.Type)
	assert.Equal(t, MessageTypeTask, msg1.Type)
	assert.Equal(t, MessageTypeTask, msg2.Type)
}

func TestStreamManager_GetCurrentOffset(t *testing.T) {
	sm, err := NewStreamManager()
	require.NoError(t, err)
	defer sm.Close()

	// Get initial offset
	offset1, err := sm.GetCurrentOffset()
	require.NoError(t, err)

	// Broadcast a message
	session := &Session{
		ID:        "sess-1",
		Repo:      "user/repo",
		Branch:    "main",
		Spec:      "spec.md",
		Status:    SessionStatusRunning,
		Iteration: 1,
		StartedAt: "2024-01-15T10:00:00Z",
	}
	require.NoError(t, sm.BroadcastSession(session))

	// Get new offset
	offset2, err := sm.GetCurrentOffset()
	require.NoError(t, err)

	// Offset should have increased
	assert.True(t, offset2.ByteOffset > offset1.ByteOffset)
}

func TestStreamManager_ReadFromOffset(t *testing.T) {
	sm, err := NewStreamManager()
	require.NoError(t, err)
	defer sm.Close()

	// Broadcast first message
	session := &Session{
		ID:        "sess-1",
		Repo:      "user/repo",
		Branch:    "main",
		Spec:      "spec.md",
		Status:    SessionStatusRunning,
		Iteration: 1,
		StartedAt: "2024-01-15T10:00:00Z",
	}
	require.NoError(t, sm.BroadcastSession(session))

	// Get offset after first message
	offset, err := sm.GetCurrentOffset()
	require.NoError(t, err)

	// Broadcast second message
	task := &Task{
		ID:        "task-1",
		SessionID: "sess-1",
		Order:     0,
		Content:   "Task one",
		Status:    TaskStatusPending,
	}
	require.NoError(t, sm.BroadcastTask(task))

	// Read from offset (should only get second message)
	messages, _, err := sm.Read(offset)
	require.NoError(t, err)
	require.Len(t, messages, 1)

	var msg StreamMessage
	require.NoError(t, json.Unmarshal(messages[0].Data, &msg))
	assert.Equal(t, MessageTypeTask, msg.Type)
}

func TestStreamManager_WaitForMessages(t *testing.T) {
	sm, err := NewStreamManager()
	require.NoError(t, err)
	defer sm.Close()

	// Get current offset
	offset, err := sm.GetCurrentOffset()
	require.NoError(t, err)

	// Start a goroutine to broadcast a message after a delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		session := &Session{
			ID:        "sess-1",
			Repo:      "user/repo",
			Branch:    "main",
			Spec:      "spec.md",
			Status:    SessionStatusRunning,
			Iteration: 1,
			StartedAt: "2024-01-15T10:00:00Z",
		}
		sm.BroadcastSession(session)
	}()

	// Wait for messages
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	messages, timedOut, err := sm.WaitForMessages(ctx, offset, 500*time.Millisecond)
	require.NoError(t, err)
	assert.False(t, timedOut)
	require.Len(t, messages, 1)

	var msg StreamMessage
	require.NoError(t, json.Unmarshal(messages[0].Data, &msg))
	assert.Equal(t, MessageTypeSession, msg.Type)
}

func TestStreamManager_WaitForMessages_Timeout(t *testing.T) {
	sm, err := NewStreamManager()
	require.NoError(t, err)
	defer sm.Close()

	// Get current offset
	offset, err := sm.GetCurrentOffset()
	require.NoError(t, err)

	// Wait with short timeout, no messages will arrive
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	messages, timedOut, err := sm.WaitForMessages(ctx, offset, 50*time.Millisecond)
	require.NoError(t, err)
	assert.True(t, timedOut)
	assert.Empty(t, messages)
}

func TestStreamManager_GetCurrentState(t *testing.T) {
	sm, err := NewStreamManager()
	require.NoError(t, err)
	defer sm.Close()

	// Broadcast some state
	session := &Session{
		ID:        "sess-1",
		Repo:      "user/repo",
		Branch:    "main",
		Spec:      "spec.md",
		Status:    SessionStatusRunning,
		Iteration: 1,
		StartedAt: "2024-01-15T10:00:00Z",
	}
	require.NoError(t, sm.BroadcastSession(session))

	task := &Task{
		ID:        "task-1",
		SessionID: "sess-1",
		Order:     0,
		Content:   "Task one",
		Status:    TaskStatusPending,
	}
	require.NoError(t, sm.BroadcastTask(task))

	req := &InputRequest{
		ID:        "input-1",
		SessionID: "sess-1",
		Iteration: 1,
		Question:  "Question?",
		Responded: false,
		Response:  nil,
	}
	require.NoError(t, sm.BroadcastInputRequest(req))

	// Get current state
	sessions, tasks, inputRequests := sm.GetCurrentState()

	assert.Len(t, sessions, 1)
	assert.Equal(t, "sess-1", sessions[0].ID)

	assert.Len(t, tasks, 1)
	assert.Equal(t, "task-1", tasks[0].ID)

	assert.Len(t, inputRequests, 1)
	assert.Equal(t, "input-1", inputRequests[0].ID)
}

func TestStreamManager_GetCurrentState_AfterDelete(t *testing.T) {
	sm, err := NewStreamManager()
	require.NoError(t, err)
	defer sm.Close()

	// Broadcast a session
	session := &Session{
		ID:        "sess-1",
		Repo:      "user/repo",
		Branch:    "main",
		Spec:      "spec.md",
		Status:    SessionStatusRunning,
		Iteration: 1,
		StartedAt: "2024-01-15T10:00:00Z",
	}
	require.NoError(t, sm.BroadcastSession(session))

	// Delete the session
	require.NoError(t, sm.BroadcastDelete("sessions", "sess-1"))

	// Get current state
	sessions, _, _ := sm.GetCurrentState()
	assert.Empty(t, sessions)
}

func TestStreamManager_GetCurrentState_UpdatesExisting(t *testing.T) {
	sm, err := NewStreamManager()
	require.NoError(t, err)
	defer sm.Close()

	// Broadcast initial session
	session := &Session{
		ID:        "sess-1",
		Repo:      "user/repo",
		Branch:    "main",
		Spec:      "spec.md",
		Status:    SessionStatusRunning,
		Iteration: 1,
		StartedAt: "2024-01-15T10:00:00Z",
	}
	require.NoError(t, sm.BroadcastSession(session))

	// Broadcast updated session
	session.Status = SessionStatusNeedsInput
	session.Iteration = 2
	require.NoError(t, sm.BroadcastSession(session))

	// Get current state (should have updated session)
	sessions, _, _ := sm.GetCurrentState()
	require.Len(t, sessions, 1)
	assert.Equal(t, SessionStatusNeedsInput, sessions[0].Status)
	assert.Equal(t, 2, sessions[0].Iteration)
}

func TestSessionStatus_Values(t *testing.T) {
	assert.Equal(t, SessionStatus("running"), SessionStatusRunning)
	assert.Equal(t, SessionStatus("needs_input"), SessionStatusNeedsInput)
	assert.Equal(t, SessionStatus("blocked"), SessionStatusBlocked)
	assert.Equal(t, SessionStatus("done"), SessionStatusDone)
}

func TestTaskStatus_Values(t *testing.T) {
	assert.Equal(t, TaskStatus("pending"), TaskStatusPending)
	assert.Equal(t, TaskStatus("in_progress"), TaskStatusInProgress)
	assert.Equal(t, TaskStatus("completed"), TaskStatusCompleted)
}

func TestMessageType_Values(t *testing.T) {
	assert.Equal(t, MessageType("session"), MessageTypeSession)
	assert.Equal(t, MessageType("task"), MessageTypeTask)
	assert.Equal(t, MessageType("claude_event"), MessageTypeClaudeEvent)
	assert.Equal(t, MessageType("input_request"), MessageTypeInputRequest)
	assert.Equal(t, MessageType("delete"), MessageTypeDelete)
}
