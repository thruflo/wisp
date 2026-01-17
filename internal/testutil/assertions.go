package testutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thruflo/wisp/internal/state"
)

// AssertTasksEqual asserts that two task slices are equal.
// Compares all fields: Category, Description, Steps, and Passes.
func AssertTasksEqual(t *testing.T, expected, actual []state.Task) {
	t.Helper()

	require.Len(t, actual, len(expected), "task count mismatch")

	for i := range expected {
		assert.Equal(t, expected[i].Category, actual[i].Category,
			"task[%d].Category mismatch", i)
		assert.Equal(t, expected[i].Description, actual[i].Description,
			"task[%d].Description mismatch", i)
		assert.Equal(t, expected[i].Steps, actual[i].Steps,
			"task[%d].Steps mismatch", i)
		assert.Equal(t, expected[i].Passes, actual[i].Passes,
			"task[%d].Passes mismatch", i)
	}
}

// AssertStateStatus asserts that a state has the expected status.
func AssertStateStatus(t *testing.T, st *state.State, expectedStatus string) {
	t.Helper()
	require.NotNil(t, st, "state is nil")
	assert.Equal(t, expectedStatus, st.Status, "state status mismatch")
}

// AssertStateContinue asserts that a state has CONTINUE status.
func AssertStateContinue(t *testing.T, st *state.State) {
	t.Helper()
	AssertStateStatus(t, st, state.StatusContinue)
}

// AssertStateDone asserts that a state has DONE status.
func AssertStateDone(t *testing.T, st *state.State) {
	t.Helper()
	AssertStateStatus(t, st, state.StatusDone)
}

// AssertStateNeedsInput asserts that a state has NEEDS_INPUT status.
func AssertStateNeedsInput(t *testing.T, st *state.State) {
	t.Helper()
	AssertStateStatus(t, st, state.StatusNeedsInput)
}

// AssertStateBlocked asserts that a state has BLOCKED status.
func AssertStateBlocked(t *testing.T, st *state.State) {
	t.Helper()
	AssertStateStatus(t, st, state.StatusBlocked)
}

// AssertStateHasQuestion asserts that a state has a non-empty question.
func AssertStateHasQuestion(t *testing.T, st *state.State) {
	t.Helper()
	require.NotNil(t, st, "state is nil")
	assert.NotEmpty(t, st.Question, "state should have a question")
}

// AssertStateHasError asserts that a state has a non-empty error.
func AssertStateHasError(t *testing.T, st *state.State) {
	t.Helper()
	require.NotNil(t, st, "state is nil")
	assert.NotEmpty(t, st.Error, "state should have an error")
}

// AssertStateHasVerification asserts that a state has verification info.
func AssertStateHasVerification(t *testing.T, st *state.State) {
	t.Helper()
	require.NotNil(t, st, "state is nil")
	require.NotNil(t, st.Verification, "state should have verification")
}

// AssertVerificationPassed asserts that verification passed.
func AssertVerificationPassed(t *testing.T, st *state.State) {
	t.Helper()
	AssertStateHasVerification(t, st)
	assert.True(t, st.Verification.Passed, "verification should have passed")
}

// AssertTasksProgress asserts the completed and total task counts.
func AssertTasksProgress(t *testing.T, tasks []state.Task, expectedCompleted, expectedTotal int) {
	t.Helper()

	total := len(tasks)
	completed := 0
	for _, task := range tasks {
		if task.Passes {
			completed++
		}
	}

	assert.Equal(t, expectedTotal, total, "total tasks mismatch")
	assert.Equal(t, expectedCompleted, completed, "completed tasks mismatch")
}

// AssertHistoryLength asserts the history has the expected length.
func AssertHistoryLength(t *testing.T, history []state.History, expected int) {
	t.Helper()
	assert.Len(t, history, expected, "history length mismatch")
}

// AssertHistoryProgress asserts the tasks completed in the last history entry.
func AssertHistoryProgress(t *testing.T, history []state.History, expectedCompleted int) {
	t.Helper()
	require.NotEmpty(t, history, "history is empty")
	last := history[len(history)-1]
	assert.Equal(t, expectedCompleted, last.TasksCompleted,
		"tasks completed in last history entry mismatch")
}
