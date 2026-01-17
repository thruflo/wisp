package tui

import (
	"context"
	"fmt"
	"io"
	"sync"
)

// View represents the current TUI view.
type View int

const (
	ViewSummary View = iota
	ViewTail
	ViewInput
)

// String returns the string representation of the view.
func (v View) String() string {
	switch v {
	case ViewSummary:
		return "summary"
	case ViewTail:
		return "tail"
	case ViewInput:
		return "input"
	default:
		return "unknown"
	}
}

// Action represents a user action from the TUI.
type Action int

const (
	ActionNone        Action = iota
	ActionAttach             // User requested attach to Sprite console
	ActionKill               // User requested kill session
	ActionBackground         // User requested backgrounding (esc)
	ActionQuit               // User requested quit (ctrl+c)
	ActionSubmitInput        // User submitted input response
	ActionCancelInput        // User cancelled input (esc from input view)
)

// String returns the string representation of the action.
func (a Action) String() string {
	switch a {
	case ActionNone:
		return "none"
	case ActionAttach:
		return "attach"
	case ActionKill:
		return "kill"
	case ActionBackground:
		return "background"
	case ActionQuit:
		return "quit"
	case ActionSubmitInput:
		return "submit_input"
	case ActionCancelInput:
		return "cancel_input"
	default:
		return "unknown"
	}
}

// ActionEvent is sent when the user triggers an action.
type ActionEvent struct {
	Action Action
	Input  string // Only set for ActionSubmitInput
}

// TUI manages the terminal user interface.
type TUI struct {
	terminal    *Terminal
	keyReader   *KeyReader
	out         io.Writer
	mu          sync.Mutex
	state       ViewState
	view        View
	tailView    *TailView
	inputView   *InputView
	summaryView *SummaryView
	width       int
	height      int
	running     bool
	actionCh    chan ActionEvent
}

// NewTUI creates a new TUI instance.
func NewTUI(out io.Writer) *TUI {
	terminal := NewTerminal(out)
	return &TUI{
		terminal:    terminal,
		out:         out,
		view:        ViewSummary,
		tailView:    NewTailView(1000),
		summaryView: &SummaryView{},
		width:       80,
		height:      24,
		actionCh:    make(chan ActionEvent, 10),
	}
}

// NewNopTUI creates a no-op TUI that discards all output and never blocks.
// This is used for headless mode where no terminal interaction is needed.
func NewNopTUI() *TUI {
	return &TUI{
		terminal:    NewTerminal(io.Discard),
		out:         io.Discard,
		view:        ViewSummary,
		tailView:    NewTailView(0), // No buffering needed
		summaryView: &SummaryView{},
		width:       80,
		height:      24,
		actionCh:    make(chan ActionEvent, 10),
	}
}

// SetState updates the view state.
func (t *TUI) SetState(state ViewState) {
	t.mu.Lock()
	t.state = state
	t.mu.Unlock()
}

// GetState returns the current view state.
func (t *TUI) GetState() ViewState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

// SetView switches to a different view.
func (t *TUI) SetView(v View) {
	t.mu.Lock()
	t.view = v
	t.mu.Unlock()
}

// GetView returns the current view.
func (t *TUI) GetView() View {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.view
}

// AppendTailLine adds a line to the tail view.
func (t *TUI) AppendTailLine(line string) {
	t.mu.Lock()
	t.tailView.Append(line)
	t.mu.Unlock()
}

// AppendTailLines adds multiple lines to the tail view.
func (t *TUI) AppendTailLines(lines []string) {
	t.mu.Lock()
	t.tailView.AppendLines(lines)
	t.mu.Unlock()
}

// ClearTail clears the tail view buffer.
func (t *TUI) ClearTail() {
	t.mu.Lock()
	t.tailView.Clear()
	t.mu.Unlock()
}

// ShowInput switches to the input view with the given question.
func (t *TUI) ShowInput(question string) {
	t.mu.Lock()
	t.state.Question = question
	if t.inputView == nil {
		t.inputView = NewInputView(t.terminal)
	} else {
		t.inputView.Reset()
	}
	t.view = ViewInput
	t.mu.Unlock()
}

// Actions returns a channel that receives user actions.
func (t *TUI) Actions() <-chan ActionEvent {
	return t.actionCh
}

// Update redraws the current view.
func (t *TUI) Update() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.running {
		return
	}

	// Get terminal size
	width, height, err := t.terminal.Size()
	if err == nil {
		t.width = width
		t.height = height
	}

	// Clear and redraw
	t.terminal.Clear()
	t.terminal.HideCursor()

	var lines []string
	switch t.view {
	case ViewSummary:
		lines = t.summaryView.Render(t.state, t.width)
	case ViewTail:
		lines = t.tailView.Render(t.width, t.height-2)
	case ViewInput:
		if t.inputView != nil {
			lines = t.inputView.Render(t.state.Question, t.width)
		}
	}

	// Render lines
	for _, line := range lines {
		t.terminal.WriteLine(line)
	}

	// Show cursor for input view
	if t.view == ViewInput {
		t.terminal.ShowCursor()
	}
}

// Run starts the TUI event loop.
// It returns when the context is cancelled or the user triggers an exit action.
func (t *TUI) Run(ctx context.Context) error {
	// Enter raw mode
	if err := t.terminal.EnterRaw(); err != nil {
		return fmt.Errorf("failed to enter raw mode: %w", err)
	}
	defer t.terminal.ExitRaw()
	defer t.terminal.ShowCursor()

	t.running = true
	defer func() { t.running = false }()

	// Initialize key reader
	t.keyReader = NewKeyReader(t.terminal)

	// Initial render
	t.Update()

	// Input channel for key events
	keyCh := make(chan KeyEvent, 10)
	keyErr := make(chan error, 1)

	// Start key reader goroutine
	go func() {
		for {
			ev, err := t.keyReader.ReadKey()
			if err != nil {
				keyErr <- err
				return
			}
			select {
			case keyCh <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()

	// Event loop
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case err := <-keyErr:
			// Reader error is usually EOF, which is expected on exit
			if err == io.EOF {
				return nil
			}
			return err

		case ev := <-keyCh:
			action := t.handleKeyEvent(ev)
			if action.Action != ActionNone {
				select {
				case t.actionCh <- action:
				default:
					// Channel full, drop event
				}

				// Exit on certain actions
				switch action.Action {
				case ActionQuit, ActionBackground:
					return nil
				}
			}
		}
	}
}

// handleKeyEvent processes a key event and returns any triggered action.
func (t *TUI) handleKeyEvent(ev KeyEvent) ActionEvent {
	t.mu.Lock()
	currentView := t.view
	t.mu.Unlock()

	// Handle input view specially
	if currentView == ViewInput {
		return t.handleInputKey(ev)
	}

	// Handle shortcuts for summary and tail views
	shortcut := ParseShortcut(ev)
	switch shortcut {
	case ShortcutTail:
		t.mu.Lock()
		if t.view == ViewTail {
			t.view = ViewSummary
		} else {
			t.view = ViewTail
		}
		t.mu.Unlock()
		t.Update()
		return ActionEvent{Action: ActionNone}

	case ShortcutAttach:
		return ActionEvent{Action: ActionAttach}

	case ShortcutDetach:
		t.mu.Lock()
		if t.view == ViewTail {
			t.view = ViewSummary
			t.mu.Unlock()
			t.Update()
		} else {
			t.mu.Unlock()
		}
		return ActionEvent{Action: ActionNone}

	case ShortcutKill:
		return ActionEvent{Action: ActionKill}

	case ShortcutEscape:
		return ActionEvent{Action: ActionBackground}

	case ShortcutQuit:
		return ActionEvent{Action: ActionQuit}
	}

	return ActionEvent{Action: ActionNone}
}

// handleInputKey processes a key event in input view.
func (t *TUI) handleInputKey(ev KeyEvent) ActionEvent {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Handle escape to cancel input
	if ev.Key == KeyEscape {
		t.view = ViewSummary
		t.inputView.Reset()
		t.mu.Unlock()
		t.Update()
		t.mu.Lock()
		return ActionEvent{Action: ActionCancelInput}
	}

	// Handle Ctrl+C to quit
	if ev.Key == KeyCtrlC {
		return ActionEvent{Action: ActionQuit}
	}

	// Pass to line editor
	if t.inputView != nil {
		if t.inputView.editor.HandleKey(ev) {
			// Enter pressed - submit input
			input := t.inputView.editor.Text()
			t.view = ViewSummary
			t.inputView.Reset()
			t.mu.Unlock()
			t.Update()
			t.mu.Lock()
			return ActionEvent{Action: ActionSubmitInput, Input: input}
		}
	}

	// Redraw to show updated input
	t.mu.Unlock()
	t.Update()
	t.mu.Lock()

	return ActionEvent{Action: ActionNone}
}

// Stop signals the TUI to stop running.
// This is a no-op if the TUI is not running.
func (t *TUI) Stop() {
	t.mu.Lock()
	t.running = false
	t.mu.Unlock()
}

// IsRunning returns whether the TUI is currently running.
func (t *TUI) IsRunning() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.running
}

// Bell sounds the terminal bell.
func (t *TUI) Bell() {
	t.terminal.RingBell()
}
