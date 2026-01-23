// Package stream provides shared types and utilities for durable stream
// communication between wisp-sprite (on the Sprite VM) and clients (TUI/web).
package stream

import (
	"context"
	"time"
)

// AwaitInputResponse waits for an input_response event matching the given request ID.
// It subscribes to the FileStore stream and returns when a matching response is found.
// This replaces polling-based input waiting with event-driven waiting per the State Protocol.
//
// The function returns the response string when found, or an error if:
// - The context is canceled (returns ctx.Err())
// - The request ID doesn't match any response
func AwaitInputResponse(ctx context.Context, fs *FileStore, requestID string) (string, error) {
	// Subscribe to the stream starting from the current position
	// We need to check existing events first in case the response was already appended
	fromSeq := fs.LastSeq()
	if fromSeq > 0 {
		fromSeq = 1 // Check from beginning to not miss any responses
	}

	// First, check existing events for a response
	events, err := fs.Read(fromSeq)
	if err == nil {
		for _, event := range events {
			if event.Type == MessageTypeInputResponse {
				ir, err := event.InputResponseData()
				if err == nil && ir.RequestID == requestID {
					return ir.Response, nil
				}
			}
		}
	}

	// Subscribe for new events
	eventCh, err := fs.Subscribe(ctx, fs.LastSeq()+1, 50*time.Millisecond)
	if err != nil {
		return "", err
	}

	// Wait for a matching input_response event
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case event, ok := <-eventCh:
			if !ok {
				// Channel closed (context canceled)
				return "", ctx.Err()
			}

			if event.Type == MessageTypeInputResponse {
				ir, err := event.InputResponseData()
				if err == nil && ir.RequestID == requestID {
					return ir.Response, nil
				}
			}
		}
	}
}

// AwaitEventWithTxID waits for an event with a matching TxID in its headers.
// This implements the State Protocol awaitTxId() pattern for transaction confirmation.
// The function returns the event when found, or an error if the context is canceled.
func AwaitEventWithTxID(ctx context.Context, fs *FileStore, txID string) (*Event, error) {
	// First, check existing events
	events, err := fs.Read(1)
	if err == nil {
		for _, event := range events {
			if event.Headers.TxID == txID {
				return event, nil
			}
		}
	}

	// Subscribe for new events
	eventCh, err := fs.Subscribe(ctx, fs.LastSeq()+1, 50*time.Millisecond)
	if err != nil {
		return nil, err
	}

	// Wait for a matching event
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case event, ok := <-eventCh:
			if !ok {
				return nil, ctx.Err()
			}

			if event.Headers.TxID == txID {
				return event, nil
			}
		}
	}
}

// InputResponseWatcher provides a channel-based interface for watching input responses.
// This is useful when the loop needs to also handle other events (commands) while waiting.
type InputResponseWatcher struct {
	fs        *FileStore
	requestID string
	resultCh  chan string
	errCh     chan error
	cancel    context.CancelFunc
}

// NewInputResponseWatcher creates a new watcher for an input response.
// Call Start() to begin watching, and Stop() when done.
func NewInputResponseWatcher(fs *FileStore, requestID string) *InputResponseWatcher {
	return &InputResponseWatcher{
		fs:        fs,
		requestID: requestID,
		resultCh:  make(chan string, 1),
		errCh:     make(chan error, 1),
	}
}

// Start begins watching for the input response in a goroutine.
// The result will be sent to ResultCh() when found.
func (w *InputResponseWatcher) Start(ctx context.Context) {
	watchCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel

	go func() {
		response, err := AwaitInputResponse(watchCtx, w.fs, w.requestID)
		if err != nil {
			select {
			case w.errCh <- err:
			default:
			}
			return
		}
		select {
		case w.resultCh <- response:
		default:
		}
	}()
}

// Stop stops watching for the input response.
func (w *InputResponseWatcher) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
}

// ResultCh returns the channel that receives the response when found.
func (w *InputResponseWatcher) ResultCh() <-chan string {
	return w.resultCh
}

// ErrCh returns the channel that receives errors.
func (w *InputResponseWatcher) ErrCh() <-chan error {
	return w.errCh
}
