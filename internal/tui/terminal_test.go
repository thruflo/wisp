package tui

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestANSIEscapeConstants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		constant string
		want     string
	}{
		{"ClearScreen", ClearScreen, "\033[2J"},
		{"ClearLine", ClearLine, "\033[K"},
		{"CursorHome", CursorHome, "\033[H"},
		{"CursorHide", CursorHide, "\033[?25l"},
		{"CursorShow", CursorShow, "\033[?25h"},
		{"Reset", Reset, "\033[0m"},
		{"Bold", Bold, "\033[1m"},
		{"Dim", Dim, "\033[2m"},
		{"Underline", Underline, "\033[4m"},
		{"FgRed", FgRed, "\033[31m"},
		{"FgGreen", FgGreen, "\033[32m"},
		{"FgYellow", FgYellow, "\033[33m"},
		{"FgBlue", FgBlue, "\033[34m"},
		{"FgCyan", FgCyan, "\033[36m"},
		{"FgBrightRed", FgBrightRed, "\033[91m"},
		{"FgBrightGreen", FgBrightGreen, "\033[92m"},
		{"BgRed", BgRed, "\033[41m"},
		{"BgGreen", BgGreen, "\033[42m"},
		{"Bell", Bell, "\a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.constant)
		})
	}
}

func TestCursorTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		row, col int
		want     string
	}{
		{"origin", 1, 1, "\033[1;1H"},
		{"row 5 col 10", 5, 10, "\033[5;10H"},
		{"large values", 100, 200, "\033[100;200H"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, CursorTo(tt.row, tt.col))
		})
	}
}

func TestCursorUp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		n    int
		want string
	}{
		{"one line", 1, "\033[1A"},
		{"five lines", 5, "\033[5A"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, CursorUp(tt.n))
		})
	}
}

func TestCursorDown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		n    int
		want string
	}{
		{"one line", 1, "\033[1B"},
		{"three lines", 3, "\033[3B"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, CursorDown(tt.n))
		})
	}
}

func TestCursorForward(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		n    int
		want string
	}{
		{"one column", 1, "\033[1C"},
		{"ten columns", 10, "\033[10C"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, CursorForward(tt.n))
		})
	}
}

func TestCursorBack(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		n    int
		want string
	}{
		{"one column", 1, "\033[1D"},
		{"seven columns", 7, "\033[7D"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, CursorBack(tt.n))
		})
	}
}

func TestTerminalWrite(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	term := NewTerminal(&buf)

	term.Write("hello")
	assert.Equal(t, "hello", buf.String())
}

func TestTerminalWriteLine(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	term := NewTerminal(&buf)

	term.WriteLine("hello")
	assert.Equal(t, "hello\n", buf.String())
}

func TestTerminalWritef(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	term := NewTerminal(&buf)

	term.Writef("count: %d", 42)
	assert.Equal(t, "count: 42", buf.String())
}

func TestTerminalClear(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	term := NewTerminal(&buf)

	term.Clear()
	assert.Equal(t, ClearScreen+CursorHome, buf.String())
}

func TestTerminalHideCursor(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	term := NewTerminal(&buf)

	term.HideCursor()
	assert.Equal(t, CursorHide, buf.String())
}

func TestTerminalShowCursor(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	term := NewTerminal(&buf)

	term.ShowCursor()
	assert.Equal(t, CursorShow, buf.String())
}

func TestTerminalRingBell(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	term := NewTerminal(&buf)

	term.RingBell()
	assert.Equal(t, Bell, buf.String())
}

func TestTerminalMoveTo(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	term := NewTerminal(&buf)

	term.MoveTo(3, 5)
	assert.Equal(t, "\033[3;5H", buf.String())
}

func TestTerminalClearLine(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	term := NewTerminal(&buf)

	term.ClearLine()
	assert.Equal(t, ClearLine, buf.String())
}

func TestTerminalIsRaw(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	term := NewTerminal(&buf)

	assert.False(t, term.IsRaw())
}
