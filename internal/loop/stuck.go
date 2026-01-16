package loop

import "github.com/thruflo/wisp/internal/state"

// DetectStuck checks if the loop is stuck by analyzing history.
// A loop is considered stuck if tasks_completed hasn't increased
// for the last N iterations where N is the threshold.
func DetectStuck(history []state.History, threshold int) bool {
	if threshold <= 0 || len(history) < threshold {
		return false
	}

	// Get the last N entries
	recent := history[len(history)-threshold:]

	// Check if tasks_completed is the same across all recent entries
	firstCompleted := recent[0].TasksCompleted
	for _, entry := range recent[1:] {
		if entry.TasksCompleted != firstCompleted {
			// Progress was made at some point
			return false
		}
	}

	// No progress in the last N iterations
	return true
}

// CalculateProgress returns the number of tasks completed and total tasks.
func CalculateProgress(tasks []state.Task) (completed, total int) {
	total = len(tasks)
	for _, t := range tasks {
		if t.Passes {
			completed++
		}
	}
	return completed, total
}

// ProgressRate calculates the completion rate over recent history.
// Returns tasks completed per iteration (averaged over the window).
func ProgressRate(history []state.History, window int) float64 {
	if len(history) < 2 {
		return 0
	}

	if window > len(history) {
		window = len(history)
	}

	recent := history[len(history)-window:]
	if len(recent) < 2 {
		return 0
	}

	startCompleted := recent[0].TasksCompleted
	endCompleted := recent[len(recent)-1].TasksCompleted
	iterations := len(recent) - 1

	if iterations == 0 {
		return 0
	}

	return float64(endCompleted-startCompleted) / float64(iterations)
}
