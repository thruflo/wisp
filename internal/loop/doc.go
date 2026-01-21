// Package loop provides orchestration for coordinating with wisp-sprite.
//
// The actual iteration loop runs on the Sprite via the wisp-sprite binary
// (see internal/spriteloop). This package handles:
//   - Connecting to wisp-sprite's stream server
//   - Processing stream events and updating the TUI
//   - Forwarding TUI actions as stream commands
//   - Broadcasting events to web clients
//   - Syncing state files from Sprite to local storage
//
// Helper functions for progress tracking (DetectStuck, CalculateProgress,
// ProgressRate) are exported for use by integration tests and other packages.
package loop
