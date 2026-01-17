//go:build integration

// main_test.go provides global test setup and cleanup for integration tests.
//
// This file implements TestMain to ensure that all test sprites created during
// integration tests are cleaned up after test runs, even if individual test
// cleanup fails.
package integration

import (
	"fmt"
	"os"
	"testing"

	"github.com/thruflo/wisp/internal/testutil"
)

// TestMain runs all integration tests and performs global cleanup afterward.
// This ensures that any sprites registered during tests are deleted, even if
// individual test cleanups fail.
func TestMain(m *testing.M) {
	// Run all tests
	code := m.Run()

	// Global cleanup after all tests
	if env := testutil.TrySetupRealSpriteEnv(); env != nil {
		if err := testutil.CleanupAllSprites(env.Client); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to cleanup sprites: %v\n", err)
		} else {
			// Log success if any sprites were cleaned up
			remaining := testutil.RegisteredSprites()
			if len(remaining) > 0 {
				fmt.Fprintf(os.Stderr, "Warning: %d sprites still registered after cleanup: %v\n", len(remaining), remaining)
			}
		}
	}

	os.Exit(code)
}
