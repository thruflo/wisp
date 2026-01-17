package testutil

import "github.com/thruflo/wisp/internal/state"

// SampleRFC provides a minimal RFC for testing task generation.
const SampleRFC = `# Sample RFC: Test Feature

## Overview

Implement a simple feature with setup and verification.

## Requirements

1. Create a configuration file
2. Implement the main logic
3. Add unit tests
`

// SampleUpdatedRFC provides an updated RFC for testing the update command.
const SampleUpdatedRFC = `# Sample RFC: Test Feature (Updated)

## Overview

Implement a simple feature with setup and verification.

## Requirements

1. Create a configuration file
2. Implement the main logic with caching
3. Add unit tests
4. Add integration tests
`

// SampleFeedback provides PR feedback for testing the review command.
const SampleFeedback = `# PR Review Feedback

## Issues

1. Missing error handling in the main function
2. Configuration validation is too lenient
3. Test coverage should be at least 80%
`

// SampleTasks returns a slice of sample tasks for testing.
// Returns a new slice each time to prevent test interference.
func SampleTasks() []state.Task {
	return []state.Task{
		{
			Category:    state.CategorySetup,
			Description: "Create configuration file",
			Steps:       []string{"Create config directory", "Write default config"},
			Passes:      false,
		},
		{
			Category:    state.CategoryFeature,
			Description: "Implement main logic",
			Steps:       []string{"Implement core function", "Add error handling"},
			Passes:      false,
		},
		{
			Category:    state.CategoryTest,
			Description: "Add unit tests",
			Steps:       []string{"Write test cases", "Verify coverage"},
			Passes:      false,
		},
	}
}

// SampleTasksPartiallyComplete returns tasks with the first one completed.
func SampleTasksPartiallyComplete() []state.Task {
	tasks := SampleTasks()
	tasks[0].Passes = true
	return tasks
}

// SampleTasksAllComplete returns tasks with all marked as complete.
func SampleTasksAllComplete() []state.Task {
	tasks := SampleTasks()
	for i := range tasks {
		tasks[i].Passes = true
	}
	return tasks
}

// SampleStateContinue returns a state with CONTINUE status.
func SampleStateContinue() *state.State {
	return &state.State{
		Status:  state.StatusContinue,
		Summary: "Working on feature implementation",
	}
}

// SampleStateDone returns a state with DONE status and verification.
func SampleStateDone() *state.State {
	return &state.State{
		Status:  state.StatusDone,
		Summary: "All tasks completed successfully",
		Verification: &state.Verification{
			Method:  state.VerificationTests,
			Passed:  true,
			Details: "All tests passed",
		},
	}
}

// SampleStateNeedsInput returns a state with NEEDS_INPUT status.
func SampleStateNeedsInput() *state.State {
	return &state.State{
		Status:   state.StatusNeedsInput,
		Summary:  "Need clarification",
		Question: "Should we use approach A or B?",
	}
}

// SampleStateBlocked returns a state with BLOCKED status.
func SampleStateBlocked() *state.State {
	return &state.State{
		Status:  state.StatusBlocked,
		Summary: "Cannot proceed",
		Error:   "Missing required dependency",
	}
}

// SampleHistory returns a slice of history entries showing progress.
// Returns a new slice each time to prevent test interference.
func SampleHistory() []state.History {
	return []state.History{
		{
			Iteration:      1,
			Summary:        "Initial setup completed",
			TasksCompleted: 1,
			Status:         state.StatusContinue,
		},
		{
			Iteration:      2,
			Summary:        "Implemented main feature",
			TasksCompleted: 2,
			Status:         state.StatusContinue,
		},
		{
			Iteration:      3,
			Summary:        "All tasks complete",
			TasksCompleted: 3,
			Status:         state.StatusDone,
		},
	}
}

// SampleHistoryStuck returns history entries showing no progress (stuck).
func SampleHistoryStuck() []state.History {
	return []state.History{
		{
			Iteration:      1,
			Summary:        "Attempted task",
			TasksCompleted: 0,
			Status:         state.StatusContinue,
		},
		{
			Iteration:      2,
			Summary:        "Still working",
			TasksCompleted: 0,
			Status:         state.StatusContinue,
		},
		{
			Iteration:      3,
			Summary:        "No progress",
			TasksCompleted: 0,
			Status:         state.StatusContinue,
		},
	}
}

// SampleHistoryWithProgress returns history showing recent progress.
func SampleHistoryWithProgress() []state.History {
	return []state.History{
		{
			Iteration:      1,
			Summary:        "First attempt",
			TasksCompleted: 0,
			Status:         state.StatusContinue,
		},
		{
			Iteration:      2,
			Summary:        "Still trying",
			TasksCompleted: 0,
			Status:         state.StatusContinue,
		},
		{
			Iteration:      3,
			Summary:        "Made progress",
			TasksCompleted: 1,
			Status:         state.StatusContinue,
		},
	}
}
