package tui

import (
	"fmt"
	"strings"
)

// ViewState holds the data needed to render views.
type ViewState struct {
	Branch         string
	Iteration      int
	MaxIterations  int
	Status         string
	CompletedTasks int
	TotalTasks     int
	LastSummary    string
	Question       string // For NEEDS_INPUT view
	Error          string // For BLOCKED status
}

// SummaryView renders the default summary view showing session status.
type SummaryView struct{}

// Render renders the summary view to a slice of strings.
// Width specifies the terminal width for the view.
func (v *SummaryView) Render(state ViewState, width int) []string {
	if width < 20 {
		width = 20
	}

	innerWidth := width - 4 // Account for border padding

	// Build content lines
	var content []string

	// Title line: "wisp: <branch> | iteration X/Y"
	title := fmt.Sprintf("wisp: %s | iteration %d/%d",
		Truncate(state.Branch, innerWidth-30),
		state.Iteration,
		state.MaxIterations)
	content = append(content, title)

	// Status line: "status: <STATUS> | tasks: X/Y complete"
	statusText := FormatStatus(state.Status)
	statusLine := fmt.Sprintf("status: %s | tasks: %d/%d complete",
		statusText,
		state.CompletedTasks,
		state.TotalTasks)
	content = append(content, statusLine)

	// Last summary line
	if state.LastSummary != "" {
		summaryText := Truncate(state.LastSummary, innerWidth-8)
		content = append(content, fmt.Sprintf("last: %q", summaryText))
	}

	// Error line (if blocked)
	if state.Error != "" && strings.ToUpper(state.Status) == "BLOCKED" {
		errorText := Truncate(state.Error, innerWidth-8)
		content = append(content, Style("error: "+errorText, FgRed))
	}

	// Empty line before shortcuts
	content = append(content, "")

	// Shortcuts line
	shortcuts := "[t]ail [a]ttach [k]ill [esc]background"
	content = append(content, Style(shortcuts, Dim))

	return BoxWithContent(width, content)
}

// TailView renders the tail view showing streaming output.
type TailView struct {
	lines      []string
	maxLines   int
	scrollback int // Number of lines above the visible area
}

// NewTailView creates a TailView with the specified maximum line buffer.
func NewTailView(maxLines int) *TailView {
	if maxLines < 1 {
		maxLines = 1000 // Default buffer size
	}
	return &TailView{
		lines:    make([]string, 0, maxLines),
		maxLines: maxLines,
	}
}

// Append adds a line to the tail view buffer.
func (v *TailView) Append(line string) {
	v.lines = append(v.lines, line)
	// Trim if we exceed max lines
	if len(v.lines) > v.maxLines {
		// Keep only the most recent maxLines
		v.lines = v.lines[len(v.lines)-v.maxLines:]
	}
}

// AppendLines adds multiple lines to the tail view buffer.
func (v *TailView) AppendLines(lines []string) {
	for _, line := range lines {
		v.Append(line)
	}
}

// Clear removes all lines from the buffer.
func (v *TailView) Clear() {
	v.lines = v.lines[:0]
	v.scrollback = 0
}

// Lines returns all lines in the buffer.
func (v *TailView) Lines() []string {
	return v.lines
}

// Render renders the tail view with a header showing how to exit.
// height is the number of content lines to display (excluding header/footer).
func (v *TailView) Render(width, height int) []string {
	if width < 20 {
		width = 20
	}
	if height < 3 {
		height = 3
	}

	result := make([]string, 0, height+2)

	// Header line
	header := Style("─── Output (press 't' or 'd' to return) ", Dim) +
		strings.Repeat("─", max(0, width-40))
	result = append(result, header)

	// Calculate visible window
	totalLines := len(v.lines)
	visibleLines := height

	start := 0
	if totalLines > visibleLines {
		start = totalLines - visibleLines
	}

	// Render visible lines
	for i := start; i < totalLines; i++ {
		line := v.lines[i]
		if len(line) > width {
			line = line[:width]
		}
		result = append(result, line)
	}

	// Pad remaining space
	for len(result) < height+1 {
		result = append(result, "")
	}

	// Footer with line count
	footer := Style(fmt.Sprintf("─── %d lines ", totalLines), Dim) +
		strings.Repeat("─", max(0, width-15))
	result = append(result, footer)

	return result
}

// InputView renders the input view for NEEDS_INPUT responses.
type InputView struct {
	editor *LineEditor
}

// NewInputView creates an InputView with the given terminal for cursor control.
func NewInputView(t *Terminal) *InputView {
	return &InputView{
		editor: NewLineEditor(t),
	}
}

// Editor returns the line editor for handling key events.
func (v *InputView) Editor() *LineEditor {
	return v.editor
}

// Reset clears the input buffer.
func (v *InputView) Reset() {
	v.editor.Clear()
}

// Render renders the input view with the question and input field.
func (v *InputView) Render(question string, width int) []string {
	if width < 20 {
		width = 20
	}

	innerWidth := width - 4

	var content []string

	// Header
	content = append(content, Style("Input Required", Bold, FgYellow))
	content = append(content, "")

	// Question text (wrapped)
	wrappedQuestion := WrapText(question, innerWidth)
	for _, line := range wrappedQuestion {
		content = append(content, line)
	}

	content = append(content, "")

	// Input field with prompt
	inputText := v.editor.Text()
	cursor := v.editor.Cursor()

	// Show input line with visual cursor
	prompt := "> "
	maxInput := innerWidth - len(prompt)

	displayText := inputText
	cursorPos := cursor
	if len(inputText) > maxInput {
		// Scroll the input to keep cursor visible
		start := 0
		if cursor > maxInput-3 {
			start = cursor - maxInput + 3
		}
		end := start + maxInput
		if end > len(inputText) {
			end = len(inputText)
		}
		displayText = inputText[start:end]
		cursorPos = cursor - start
	}

	// Build input line with cursor indicator
	inputLine := prompt + displayText
	content = append(content, inputLine)

	// Cursor position line (underline)
	cursorLine := strings.Repeat(" ", len(prompt)+cursorPos) + "^"
	content = append(content, Style(cursorLine, FgCyan))

	content = append(content, "")
	content = append(content, Style("Press Enter to submit, Esc to cancel", Dim))

	return BoxWithContent(width, content)
}

// max returns the larger of two integers.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
