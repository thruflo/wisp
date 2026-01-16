package tui

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

// Terminal handles raw terminal mode and provides ANSI escape helpers.
type Terminal struct {
	in       *os.File
	out      io.Writer
	oldState *term.State
	isRaw    bool
}

// NewTerminal creates a Terminal that reads from stdin and writes to the given writer.
func NewTerminal(out io.Writer) *Terminal {
	return &Terminal{
		in:  os.Stdin,
		out: out,
	}
}

// EnterRaw puts the terminal into raw mode.
// Returns an error if already in raw mode or if the operation fails.
func (t *Terminal) EnterRaw() error {
	if t.isRaw {
		return fmt.Errorf("terminal already in raw mode")
	}

	fd := int(t.in.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("failed to enter raw mode: %w", err)
	}

	t.oldState = oldState
	t.isRaw = true
	return nil
}

// ExitRaw restores the terminal to its original state.
// Safe to call even if not in raw mode.
func (t *Terminal) ExitRaw() error {
	if !t.isRaw || t.oldState == nil {
		return nil
	}

	fd := int(t.in.Fd())
	if err := term.Restore(fd, t.oldState); err != nil {
		return fmt.Errorf("failed to restore terminal: %w", err)
	}

	t.isRaw = false
	t.oldState = nil
	return nil
}

// IsRaw returns true if the terminal is in raw mode.
func (t *Terminal) IsRaw() bool {
	return t.isRaw
}

// Size returns the current terminal width and height.
func (t *Terminal) Size() (width, height int, err error) {
	fd := int(t.in.Fd())
	width, height, err = term.GetSize(fd)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get terminal size: %w", err)
	}
	return width, height, nil
}

// Read reads up to len(p) bytes from the terminal input.
func (t *Terminal) Read(p []byte) (n int, err error) {
	return t.in.Read(p)
}

// ANSI escape sequences
const (
	// Screen control
	ClearScreen = "\033[2J"   // Clear entire screen
	ClearLine   = "\033[K"    // Clear from cursor to end of line
	CursorHome  = "\033[H"    // Move cursor to home position (1,1)
	CursorHide  = "\033[?25l" // Hide cursor
	CursorShow  = "\033[?25h" // Show cursor

	// Text attributes
	Reset     = "\033[0m"
	Bold      = "\033[1m"
	Dim       = "\033[2m"
	Underline = "\033[4m"
	Blink     = "\033[5m"
	Reverse   = "\033[7m"

	// Foreground colors
	FgBlack   = "\033[30m"
	FgRed     = "\033[31m"
	FgGreen   = "\033[32m"
	FgYellow  = "\033[33m"
	FgBlue    = "\033[34m"
	FgMagenta = "\033[35m"
	FgCyan    = "\033[36m"
	FgWhite   = "\033[37m"

	// Bright foreground colors
	FgBrightBlack   = "\033[90m"
	FgBrightRed     = "\033[91m"
	FgBrightGreen   = "\033[92m"
	FgBrightYellow  = "\033[93m"
	FgBrightBlue    = "\033[94m"
	FgBrightMagenta = "\033[95m"
	FgBrightCyan    = "\033[96m"
	FgBrightWhite   = "\033[97m"

	// Background colors
	BgBlack   = "\033[40m"
	BgRed     = "\033[41m"
	BgGreen   = "\033[42m"
	BgYellow  = "\033[43m"
	BgBlue    = "\033[44m"
	BgMagenta = "\033[45m"
	BgCyan    = "\033[46m"
	BgWhite   = "\033[47m"

	// Bell
	Bell = "\a"
)

// CursorTo returns an ANSI escape sequence to move the cursor to (row, col).
// Row and column are 1-indexed.
func CursorTo(row, col int) string {
	return fmt.Sprintf("\033[%d;%dH", row, col)
}

// CursorUp returns an ANSI escape sequence to move the cursor up n lines.
func CursorUp(n int) string {
	return fmt.Sprintf("\033[%dA", n)
}

// CursorDown returns an ANSI escape sequence to move the cursor down n lines.
func CursorDown(n int) string {
	return fmt.Sprintf("\033[%dB", n)
}

// CursorForward returns an ANSI escape sequence to move the cursor forward n columns.
func CursorForward(n int) string {
	return fmt.Sprintf("\033[%dC", n)
}

// CursorBack returns an ANSI escape sequence to move the cursor back n columns.
func CursorBack(n int) string {
	return fmt.Sprintf("\033[%dD", n)
}

// Write helpers for Terminal

// Clear clears the screen and moves cursor to home.
func (t *Terminal) Clear() {
	fmt.Fprint(t.out, ClearScreen+CursorHome)
}

// HideCursor hides the cursor.
func (t *Terminal) HideCursor() {
	fmt.Fprint(t.out, CursorHide)
}

// ShowCursor shows the cursor.
func (t *Terminal) ShowCursor() {
	fmt.Fprint(t.out, CursorShow)
}

// Bell sounds the terminal bell.
func (t *Terminal) RingBell() {
	fmt.Fprint(t.out, Bell)
}

// Write writes the given string to the terminal output.
func (t *Terminal) Write(s string) {
	fmt.Fprint(t.out, s)
}

// WriteLine writes a string followed by a newline to the terminal output.
func (t *Terminal) WriteLine(s string) {
	fmt.Fprintln(t.out, s)
}

// Writef writes a formatted string to the terminal output.
func (t *Terminal) Writef(format string, args ...any) {
	fmt.Fprintf(t.out, format, args...)
}

// MoveTo moves the cursor to the given position (1-indexed).
func (t *Terminal) MoveTo(row, col int) {
	fmt.Fprint(t.out, CursorTo(row, col))
}

// ClearLine clears from the cursor to the end of the current line.
func (t *Terminal) ClearLine() {
	fmt.Fprint(t.out, ClearLine)
}
