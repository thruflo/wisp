package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/state"
)

// mockSessionReader implements SessionReader for testing.
type mockSessionReader struct {
	sessions []*config.Session
	states   map[string]*state.State
	tasks    map[string][]state.Task
	history  map[string][]state.History
	err      error
}

func newMockSessionReader() *mockSessionReader {
	return &mockSessionReader{
		sessions: []*config.Session{},
		states:   make(map[string]*state.State),
		tasks:    make(map[string][]state.Task),
		history:  make(map[string][]state.History),
	}
}

func (m *mockSessionReader) ListSessions() ([]*config.Session, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.sessions, nil
}

func (m *mockSessionReader) GetSession(branch string) (*config.Session, error) {
	if m.err != nil {
		return nil, m.err
	}
	for _, s := range m.sessions {
		if s.Branch == branch {
			return s, nil
		}
	}
	return nil, fmt.Errorf("session not found: %s", branch)
}

func (m *mockSessionReader) LoadState(branch string) (*state.State, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.states[branch], nil
}

func (m *mockSessionReader) LoadTasks(branch string) ([]state.Task, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.tasks[branch], nil
}

func (m *mockSessionReader) LoadHistory(branch string) ([]state.History, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.history[branch], nil
}

func (m *mockSessionReader) addSession(s *config.Session) {
	m.sessions = append(m.sessions, s)
}

func captureOutput(f func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	f()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

func TestStatusCommand_ListSessions_Empty(t *testing.T) {
	mock := newMockSessionReader()
	statusStore = mock
	defer func() { statusStore = nil }()

	output := captureOutput(func() {
		err := runStatus(statusCmd, []string{})
		require.NoError(t, err)
	})

	assert.Contains(t, output, "No sessions found")
}

func TestStatusCommand_ListSessions(t *testing.T) {
	mock := newMockSessionReader()
	statusStore = mock
	defer func() { statusStore = nil }()

	// Add sessions
	mock.addSession(&config.Session{
		Repo:       "org/repo1",
		Branch:     "wisp/feat1",
		SpriteName: "wisp-111",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	})
	mock.addSession(&config.Session{
		Repo:       "org/repo2",
		Branch:     "wisp/feat2",
		SpriteName: "wisp-222",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusStopped,
	})

	// Add tasks for progress display
	mock.tasks["wisp/feat1"] = []state.Task{
		{Description: "Task 1", Passes: true},
		{Description: "Task 2", Passes: true},
		{Description: "Task 3", Passes: false},
	}
	mock.tasks["wisp/feat2"] = []state.Task{
		{Description: "Task 1", Passes: true},
	}

	output := captureOutput(func() {
		err := runStatus(statusCmd, []string{})
		require.NoError(t, err)
	})

	// Verify header
	assert.Contains(t, output, "BRANCH")
	assert.Contains(t, output, "STATUS")
	assert.Contains(t, output, "TASKS")

	// Verify session data
	assert.Contains(t, output, "wisp/feat1")
	assert.Contains(t, output, "running")
	assert.Contains(t, output, "2/3")

	assert.Contains(t, output, "wisp/feat2")
	assert.Contains(t, output, "stopped")
	assert.Contains(t, output, "1/1")
}

func TestStatusCommand_ShowSession(t *testing.T) {
	mock := newMockSessionReader()
	statusStore = mock
	defer func() { statusStore = nil }()

	startTime := time.Date(2026, 1, 16, 10, 0, 0, 0, time.UTC)
	mock.addSession(&config.Session{
		Repo:       "electric-sql/electric",
		Spec:       "docs/rfc.md",
		Branch:     "wisp/feat-auth",
		SpriteName: "wisp-abc123",
		StartedAt:  startTime,
		Status:     config.SessionStatusRunning,
	})

	mock.states["wisp/feat-auth"] = &state.State{
		Status:  state.StatusContinue,
		Summary: "Implemented JWT validation",
	}

	mock.tasks["wisp/feat-auth"] = []state.Task{
		{Description: "Setup", Passes: true},
		{Description: "Auth", Passes: true},
		{Description: "Tests", Passes: false},
		{Description: "Docs", Passes: false},
	}

	mock.history["wisp/feat-auth"] = []state.History{
		{Iteration: 1, Summary: "Initial setup", TasksCompleted: 1},
		{Iteration: 2, Summary: "Added auth", TasksCompleted: 2},
		{Iteration: 3, Summary: "JWT validation", TasksCompleted: 2},
	}

	output := captureOutput(func() {
		err := runStatus(statusCmd, []string{"wisp/feat-auth"})
		require.NoError(t, err)
	})

	// Verify session details
	assert.Contains(t, output, "Session Details")
	assert.Contains(t, output, "electric-sql/electric")
	assert.Contains(t, output, "docs/rfc.md")
	assert.Contains(t, output, "wisp/feat-auth")
	assert.Contains(t, output, "wisp-abc123")
	assert.Contains(t, output, "2026-01-16")
	assert.Contains(t, output, "running")

	// Verify progress
	assert.Contains(t, output, "Progress")
	assert.Contains(t, output, "2/4 completed")
	assert.Contains(t, output, "3") // iteration count

	// Verify last summary
	assert.Contains(t, output, "Implemented JWT validation")
}

func TestStatusCommand_ShowSession_NeedsInput(t *testing.T) {
	mock := newMockSessionReader()
	statusStore = mock
	defer func() { statusStore = nil }()

	mock.addSession(&config.Session{
		Repo:       "org/repo",
		Branch:     "wisp/feature",
		SpriteName: "wisp-123",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	})

	mock.states["wisp/feature"] = &state.State{
		Status:   state.StatusNeedsInput,
		Summary:  "Waiting for input",
		Question: "Which authentication method should I use?",
	}

	output := captureOutput(func() {
		err := runStatus(statusCmd, []string{"wisp/feature"})
		require.NoError(t, err)
	})

	assert.Contains(t, output, "NEEDS_INPUT")
	assert.Contains(t, output, "Which authentication method should I use?")
}

func TestStatusCommand_ShowSession_Blocked(t *testing.T) {
	mock := newMockSessionReader()
	statusStore = mock
	defer func() { statusStore = nil }()

	mock.addSession(&config.Session{
		Repo:       "org/repo",
		Branch:     "wisp/feature",
		SpriteName: "wisp-123",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusStopped,
	})

	mock.states["wisp/feature"] = &state.State{
		Status:  state.StatusBlocked,
		Summary: "Cannot proceed",
		Error:   "Missing database credentials",
	}

	output := captureOutput(func() {
		err := runStatus(statusCmd, []string{"wisp/feature"})
		require.NoError(t, err)
	})

	assert.Contains(t, output, "BLOCKED")
	assert.Contains(t, output, "Missing database credentials")
}

func TestStatusCommand_ShowSession_NotFound(t *testing.T) {
	mock := newMockSessionReader()
	statusStore = mock
	defer func() { statusStore = nil }()

	err := runStatus(statusCmd, []string{"nonexistent"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

func TestStatusCommand_ShowSession_NoState(t *testing.T) {
	mock := newMockSessionReader()
	statusStore = mock
	defer func() { statusStore = nil }()

	mock.addSession(&config.Session{
		Repo:       "org/repo",
		Branch:     "wisp/feature",
		SpriteName: "wisp-123",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	})

	// No state, tasks, or history

	output := captureOutput(func() {
		err := runStatus(statusCmd, []string{"wisp/feature"})
		require.NoError(t, err)
	})

	// Should still show session info
	assert.Contains(t, output, "wisp/feature")
	assert.Contains(t, output, "0/0 completed")
	assert.Contains(t, output, "0") // iterations
}

func TestCountTasks(t *testing.T) {
	tests := []struct {
		name          string
		tasks         []state.Task
		wantCompleted int
		wantTotal     int
	}{
		{
			name:          "empty",
			tasks:         nil,
			wantCompleted: 0,
			wantTotal:     0,
		},
		{
			name: "all complete",
			tasks: []state.Task{
				{Passes: true},
				{Passes: true},
			},
			wantCompleted: 2,
			wantTotal:     2,
		},
		{
			name: "none complete",
			tasks: []state.Task{
				{Passes: false},
				{Passes: false},
			},
			wantCompleted: 0,
			wantTotal:     2,
		},
		{
			name: "mixed",
			tasks: []state.Task{
				{Passes: true},
				{Passes: false},
				{Passes: true},
				{Passes: false},
			},
			wantCompleted: 2,
			wantTotal:     4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			completed, total := countTasks(tt.tasks)
			assert.Equal(t, tt.wantCompleted, completed)
			assert.Equal(t, tt.wantTotal, total)
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{"seconds only", 45 * time.Second, "45s"},
		{"minutes and seconds", 5*time.Minute + 30*time.Second, "5m 30s"},
		{"hours minutes seconds", 2*time.Hour + 15*time.Minute + 45*time.Second, "2h 15m 45s"},
		{"zero", 0, "0s"},
		{"just minutes", 10 * time.Minute, "10m 0s"},
		{"just hours", 3 * time.Hour, "3h 0m 0s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDuration(tt.duration)
			assert.Equal(t, tt.want, got)
		})
	}
}
