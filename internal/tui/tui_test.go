package tui

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestView_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		view View
		want string
	}{
		{ViewSummary, "summary"},
		{ViewTail, "tail"},
		{ViewInput, "input"},
		{View(99), "unknown"},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, tt.view.String())
	}
}

func TestAction_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		action Action
		want   string
	}{
		{ActionNone, "none"},
		{ActionAttach, "attach"},
		{ActionKill, "kill"},
		{ActionBackground, "background"},
		{ActionQuit, "quit"},
		{ActionSubmitInput, "submit_input"},
		{ActionCancelInput, "cancel_input"},
		{Action(99), "unknown"},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, tt.action.String())
	}
}

func TestNewTUI(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	tui := NewTUI(buf)

	require.NotNil(t, tui)
	assert.NotNil(t, tui.terminal)
	assert.NotNil(t, tui.tailView)
	assert.NotNil(t, tui.summaryView)
	assert.NotNil(t, tui.actionCh)
	assert.Equal(t, ViewSummary, tui.view)
	assert.False(t, tui.running)
}

func TestTUI_SetGetState(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	tui := NewTUI(buf)

	state := ViewState{
		Branch:         "test-branch",
		Iteration:      5,
		MaxIterations:  10,
		Status:         "CONTINUE",
		CompletedTasks: 2,
		TotalTasks:     5,
	}

	tui.SetState(state)
	got := tui.GetState()

	assert.Equal(t, state.Branch, got.Branch)
	assert.Equal(t, state.Iteration, got.Iteration)
	assert.Equal(t, state.MaxIterations, got.MaxIterations)
	assert.Equal(t, state.Status, got.Status)
	assert.Equal(t, state.CompletedTasks, got.CompletedTasks)
	assert.Equal(t, state.TotalTasks, got.TotalTasks)
}

func TestTUI_SetGetView(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	tui := NewTUI(buf)

	assert.Equal(t, ViewSummary, tui.GetView())

	tui.SetView(ViewTail)
	assert.Equal(t, ViewTail, tui.GetView())

	tui.SetView(ViewInput)
	assert.Equal(t, ViewInput, tui.GetView())

	tui.SetView(ViewSummary)
	assert.Equal(t, ViewSummary, tui.GetView())
}

func TestTUI_TailView(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	tui := NewTUI(buf)

	tui.AppendTailLine("line 1")
	tui.AppendTailLine("line 2")

	assert.Len(t, tui.tailView.Lines(), 2)

	tui.AppendTailLines([]string{"line 3", "line 4"})
	assert.Len(t, tui.tailView.Lines(), 4)

	tui.ClearTail()
	assert.Empty(t, tui.tailView.Lines())
}

func TestTUI_ShowInput(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	tui := NewTUI(buf)

	tui.ShowInput("What is your question?")

	assert.Equal(t, ViewInput, tui.GetView())
	assert.Equal(t, "What is your question?", tui.GetState().Question)
	assert.NotNil(t, tui.inputView)
}

func TestTUI_ShowInput_ResetsEditor(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	tui := NewTUI(buf)

	// First input
	tui.ShowInput("Question 1")
	tui.inputView.editor.HandleKey(KeyEvent{Key: KeyRune, Rune: 'A'})
	assert.Equal(t, "A", tui.inputView.editor.Text())

	// Second input should reset
	tui.ShowInput("Question 2")
	assert.Equal(t, "", tui.inputView.editor.Text())
}

func TestTUI_Actions(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	tui := NewTUI(buf)

	// Actions channel should be available
	ch := tui.Actions()
	require.NotNil(t, ch)
}

func TestTUI_IsRunning(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	tui := NewTUI(buf)

	assert.False(t, tui.IsRunning())

	tui.running = true
	assert.True(t, tui.IsRunning())

	tui.Stop()
	assert.False(t, tui.IsRunning())
}

func TestTUI_HandleKeyEvent_Summary(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	tui := NewTUI(buf)

	tests := []struct {
		name       string
		key        KeyEvent
		wantView   View
		wantAction Action
	}{
		{
			name:       "t toggles to tail",
			key:        KeyEvent{Key: KeyRune, Rune: 't'},
			wantView:   ViewTail,
			wantAction: ActionNone,
		},
		{
			name:       "a triggers attach",
			key:        KeyEvent{Key: KeyRune, Rune: 'a'},
			wantView:   ViewSummary, // View doesn't change
			wantAction: ActionAttach,
		},
		{
			name:       "k triggers kill",
			key:        KeyEvent{Key: KeyRune, Rune: 'k'},
			wantView:   ViewSummary,
			wantAction: ActionKill,
		},
		{
			name:       "escape triggers background",
			key:        KeyEvent{Key: KeyEscape},
			wantView:   ViewSummary,
			wantAction: ActionBackground,
		},
		{
			name:       "ctrl+c triggers quit",
			key:        KeyEvent{Key: KeyCtrlC},
			wantView:   ViewSummary,
			wantAction: ActionQuit,
		},
		{
			name:       "d does nothing in summary",
			key:        KeyEvent{Key: KeyRune, Rune: 'd'},
			wantView:   ViewSummary,
			wantAction: ActionNone,
		},
		{
			name:       "unknown key does nothing",
			key:        KeyEvent{Key: KeyRune, Rune: 'x'},
			wantView:   ViewSummary,
			wantAction: ActionNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset view to summary
			tui.SetView(ViewSummary)

			action := tui.handleKeyEvent(tt.key)

			assert.Equal(t, tt.wantView, tui.GetView(), "view mismatch")
			assert.Equal(t, tt.wantAction, action.Action, "action mismatch")
		})
	}
}

func TestTUI_HandleKeyEvent_Tail(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	tui := NewTUI(buf)
	tui.SetView(ViewTail)

	tests := []struct {
		name       string
		key        KeyEvent
		wantView   View
		wantAction Action
	}{
		{
			name:       "t toggles back to summary",
			key:        KeyEvent{Key: KeyRune, Rune: 't'},
			wantView:   ViewSummary,
			wantAction: ActionNone,
		},
		{
			name:       "d returns to summary",
			key:        KeyEvent{Key: KeyRune, Rune: 'd'},
			wantView:   ViewSummary,
			wantAction: ActionNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tui.SetView(ViewTail)

			action := tui.handleKeyEvent(tt.key)

			assert.Equal(t, tt.wantView, tui.GetView(), "view mismatch")
			assert.Equal(t, tt.wantAction, action.Action, "action mismatch")
		})
	}
}

func TestTUI_HandleInputKey(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	tui := NewTUI(buf)
	tui.ShowInput("Question?")

	// Test typing
	tui.handleInputKey(KeyEvent{Key: KeyRune, Rune: 'H'})
	tui.handleInputKey(KeyEvent{Key: KeyRune, Rune: 'i'})
	assert.Equal(t, "Hi", tui.inputView.editor.Text())

	// Test escape cancels
	tui.ShowInput("Question?")
	tui.inputView.editor.HandleKey(KeyEvent{Key: KeyRune, Rune: 'X'})
	action := tui.handleInputKey(KeyEvent{Key: KeyEscape})
	assert.Equal(t, ActionCancelInput, action.Action)
	assert.Equal(t, ViewSummary, tui.GetView())

	// Test ctrl+c quits
	tui.ShowInput("Question?")
	action = tui.handleInputKey(KeyEvent{Key: KeyCtrlC})
	assert.Equal(t, ActionQuit, action.Action)

	// Test enter submits
	tui.ShowInput("Question?")
	tui.inputView.editor.HandleKey(KeyEvent{Key: KeyRune, Rune: 'Y'})
	tui.inputView.editor.HandleKey(KeyEvent{Key: KeyRune, Rune: 'e'})
	tui.inputView.editor.HandleKey(KeyEvent{Key: KeyRune, Rune: 's'})
	action = tui.handleInputKey(KeyEvent{Key: KeyEnter})
	assert.Equal(t, ActionSubmitInput, action.Action)
	assert.Equal(t, "Yes", action.Input)
	assert.Equal(t, ViewSummary, tui.GetView())
}

func TestTUI_Bell(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	tui := NewTUI(buf)

	tui.Bell()

	assert.Contains(t, buf.String(), "\a")
}

func TestTUI_Update_NotRunning(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	tui := NewTUI(buf)

	// Update should be no-op when not running
	tui.Update()
	assert.Empty(t, buf.String())
}

func TestTUI_Concurrency(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	tui := NewTUI(buf)

	// Test concurrent access to state and view
	done := make(chan bool)

	go func() {
		for i := 0; i < 100; i++ {
			tui.SetState(ViewState{Iteration: i})
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			_ = tui.GetState()
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			if i%2 == 0 {
				tui.SetView(ViewTail)
			} else {
				tui.SetView(ViewSummary)
			}
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			_ = tui.GetView()
		}
		done <- true
	}()

	// Wait for all goroutines
	for i := 0; i < 4; i++ {
		<-done
	}
}

func TestTUI_TailView_Concurrency(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	tui := NewTUI(buf)

	done := make(chan bool)

	go func() {
		for i := 0; i < 100; i++ {
			tui.AppendTailLine("line")
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 50; i++ {
			tui.ClearTail()
		}
		done <- true
	}()

	<-done
	<-done
}
