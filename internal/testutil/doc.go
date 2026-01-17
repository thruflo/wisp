// Package testutil provides shared test utilities for wisp.
//
// This package consolidates common test helpers, fixtures, and assertions
// used across the wisp codebase to reduce duplication and ensure consistent
// test patterns.
//
// # Fixtures
//
// The fixtures.go file provides sample data for testing:
//
//   - SampleRFC, SampleUpdatedRFC, SampleFeedback - string constants for RFC content
//   - SampleTasks() - returns a slice of sample tasks
//   - SampleTasksPartiallyComplete() - tasks with first one completed
//   - SampleTasksAllComplete() - all tasks completed
//   - SampleStateContinue(), SampleStateDone(), etc. - various state configurations
//   - SampleHistory(), SampleHistoryStuck() - history entries for testing
//
// # Environment Helpers
//
// The env.go file provides test environment setup:
//
//   - SetupTestDir(t) - creates a temp directory with .wisp structure
//   - LoadTestCredentials(t) - loads credentials, skips if unavailable
//   - TryLoadTestCredentials() - loads credentials, returns nil if unavailable
//   - FindProjectRoot(t) - finds the project root directory
//   - MustMarshalJSON(t, v) - marshals to JSON or fails test
//   - MustUnmarshalJSON(t, data, v) - unmarshals JSON or fails test
//   - WriteTestFile(t, base, path, content) - writes a file in test dir
//
// # Assertions
//
// The assertions.go file provides custom test assertions:
//
//   - AssertTasksEqual(t, expected, actual) - compares task slices
//   - AssertStateStatus(t, st, status) - checks state status
//   - AssertStateContinue(t, st), AssertStateDone(t, st), etc. - status shortcuts
//   - AssertStateHasQuestion(t, st), AssertStateHasError(t, st) - field presence
//   - AssertStateHasVerification(t, st), AssertVerificationPassed(t, st)
//   - AssertTasksProgress(t, tasks, completed, total) - task progress
//   - AssertHistoryLength(t, history, expected) - history length
//   - AssertHistoryProgress(t, history, completed) - last entry progress
//
// # Usage
//
// Import the package in your test files:
//
//	import "github.com/thruflo/wisp/internal/testutil"
//
// Then use the helpers:
//
//	func TestSomething(t *testing.T) {
//	    tmpDir, store := testutil.SetupTestDir(t)
//	    tasks := testutil.SampleTasks()
//	    // ... run test ...
//	    testutil.AssertTasksProgress(t, tasks, 0, 3)
//	}
package testutil
