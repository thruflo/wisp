// Package spriteloop implements the Claude Code iteration loop that runs
// directly on a Sprite VM. This package extracts the core loop logic from
// internal/loop but adapts it for local execution on the Sprite:
//
//   - No SSH: Commands run locally via exec.Command
//   - Direct file access: State files read/written directly to /var/local/wisp/
//   - FileStore integration: Events published to durable stream for remote clients
//   - HTTP server: Exposes stream and command endpoints to TUI/web clients
//
// The spriteloop runs as part of the wisp-sprite binary, which is deployed
// to the Sprite during session setup. This enables the loop to continue
// running even if the client (TUI/laptop) disconnects.
package spriteloop
