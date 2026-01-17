package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBox(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		width, height int
		want          []string
	}{
		{
			name:   "3x3 box",
			width:  3,
			height: 3,
			want: []string{
				"┌─┐",
				"│ │",
				"└─┘",
			},
		},
		{
			name:   "5x4 box",
			width:  5,
			height: 4,
			want: []string{
				"┌───┐",
				"│   │",
				"│   │",
				"└───┘",
			},
		},
		{
			name:   "too small width",
			width:  1,
			height: 3,
			want:   nil,
		},
		{
			name:   "too small height",
			width:  3,
			height: 1,
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Box(tt.width, tt.height)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBoxWithContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		width   int
		content []string
		want    []string
	}{
		{
			name:    "single line",
			width:   20,
			content: []string{"Hello"},
			want: []string{
				"┌──────────────────┐",
				"│ Hello            │",
				"└──────────────────┘",
			},
		},
		{
			name:    "multiple lines",
			width:   20,
			content: []string{"Line 1", "Line 2"},
			want: []string{
				"┌──────────────────┐",
				"│ Line 1           │",
				"│ Line 2           │",
				"└──────────────────┘",
			},
		},
		{
			name:    "long line truncated",
			width:   15,
			content: []string{"This is a very long line"},
			want: []string{
				"┌─────────────┐",
				"│ This is ... │",
				"└─────────────┘",
			},
		},
		{
			name:    "width too small",
			width:   3,
			content: []string{"Hi"},
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := BoxWithContent(tt.width, tt.content)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPadOrTruncate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		width int
		want  string
	}{
		{"exact length", "hello", 5, "hello"},
		{"needs padding", "hi", 5, "hi   "},
		{"needs truncation", "hello world", 8, "hello..."},
		{"very short truncation", "hello", 2, "he"},
		{"zero width", "hello", 0, ""},
		{"negative width", "hello", -1, ""},
		{"empty string", "", 5, "     "},
		{"unicode exact", "日本語", 3, "日本語"},
		{"unicode padded", "日本", 5, "日本   "},
		{"unicode truncated", "日本語文字漢字", 6, "日本語..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, PadOrTruncate(tt.input, tt.width))
		})
	}
}

func TestTruncate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		width int
		want  string
	}{
		{"no truncation needed", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"needs truncation", "hello world", 8, "hello..."},
		{"short truncation", "hello", 3, "..."},
		{"very short", "hello", 2, "he"},
		{"zero width", "hello", 0, ""},
		{"unicode", "日本語文字漢字", 6, "日本語..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, Truncate(tt.input, tt.width))
		})
	}
}

func TestWrapText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		text  string
		width int
		want  []string
	}{
		{
			name:  "no wrap needed",
			text:  "hello world",
			width: 20,
			want:  []string{"hello world"},
		},
		{
			name:  "simple wrap",
			text:  "hello world foo bar",
			width: 10,
			want:  []string{"hello", "world foo", "bar"},
		},
		{
			name:  "single long word",
			text:  "hello",
			width: 10,
			want:  []string{"hello"},
		},
		{
			name:  "empty text",
			text:  "",
			width: 10,
			want:  nil,
		},
		{
			name:  "zero width",
			text:  "hello",
			width: 0,
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, WrapText(tt.text, tt.width))
		})
	}
}

func TestCenterText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		width int
		want  string
	}{
		{"centered", "hi", 6, "  hi  "},
		{"odd spacing", "hi", 5, " hi  "},
		{"exact width", "hello", 5, "hello"},
		{"too long", "hello world", 5, "he..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, CenterText(tt.input, tt.width))
		})
	}
}

func TestRightAlign(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		width int
		want  string
	}{
		{"right aligned", "hi", 5, "   hi"},
		{"exact width", "hello", 5, "hello"},
		{"too long", "hello world", 5, "he..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, RightAlign(tt.input, tt.width))
		})
	}
}

func TestProgressBar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		current      int
		total        int
		width        int
		wantContains string
	}{
		{"50 percent", 5, 10, 20, "50%"},
		{"100 percent", 10, 10, 20, "100%"},
		{"zero percent", 0, 10, 20, "0%"},
		{"over 100", 15, 10, 20, "100%"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ProgressBar(tt.current, tt.total, tt.width)
			assert.Contains(t, got, tt.wantContains)
			assert.Contains(t, got, "[")
			assert.Contains(t, got, "]")
		})
	}
}

func TestProgressBar_EdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		current int
		total   int
		width   int
		want    string
	}{
		{"zero total", 5, 0, 20, ""},
		{"width too small", 5, 10, 5, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ProgressBar(tt.current, tt.total, tt.width))
		})
	}
}

func TestStyle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		text  string
		codes []string
		want  string
	}{
		{"bold", "hello", []string{Bold}, Bold + "hello" + Reset},
		{"red bold", "hello", []string{FgRed, Bold}, FgRed + Bold + "hello" + Reset},
		{"no codes", "hello", nil, "hello"},
		{"empty codes", "hello", []string{}, "hello"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, Style(tt.text, tt.codes...))
		})
	}
}

func TestStatusColor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status string
		want   string
	}{
		{"running", "RUNNING", FgGreen},
		{"continue", "CONTINUE", FgGreen},
		{"done", "DONE", FgBrightGreen},
		{"complete", "COMPLETE", FgBrightGreen},
		{"completed", "COMPLETED", FgBrightGreen},
		{"blocked", "BLOCKED", FgRed},
		{"error", "ERROR", FgRed},
		{"failed", "FAILED", FgRed},
		{"needs_input", "NEEDS_INPUT", FgYellow},
		{"waiting", "WAITING", FgYellow},
		{"stopped", "STOPPED", FgBrightBlack},
		{"lowercase", "running", FgGreen},
		{"mixed case", "Running", FgGreen},
		{"unknown", "SOMETHING", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, StatusColor(tt.status))
		})
	}
}

func TestFormatStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status string
		want   string
	}{
		{"running", "RUNNING", FgGreen + Bold + "RUNNING" + Reset},
		{"done", "DONE", FgBrightGreen + Bold + "DONE" + Reset},
		{"unknown", "SOMETHING", "SOMETHING"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, FormatStatus(tt.status))
		})
	}
}

func TestVisualWidth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"plain text", "hello", 5},
		{"with bold", Bold + "hello" + Reset, 5},
		{"with color", FgGreen + "hello" + Reset, 5},
		{"with color and bold", FgGreen + Bold + "RUNNING" + Reset, 7},
		{"unicode", "日本語", 3},
		{"empty", "", 0},
		{"only ansi", FgGreen + Reset, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, VisualWidth(tt.input))
		})
	}
}

func TestStripAnsi(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain text", "hello", "hello"},
		{"with bold", Bold + "hello" + Reset, "hello"},
		{"with color", FgGreen + "hello" + Reset, "hello"},
		{"multiple codes", FgGreen + Bold + "test" + Reset, "test"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, StripAnsi(tt.input))
		})
	}
}

func TestPadOrTruncateWithAnsi(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		width int
		want  string
	}{
		{
			name:  "ansi needs padding",
			input: FgGreen + "hi" + Reset,
			width: 5,
			want:  FgGreen + "hi" + Reset + "   ",
		},
		{
			name:  "ansi exact width",
			input: FgGreen + "hello" + Reset,
			width: 5,
			want:  FgGreen + "hello" + Reset,
		},
		{
			name:  "ansi needs truncation",
			input: FgGreen + "hello world" + Reset,
			width: 8,
			want:  FgGreen + "hello" + Reset + "...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := PadOrTruncate(tt.input, tt.width)
			assert.Equal(t, tt.want, got)
			// Also verify the visual width is correct
			assert.Equal(t, tt.width, VisualWidth(got))
		})
	}
}
