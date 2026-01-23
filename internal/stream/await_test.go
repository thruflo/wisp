package stream

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestAwaitInputResponse(t *testing.T) {
	t.Run("finds existing response", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		if err != nil {
			t.Fatalf("failed to create FileStore: %v", err)
		}
		defer fs.Close()

		// Append an input request and response
		reqEvent, _ := NewInputRequestEvent(&InputRequest{
			ID:        "req-1",
			SessionID: "test-session",
			Iteration: 1,
			Question:  "What?",
		})
		fs.Append(reqEvent)

		respEvent, _ := NewInputResponseEvent(&InputResponse{
			ID:        "resp-1",
			RequestID: "req-1",
			Response:  "Test answer",
		})
		fs.Append(respEvent)

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		// Should find the existing response immediately
		response, err := AwaitInputResponse(ctx, fs, "req-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if response != "Test answer" {
			t.Errorf("expected 'Test answer', got %q", response)
		}
	})

	t.Run("waits for new response", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		if err != nil {
			t.Fatalf("failed to create FileStore: %v", err)
		}
		defer fs.Close()

		// Append an input request
		reqEvent, _ := NewInputRequestEvent(&InputRequest{
			ID:        "req-2",
			SessionID: "test-session",
			Iteration: 1,
			Question:  "What?",
		})
		fs.Append(reqEvent)

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		// Start waiting in a goroutine
		resultCh := make(chan string, 1)
		errCh := make(chan error, 1)
		go func() {
			response, err := AwaitInputResponse(ctx, fs, "req-2")
			if err != nil {
				errCh <- err
				return
			}
			resultCh <- response
		}()

		// Give the watcher time to subscribe
		time.Sleep(100 * time.Millisecond)

		// Append the response
		respEvent, _ := NewInputResponseEvent(&InputResponse{
			ID:        "resp-2",
			RequestID: "req-2",
			Response:  "Delayed answer",
		})
		fs.Append(respEvent)

		// Should receive the response
		select {
		case response := <-resultCh:
			if response != "Delayed answer" {
				t.Errorf("expected 'Delayed answer', got %q", response)
			}
		case err := <-errCh:
			t.Fatalf("unexpected error: %v", err)
		case <-time.After(500 * time.Millisecond):
			t.Error("timeout waiting for response")
		}
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		if err != nil {
			t.Fatalf("failed to create FileStore: %v", err)
		}
		defer fs.Close()

		ctx, cancel := context.WithCancel(context.Background())

		// Start waiting in a goroutine
		errCh := make(chan error, 1)
		go func() {
			_, err := AwaitInputResponse(ctx, fs, "nonexistent")
			errCh <- err
		}()

		// Cancel immediately
		cancel()

		// Should receive context error
		select {
		case err := <-errCh:
			if err != context.Canceled {
				t.Errorf("expected context.Canceled, got %v", err)
			}
		case <-time.After(500 * time.Millisecond):
			t.Error("timeout waiting for cancellation")
		}
	})
}

func TestAwaitEventWithTxID(t *testing.T) {
	t.Run("finds existing event with txid", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		if err != nil {
			t.Fatalf("failed to create FileStore: %v", err)
		}
		defer fs.Close()

		// Append an event with TxID
		event, _ := NewEventWithOp(MessageTypeInputResponse, "input_response:test", &InputResponse{
			ID:        "resp-tx",
			RequestID: "req-tx",
			Response:  "Answer with txid",
		}, OperationInsert)
		event.Headers.TxID = "tx-123"
		fs.Append(event)

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		// Should find the event with matching txid
		found, err := AwaitEventWithTxID(ctx, fs, "tx-123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if found.Headers.TxID != "tx-123" {
			t.Errorf("expected txid 'tx-123', got %q", found.Headers.TxID)
		}
	})
}

func TestInputResponseWatcher(t *testing.T) {
	t.Run("receives response via watcher", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		if err != nil {
			t.Fatalf("failed to create FileStore: %v", err)
		}
		defer fs.Close()

		watcher := NewInputResponseWatcher(fs, "watch-req-1")

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		watcher.Start(ctx)
		defer watcher.Stop()

		// Give watcher time to subscribe
		time.Sleep(50 * time.Millisecond)

		// Append the response
		respEvent, _ := NewInputResponseEvent(&InputResponse{
			ID:        "watch-resp-1",
			RequestID: "watch-req-1",
			Response:  "Watched answer",
		})
		fs.Append(respEvent)

		// Should receive via ResultCh
		select {
		case response := <-watcher.ResultCh():
			if response != "Watched answer" {
				t.Errorf("expected 'Watched answer', got %q", response)
			}
		case err := <-watcher.ErrCh():
			t.Fatalf("unexpected error: %v", err)
		case <-time.After(500 * time.Millisecond):
			t.Error("timeout waiting for watcher result")
		}
	})

	t.Run("stop cancels watching", func(t *testing.T) {
		tmpDir := t.TempDir()
		fs, err := NewFileStore(filepath.Join(tmpDir, "stream.ndjson"))
		if err != nil {
			t.Fatalf("failed to create FileStore: %v", err)
		}
		defer fs.Close()

		watcher := NewInputResponseWatcher(fs, "stop-req")

		ctx := context.Background()
		watcher.Start(ctx)

		// Stop immediately
		watcher.Stop()

		// Should receive error or closed channel
		select {
		case err := <-watcher.ErrCh():
			if err != context.Canceled {
				t.Errorf("expected context.Canceled, got %v", err)
			}
		case <-time.After(200 * time.Millisecond):
			// Also acceptable - may not have started yet
		}
	})
}
