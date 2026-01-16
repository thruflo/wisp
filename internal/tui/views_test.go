package tui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummaryView_Render(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		state      ViewState
		width      int
		wantBranch string
		wantStatus string
		wantTasks  string
	}{
		{
			name: "basic render",
			state: ViewState{
				Branch:         "feat-auth",
				Iteration:      5,
				MaxIterations:  50,
				Status:         "CONTINUE",
				CompletedTasks: 3,
				TotalTasks:     7,
				LastSummary:    "Implemented JWT validation",
			},
			width:      60,
			wantBranch: "feat-auth",
			wantStatus: "CONTINUE",
			wantTasks:  "3/7",
		},
		{
			name: "blocked status with error",
			state: ViewState{
				Branch:         "fix-bug",
				Iteration:      10,
				MaxIterations:  50,
				Status:         "BLOCKED",
				CompletedTasks: 2,
				TotalTasks:     5,
				LastSummary:    "Stuck on test failure",
				Error:          "Tests failing: expected 3, got 4",
			},
			width:      60,
			wantBranch: "fix-bug",
			wantStatus: "BLOCKED",
			wantTasks:  "2/5",
		},
		{
			name: "needs input",
			state: ViewState{
				Branch:         "add-feature",
				Iteration:      3,
				MaxIterations:  20,
				Status:         "NEEDS_INPUT",
				CompletedTasks: 1,
				TotalTasks:     4,
				LastSummary:    "Need clarification on API",
				Question:       "Which authentication method?",
			},
			width:      60,
			wantBranch: "add-feature",
			wantStatus: "NEEDS_INPUT",
			wantTasks:  "1/4",
		},
		{
			name: "narrow width",
			state: ViewState{
				Branch:         "test",
				Iteration:      1,
				MaxIterations:  10,
				Status:         "CONTINUE",
				CompletedTasks: 0,
				TotalTasks:     1,
			},
			width:      50,
			wantBranch: "test",
			wantStatus: "CONTINUE",
			wantTasks:  "0/1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			v := &SummaryView{}
			lines := v.Render(tt.state, tt.width)

			require.NotEmpty(t, lines, "render should produce output")

			// Join lines for searching
			output := strings.Join(lines, "\n")

			// Check expected content is present
			assert.Contains(t, output, tt.wantBranch, "should contain branch name")
			assert.Contains(t, output, tt.wantTasks, "should contain task count")

			// Check shortcuts are present
			assert.Contains(t, output, "[t]ail", "should show tail shortcut")
			assert.Contains(t, output, "[a]ttach", "should show attach shortcut")
			assert.Contains(t, output, "[k]ill", "should show kill shortcut")
			assert.Contains(t, output, "[esc]", "should show escape shortcut")

			// Check box structure
			assert.True(t, strings.HasPrefix(lines[0], BoxTopLeft), "should start with box top-left")
			assert.True(t, strings.HasSuffix(lines[0], BoxTopRight), "first line should end with box top-right")
			assert.True(t, strings.HasPrefix(lines[len(lines)-1], BoxBottomLeft), "should end with box bottom-left")
		})
	}
}

func TestSummaryView_Render_EmptySummary(t *testing.T) {
	t.Parallel()

	v := &SummaryView{}
	state := ViewState{
		Branch:         "test",
		Iteration:      1,
		MaxIterations:  10,
		Status:         "CONTINUE",
		CompletedTasks: 0,
		TotalTasks:     1,
		LastSummary:    "",
	}

	lines := v.Render(state, 60)
	output := strings.Join(lines, "\n")

	// Should not contain "last:" when summary is empty
	assert.NotContains(t, output, "last:", "should not show last summary when empty")
}

func TestTailView_Append(t *testing.T) {
	t.Parallel()

	tv := NewTailView(100)

	tv.Append("line 1")
	tv.Append("line 2")
	tv.Append("line 3")

	lines := tv.Lines()
	require.Len(t, lines, 3)
	assert.Equal(t, "line 1", lines[0])
	assert.Equal(t, "line 2", lines[1])
	assert.Equal(t, "line 3", lines[2])
}

func TestTailView_AppendLines(t *testing.T) {
	t.Parallel()

	tv := NewTailView(100)

	tv.AppendLines([]string{"line 1", "line 2", "line 3"})

	lines := tv.Lines()
	require.Len(t, lines, 3)
	assert.Equal(t, "line 1", lines[0])
	assert.Equal(t, "line 2", lines[1])
	assert.Equal(t, "line 3", lines[2])
}

func TestTailView_MaxLines(t *testing.T) {
	t.Parallel()

	tv := NewTailView(5)

	// Add more than max lines
	for i := 0; i < 10; i++ {
		tv.Append(strings.Repeat("x", i+1))
	}

	lines := tv.Lines()
	require.Len(t, lines, 5, "should trim to max lines")

	// Should contain the last 5 lines (6-10 x's)
	assert.Equal(t, strings.Repeat("x", 6), lines[0])
	assert.Equal(t, strings.Repeat("x", 10), lines[4])
}

func TestTailView_Clear(t *testing.T) {
	t.Parallel()

	tv := NewTailView(100)
	tv.Append("line 1")
	tv.Append("line 2")

	tv.Clear()

	assert.Empty(t, tv.Lines())
}

func TestTailView_Render(t *testing.T) {
	t.Parallel()

	tv := NewTailView(100)
	tv.Append("line 1")
	tv.Append("line 2")
	tv.Append("line 3")

	lines := tv.Render(60, 10)

	require.NotEmpty(t, lines)

	// Check header
	assert.Contains(t, lines[0], "Output")
	assert.Contains(t, lines[0], "'t'")

	// Check footer
	lastLine := lines[len(lines)-1]
	assert.Contains(t, lastLine, "3 lines")

	// Check content lines are present
	output := strings.Join(lines, "\n")
	assert.Contains(t, output, "line 1")
	assert.Contains(t, output, "line 2")
	assert.Contains(t, output, "line 3")
}

func TestTailView_Render_Empty(t *testing.T) {
	t.Parallel()

	tv := NewTailView(100)
	lines := tv.Render(60, 10)

	require.NotEmpty(t, lines)
	// Should show 0 lines in footer
	lastLine := lines[len(lines)-1]
	assert.Contains(t, lastLine, "0 lines")
}

func TestTailView_Render_MoreThanHeight(t *testing.T) {
	t.Parallel()

	tv := NewTailView(100)
	for i := 0; i < 20; i++ {
		tv.Append(strings.Repeat("x", i+1))
	}

	// Render with small height
	lines := tv.Render(60, 5)

	// Should only show last few lines plus header/footer
	output := strings.Join(lines, "\n")

	// Should show the most recent lines
	assert.Contains(t, output, strings.Repeat("x", 20))
	assert.Contains(t, output, strings.Repeat("x", 19))
}

func TestInputView_Render(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	terminal := NewTerminal(buf)
	iv := NewInputView(terminal)

	question := "Which database should we use for this feature?"
	lines := iv.Render(question, 60)

	require.NotEmpty(t, lines)

	output := strings.Join(lines, "\n")

	// Check header
	assert.Contains(t, output, "Input Required")

	// Check question is displayed
	assert.Contains(t, output, "database")

	// Check input prompt
	assert.Contains(t, output, "> ")

	// Check instructions
	assert.Contains(t, output, "Enter to submit")
	assert.Contains(t, output, "Esc to cancel")
}

func TestInputView_Editor(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	terminal := NewTerminal(buf)
	iv := NewInputView(terminal)

	editor := iv.Editor()
	require.NotNil(t, editor)

	// Type some text
	editor.HandleKey(KeyEvent{Key: KeyRune, Rune: 'H'})
	editor.HandleKey(KeyEvent{Key: KeyRune, Rune: 'i'})

	assert.Equal(t, "Hi", editor.Text())

	// Render should show the text
	lines := iv.Render("Question?", 60)
	output := strings.Join(lines, "\n")
	assert.Contains(t, output, "Hi")
}

func TestInputView_Reset(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	terminal := NewTerminal(buf)
	iv := NewInputView(terminal)

	// Type some text
	iv.Editor().HandleKey(KeyEvent{Key: KeyRune, Rune: 'X'})
	assert.Equal(t, "X", iv.Editor().Text())

	// Reset
	iv.Reset()
	assert.Equal(t, "", iv.Editor().Text())
}

func TestViewState_ZeroValue(t *testing.T) {
	t.Parallel()

	// Zero-value ViewState should render without panics
	v := &SummaryView{}
	lines := v.Render(ViewState{}, 60)

	require.NotEmpty(t, lines)

	output := strings.Join(lines, "\n")
	// Should show 0/0 iterations and 0/0 tasks
	assert.Contains(t, output, "iteration 0/0")
	assert.Contains(t, output, "0/0 complete")
}

func TestMax(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 5, max(5, 3))
	assert.Equal(t, 5, max(3, 5))
	assert.Equal(t, 0, max(0, 0))
	assert.Equal(t, -1, max(-1, -5))
	assert.Equal(t, 0, max(0, -5))
}
