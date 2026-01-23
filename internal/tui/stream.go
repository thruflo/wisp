package tui

import (
	"context"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/thruflo/wisp/internal/logging"
	"github.com/thruflo/wisp/internal/stream"
)

// StreamRunner runs the TUI with stream client integration.
// It connects to a remote Sprite server, subscribes to events, and
// converts user actions to stream commands.
type StreamRunner struct {
	tui    *TUI
	client *stream.StreamClient
}

// NewStreamRunner creates a new StreamRunner for the given TUI and client.
func NewStreamRunner(tui *TUI, client *stream.StreamClient) *StreamRunner {
	return &StreamRunner{
		tui:    tui,
		client: client,
	}
}

// Run executes the TUI event loop with stream client integration.
// It subscribes to stream events and processes them alongside user input.
// Returns when the context is cancelled, the stream ends, or the user exits.
func (r *StreamRunner) Run(ctx context.Context) error {
	// Configure TUI with stream client
	r.tui.SetStreamClient(r.client)

	// Get initial state snapshot
	snapshot, err := r.client.GetState(ctx)
	if err != nil {
		return fmt.Errorf("failed to get initial state: %w", err)
	}
	r.tui.UpdateFromSnapshot(snapshot)

	// Subscribe to stream events starting from last known sequence
	eventCh, errCh := r.client.Subscribe(ctx, snapshot.LastSeq+1)

	// Show input view if there's a pending input request
	// In State Protocol, presence in snapshot means it's pending (not yet responded)
	if snapshot.InputRequest != nil {
		r.tui.ShowInput(snapshot.InputRequest.Question)
		r.tui.Bell()
	}

	// Enter raw mode
	if err := r.tui.terminal.EnterRaw(); err != nil {
		return fmt.Errorf("failed to enter raw mode: %w", err)
	}
	defer r.tui.terminal.ExitRaw()
	defer r.tui.terminal.ShowCursor()

	r.tui.running = true
	defer func() { r.tui.running = false }()

	// Initialize key reader
	r.tui.keyReader = NewKeyReader(r.tui.terminal)

	// Initial render
	r.tui.Update()

	// Input channel for key events
	keyCh := make(chan KeyEvent, 10)
	keyErr := make(chan error, 1)

	// Start key reader goroutine
	go func() {
		for {
			ev, err := r.tui.keyReader.ReadKey()
			if err != nil {
				keyErr <- err
				return
			}
			select {
			case keyCh <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()

	// Event loop
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case err := <-keyErr:
			// Reader error is usually EOF, which is expected on exit
			if err == io.EOF {
				return nil
			}
			return err

		case err := <-errCh:
			// Stream error
			if err != nil {
				return fmt.Errorf("stream error: %w", err)
			}
			// Stream closed normally
			return nil

		case event, ok := <-eventCh:
			if !ok {
				// Event channel closed
				return nil
			}
			r.tui.HandleStreamEvent(event)

		case ev := <-keyCh:
			action := r.tui.handleKeyEvent(ev)
			if action.Action == ActionNone {
				continue
			}

			// Convert action to stream command
			if err := r.handleAction(ctx, action); err != nil {
				logging.Warn("failed to handle TUI action", "error", err, "action", action.Action)
				continue
			}

			// Exit on certain actions
			switch action.Action {
			case ActionQuit, ActionBackground:
				return nil
			}
		}
	}
}

// handleAction converts a TUI action to a stream command and sends it.
func (r *StreamRunner) handleAction(ctx context.Context, action ActionEvent) error {
	switch action.Action {
	case ActionKill:
		commandID := generateCommandID()
		_, err := r.client.SendKillCommand(ctx, commandID, false)
		if err != nil {
			logging.Warn("failed to send kill command", "error", err, "commandID", commandID)
		}
		return err

	case ActionBackground:
		commandID := generateCommandID()
		_, err := r.client.SendBackgroundCommand(ctx, commandID)
		if err != nil {
			logging.Warn("failed to send background command", "error", err, "commandID", commandID)
		}
		return err

	case ActionSubmitInput:
		requestID := r.tui.InputRequestID()
		if requestID == "" {
			return nil
		}
		commandID := generateCommandID()
		_, err := r.client.SendInputResponse(ctx, commandID, requestID, action.Input)
		if err != nil {
			logging.Warn("failed to send input response", "error", err, "commandID", commandID, "requestID", requestID)
			return err
		}
		r.tui.SetInputRequestID("")
		return nil

	default:
		// Other actions don't need stream commands
		return nil
	}
}

// generateCommandID generates a unique command ID.
func generateCommandID() string {
	return uuid.New().String()
}

// RunWithStream is a convenience function that creates and runs a StreamRunner.
// It connects to the stream server, syncs initial state, and runs the TUI event loop.
func RunWithStream(ctx context.Context, tui *TUI, client *stream.StreamClient) error {
	runner := NewStreamRunner(tui, client)
	return runner.Run(ctx)
}
