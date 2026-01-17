package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thruflo/wisp/internal/state"
)

func TestSampleTasks(t *testing.T) {
	t.Parallel()

	tasks := SampleTasks()
	require.Len(t, tasks, 3)

	// Verify each call returns a new slice (no interference between tests)
	tasks[0].Passes = true
	tasks2 := SampleTasks()
	assert.False(t, tasks2[0].Passes, "SampleTasks should return fresh slice")
}

func TestSampleTasksPartiallyComplete(t *testing.T) {
	t.Parallel()

	tasks := SampleTasksPartiallyComplete()
	require.Len(t, tasks, 3)
	assert.True(t, tasks[0].Passes)
	assert.False(t, tasks[1].Passes)
	assert.False(t, tasks[2].Passes)
}

func TestSampleTasksAllComplete(t *testing.T) {
	t.Parallel()

	tasks := SampleTasksAllComplete()
	require.Len(t, tasks, 3)
	for i, task := range tasks {
		assert.True(t, task.Passes, "task %d should be complete", i)
	}
}

func TestSampleStates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		fn             func() *state.State
		expectedStatus string
	}{
		{"continue", SampleStateContinue, state.StatusContinue},
		{"done", SampleStateDone, state.StatusDone},
		{"needs_input", SampleStateNeedsInput, state.StatusNeedsInput},
		{"blocked", SampleStateBlocked, state.StatusBlocked},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := tt.fn()
			assert.Equal(t, tt.expectedStatus, st.Status)
		})
	}
}

func TestSampleHistory(t *testing.T) {
	t.Parallel()

	history := SampleHistory()
	require.Len(t, history, 3)

	// Verify progress
	assert.Equal(t, 1, history[0].TasksCompleted)
	assert.Equal(t, 2, history[1].TasksCompleted)
	assert.Equal(t, 3, history[2].TasksCompleted)
}

func TestSampleHistoryStuck(t *testing.T) {
	t.Parallel()

	history := SampleHistoryStuck()
	require.Len(t, history, 3)

	for _, h := range history {
		assert.Equal(t, 0, h.TasksCompleted, "stuck history should have no progress")
	}
}

func TestSetupTestDir(t *testing.T) {
	t.Parallel()

	tmpDir, store := SetupTestDir(t)

	// Verify directory structure
	assert.DirExists(t, filepath.Join(tmpDir, ".wisp"))
	assert.DirExists(t, filepath.Join(tmpDir, ".wisp", "sessions"))
	assert.DirExists(t, filepath.Join(tmpDir, ".wisp", "templates", "default"))

	// Verify files exist
	assert.FileExists(t, filepath.Join(tmpDir, ".wisp", "config.yaml"))
	assert.FileExists(t, filepath.Join(tmpDir, ".wisp", "settings.json"))
	assert.FileExists(t, filepath.Join(tmpDir, ".wisp", ".sprite.env"))

	// Verify templates exist
	templateDir := filepath.Join(tmpDir, ".wisp", "templates", "default")
	assert.FileExists(t, filepath.Join(templateDir, "context.md"))
	assert.FileExists(t, filepath.Join(templateDir, "create-tasks.md"))
	assert.FileExists(t, filepath.Join(templateDir, "iterate.md"))

	// Verify store is usable
	assert.NotNil(t, store)
}

func TestMustMarshalJSON(t *testing.T) {
	t.Parallel()

	data := MustMarshalJSON(t, map[string]string{"key": "value"})
	assert.Contains(t, string(data), "key")
	assert.Contains(t, string(data), "value")
}

func TestWriteTestFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	content := []byte("test content")

	WriteTestFile(t, tmpDir, "subdir/file.txt", content)

	// Verify file exists and has correct content
	readContent, err := os.ReadFile(filepath.Join(tmpDir, "subdir", "file.txt"))
	require.NoError(t, err)
	assert.Equal(t, content, readContent)
}

func TestAssertTasksEqual(t *testing.T) {
	t.Parallel()

	expected := SampleTasks()
	actual := SampleTasks()

	// This should not panic/fail
	AssertTasksEqual(t, expected, actual)
}

func TestAssertStateStatus(t *testing.T) {
	t.Parallel()

	st := SampleStateContinue()
	AssertStateStatus(t, st, state.StatusContinue)
	AssertStateContinue(t, st)
}

func TestAssertStateHasQuestion(t *testing.T) {
	t.Parallel()

	st := SampleStateNeedsInput()
	AssertStateHasQuestion(t, st)
}

func TestAssertStateHasError(t *testing.T) {
	t.Parallel()

	st := SampleStateBlocked()
	AssertStateHasError(t, st)
}

func TestAssertVerificationPassed(t *testing.T) {
	t.Parallel()

	st := SampleStateDone()
	AssertVerificationPassed(t, st)
}

func TestAssertTasksProgress(t *testing.T) {
	t.Parallel()

	tasks := SampleTasksPartiallyComplete()
	AssertTasksProgress(t, tasks, 1, 3)
}

func TestAssertHistoryLength(t *testing.T) {
	t.Parallel()

	history := SampleHistory()
	AssertHistoryLength(t, history, 3)
}

func TestAssertHistoryProgress(t *testing.T) {
	t.Parallel()

	history := SampleHistory()
	AssertHistoryProgress(t, history, 3)
}
