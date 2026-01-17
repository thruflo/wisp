package testutil

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/thruflo/wisp/internal/sprite"
)

// createdSprites is a thread-safe registry of sprites created during tests.
// This allows for cleanup of all test sprites after test runs.
var (
	createdSprites = make(map[string]bool)
	spritesMu      sync.Mutex
)

// RegisterSprite records a sprite name for potential cleanup.
// This should be called after successfully creating a sprite in tests.
// The sprite will be deleted when CleanupAllSprites is called.
func RegisterSprite(name string) {
	spritesMu.Lock()
	defer spritesMu.Unlock()
	createdSprites[name] = true
}

// UnregisterSprite removes a sprite from the cleanup registry.
// Use this if a sprite was already cleaned up manually.
func UnregisterSprite(name string) {
	spritesMu.Lock()
	defer spritesMu.Unlock()
	delete(createdSprites, name)
}

// RegisteredSprites returns a copy of all registered sprite names.
// This is useful for debugging or logging.
func RegisteredSprites() []string {
	spritesMu.Lock()
	defer spritesMu.Unlock()
	names := make([]string, 0, len(createdSprites))
	for name := range createdSprites {
		names = append(names, name)
	}
	return names
}

// CleanupAllSprites deletes all registered sprites using the provided client.
// Returns an error combining all individual deletion errors.
// Successfully deleted sprites are removed from the registry.
func CleanupAllSprites(client sprite.Client) error {
	spritesMu.Lock()
	// Copy names to avoid holding lock during network calls
	names := make([]string, 0, len(createdSprites))
	for name := range createdSprites {
		names = append(names, name)
	}
	spritesMu.Unlock()

	var errs []error
	for _, name := range names {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := client.Delete(ctx, name)
		cancel()

		if err != nil {
			errs = append(errs, err)
		} else {
			// Only remove from registry if deletion succeeded
			spritesMu.Lock()
			delete(createdSprites, name)
			spritesMu.Unlock()
		}
	}

	return errors.Join(errs...)
}

// ClearRegistry removes all entries from the sprite registry without deleting any sprites.
// This is useful for resetting state between tests.
func ClearRegistry() {
	spritesMu.Lock()
	defer spritesMu.Unlock()
	createdSprites = make(map[string]bool)
}
