package stream

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFileStore(t *testing.T) {
	t.Parallel()

	t.Run("creates store with non-existent file", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "stream.ndjson")

		fs, err := NewFileStore(path)
		require.NoError(t, err)
		require.NotNil(t, fs)
		defer fs.Close()

		assert.Equal(t, path, fs.Path())
		assert.Equal(t, uint64(0), fs.LastSeq())
	})

	t.Run("creates store with existing file", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "stream.ndjson")

		// Create a store and write some events
		fs1, err := NewFileStore(path)
		require.NoError(t, err)

		event1 := MustNewEvent(MessageTypeSession, SessionEvent{ID: "sess-1"})
		require.NoError(t, fs1.Append(event1))
		event2 := MustNewEvent(MessageTypeTask, TaskEvent{ID: "task-1"})
		require.NoError(t, fs1.Append(event2))
		fs1.Close()

		// Create a new store from the same file
		fs2, err := NewFileStore(path)
		require.NoError(t, err)
		defer fs2.Close()

		// Should continue from where we left off
		assert.Equal(t, uint64(2), fs2.LastSeq())

		// New event should get sequence 3
		event3 := MustNewEvent(MessageTypeAck, Ack{CommandID: "cmd-1"})
		require.NoError(t, fs2.Append(event3))
		assert.Equal(t, uint64(3), event3.Seq)
	})

	t.Run("handles empty existing file", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "stream.ndjson")

		// Create empty file
		file, err := os.Create(path)
		require.NoError(t, err)
		file.Close()

		fs, err := NewFileStore(path)
		require.NoError(t, err)
		defer fs.Close()

		assert.Equal(t, uint64(0), fs.LastSeq())
	})
}

func TestFileStoreAppend(t *testing.T) {
	t.Parallel()

	t.Run("appends events with sequence numbers", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "stream.ndjson")

		fs, err := NewFileStore(path)
		require.NoError(t, err)
		defer fs.Close()

		event1 := MustNewEvent(MessageTypeSession, SessionEvent{ID: "sess-1"})
		require.NoError(t, fs.Append(event1))
		assert.Equal(t, uint64(1), event1.Seq)

		event2 := MustNewEvent(MessageTypeTask, TaskEvent{ID: "task-1"})
		require.NoError(t, fs.Append(event2))
		assert.Equal(t, uint64(2), event2.Seq)

		event3 := MustNewEvent(MessageTypeClaudeEvent, ClaudeEvent{ID: "claude-1"})
		require.NoError(t, fs.Append(event3))
		assert.Equal(t, uint64(3), event3.Seq)

		assert.Equal(t, uint64(3), fs.LastSeq())
	})

	t.Run("creates directory if not exists", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "subdir", "nested", "stream.ndjson")

		fs, err := NewFileStore(path)
		require.NoError(t, err)
		defer fs.Close()

		event := MustNewEvent(MessageTypeSession, SessionEvent{ID: "sess-1"})
		require.NoError(t, fs.Append(event))

		// Verify data was stored (durable-streams manages its own storage)
		events, err := fs.Read(0)
		require.NoError(t, err)
		assert.Len(t, events, 1)
	})

	t.Run("persists events to disk", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "stream.ndjson")

		fs, err := NewFileStore(path)
		require.NoError(t, err)

		event := MustNewEvent(MessageTypeSession, SessionEvent{
			ID:     "sess-test",
			Repo:   "owner/repo",
			Branch: "main",
			Status: SessionStatusRunning,
		})
		require.NoError(t, fs.Append(event))
		fs.Close()

		// Reopen and verify data persisted
		fs2, err := NewFileStore(path)
		require.NoError(t, err)
		defer fs2.Close()

		events, err := fs2.Read(0)
		require.NoError(t, err)
		require.Len(t, events, 1)

		sessionData, err := events[0].SessionData()
		require.NoError(t, err)
		assert.Equal(t, "sess-test", sessionData.ID)
		assert.Equal(t, "owner/repo", sessionData.Repo)
		assert.Equal(t, "main", sessionData.Branch)
	})
}

func TestFileStoreRead(t *testing.T) {
	t.Parallel()

	t.Run("reads all events when fromSeq is 0", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "stream.ndjson")

		fs, err := NewFileStore(path)
		require.NoError(t, err)
		defer fs.Close()

		// Append some events
		for i := 0; i < 5; i++ {
			event := MustNewEvent(MessageTypeSession, SessionEvent{ID: "sess-1"})
			require.NoError(t, fs.Append(event))
		}

		events, err := fs.Read(0)
		require.NoError(t, err)
		assert.Len(t, events, 5)

		// Verify sequence numbers
		for i, event := range events {
			assert.Equal(t, uint64(i+1), event.Seq)
		}
	})

	t.Run("reads events from specific sequence", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "stream.ndjson")

		fs, err := NewFileStore(path)
		require.NoError(t, err)
		defer fs.Close()

		// Append 10 events
		for i := 0; i < 10; i++ {
			event := MustNewEvent(MessageTypeSession, SessionEvent{ID: "sess-1"})
			require.NoError(t, fs.Append(event))
		}

		// Read from sequence 6
		events, err := fs.Read(6)
		require.NoError(t, err)
		assert.Len(t, events, 5) // Events 6, 7, 8, 9, 10

		assert.Equal(t, uint64(6), events[0].Seq)
		assert.Equal(t, uint64(10), events[4].Seq)
	})

	t.Run("returns empty slice for non-existent file", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "nonexistent.ndjson")

		fs, err := NewFileStore(path)
		require.NoError(t, err)
		defer fs.Close()

		events, err := fs.Read(0)
		require.NoError(t, err)
		assert.Empty(t, events)
	})

	t.Run("returns empty slice when fromSeq is beyond last event", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "stream.ndjson")

		fs, err := NewFileStore(path)
		require.NoError(t, err)
		defer fs.Close()

		// Append 3 events
		for i := 0; i < 3; i++ {
			event := MustNewEvent(MessageTypeSession, SessionEvent{ID: "sess-1"})
			require.NoError(t, fs.Append(event))
		}

		// Read from sequence 100
		events, err := fs.Read(100)
		require.NoError(t, err)
		assert.Empty(t, events)
	})

	t.Run("preserves event data through read", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "stream.ndjson")

		fs, err := NewFileStore(path)
		require.NoError(t, err)
		defer fs.Close()

		originalSession := SessionEvent{
			ID:        "sess-abc",
			Repo:      "owner/repo",
			Branch:    "feature-branch",
			Spec:      "# RFC\n\nContent here",
			Status:    SessionStatusRunning,
			Iteration: 42,
			StartedAt: time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC),
		}

		event := MustNewEvent(MessageTypeSession, originalSession)
		require.NoError(t, fs.Append(event))

		events, err := fs.Read(1)
		require.NoError(t, err)
		require.Len(t, events, 1)

		// Extract session data and verify
		sessionData, err := events[0].SessionData()
		require.NoError(t, err)
		assert.Equal(t, originalSession.ID, sessionData.ID)
		assert.Equal(t, originalSession.Repo, sessionData.Repo)
		assert.Equal(t, originalSession.Branch, sessionData.Branch)
		assert.Equal(t, originalSession.Spec, sessionData.Spec)
		assert.Equal(t, originalSession.Status, sessionData.Status)
		assert.Equal(t, originalSession.Iteration, sessionData.Iteration)
	})
}

func TestFileStoreSubscribe(t *testing.T) {
	t.Parallel()

	t.Run("receives existing events", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "stream.ndjson")

		fs, err := NewFileStore(path)
		require.NoError(t, err)
		defer fs.Close()

		// Append some events before subscribing
		for i := 0; i < 3; i++ {
			event := MustNewEvent(MessageTypeSession, SessionEvent{ID: "sess-1"})
			require.NoError(t, fs.Append(event))
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		ch, err := fs.Subscribe(ctx, 0, 50*time.Millisecond)
		require.NoError(t, err)

		var received []*Event
		for i := 0; i < 3; i++ {
			select {
			case event := <-ch:
				received = append(received, event)
			case <-ctx.Done():
				t.Fatal("timeout waiting for events")
			}
		}

		assert.Len(t, received, 3)
		assert.Equal(t, uint64(1), received[0].Seq)
		assert.Equal(t, uint64(2), received[1].Seq)
		assert.Equal(t, uint64(3), received[2].Seq)
	})

	t.Run("receives new events as they are written", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "stream.ndjson")

		fs, err := NewFileStore(path)
		require.NoError(t, err)
		defer fs.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		ch, err := fs.Subscribe(ctx, 0, 50*time.Millisecond)
		require.NoError(t, err)

		// Write events after subscribing
		go func() {
			time.Sleep(100 * time.Millisecond)
			for i := 0; i < 3; i++ {
				event := MustNewEvent(MessageTypeTask, TaskEvent{ID: "task-1"})
				fs.Append(event)
				time.Sleep(60 * time.Millisecond)
			}
		}()

		var received []*Event
		for i := 0; i < 3; i++ {
			select {
			case event := <-ch:
				received = append(received, event)
			case <-ctx.Done():
				t.Fatal("timeout waiting for events")
			}
		}

		assert.Len(t, received, 3)
	})

	t.Run("respects fromSeq parameter", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "stream.ndjson")

		fs, err := NewFileStore(path)
		require.NoError(t, err)
		defer fs.Close()

		// Append 5 events
		for i := 0; i < 5; i++ {
			event := MustNewEvent(MessageTypeSession, SessionEvent{ID: "sess-1"})
			require.NoError(t, fs.Append(event))
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		// Subscribe from sequence 3
		ch, err := fs.Subscribe(ctx, 3, 50*time.Millisecond)
		require.NoError(t, err)

		var received []*Event
		for i := 0; i < 3; i++ {
			select {
			case event := <-ch:
				received = append(received, event)
			case <-ctx.Done():
				t.Fatal("timeout waiting for events")
			}
		}

		assert.Len(t, received, 3)
		assert.Equal(t, uint64(3), received[0].Seq)
		assert.Equal(t, uint64(4), received[1].Seq)
		assert.Equal(t, uint64(5), received[2].Seq)
	})

	t.Run("channel is closed when context is canceled", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "stream.ndjson")

		fs, err := NewFileStore(path)
		require.NoError(t, err)
		defer fs.Close()

		ctx, cancel := context.WithCancel(context.Background())

		ch, err := fs.Subscribe(ctx, 0, 50*time.Millisecond)
		require.NoError(t, err)

		// Cancel context
		cancel()

		// Wait for channel to close
		select {
		case _, ok := <-ch:
			if ok {
				// Drain any buffered events
				for range ch {
				}
			}
		case <-time.After(500 * time.Millisecond):
			t.Fatal("channel was not closed after context cancellation")
		}
	})
}

func TestFileStoreConcurrency(t *testing.T) {
	t.Parallel()

	t.Run("concurrent appends are serialized", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "stream.ndjson")

		fs, err := NewFileStore(path)
		require.NoError(t, err)
		defer fs.Close()

		const numGoroutines = 10
		const eventsPerGoroutine = 100

		var wg sync.WaitGroup
		wg.Add(numGoroutines)

		for i := 0; i < numGoroutines; i++ {
			go func(goroutineID int) {
				defer wg.Done()
				for j := 0; j < eventsPerGoroutine; j++ {
					event := MustNewEvent(MessageTypeSession, SessionEvent{ID: "sess-1"})
					if err := fs.Append(event); err != nil {
						t.Errorf("append failed: %v", err)
					}
				}
			}(i)
		}

		wg.Wait()

		// Verify all events were written with unique sequence numbers
		events, err := fs.Read(0)
		require.NoError(t, err)
		assert.Len(t, events, numGoroutines*eventsPerGoroutine)

		// Verify sequence numbers are unique and consecutive
		seenSeqs := make(map[uint64]bool)
		for _, event := range events {
			assert.False(t, seenSeqs[event.Seq], "duplicate sequence number: %d", event.Seq)
			seenSeqs[event.Seq] = true
		}

		// Verify we have all sequence numbers from 1 to total
		for seq := uint64(1); seq <= uint64(numGoroutines*eventsPerGoroutine); seq++ {
			assert.True(t, seenSeqs[seq], "missing sequence number: %d", seq)
		}
	})

	t.Run("concurrent reads don't block appends", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "stream.ndjson")

		fs, err := NewFileStore(path)
		require.NoError(t, err)
		defer fs.Close()

		// Write initial events
		for i := 0; i < 10; i++ {
			event := MustNewEvent(MessageTypeSession, SessionEvent{ID: "sess-1"})
			require.NoError(t, fs.Append(event))
		}

		var wg sync.WaitGroup
		wg.Add(2)

		// Reader goroutine
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_, err := fs.Read(0)
				if err != nil {
					t.Errorf("read failed: %v", err)
				}
			}
		}()

		// Writer goroutine
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				event := MustNewEvent(MessageTypeSession, SessionEvent{ID: "sess-1"})
				if err := fs.Append(event); err != nil {
					t.Errorf("append failed: %v", err)
				}
			}
		}()

		wg.Wait()
	})
}

func TestFileStoreClose(t *testing.T) {
	t.Parallel()

	t.Run("close releases resources", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "stream.ndjson")

		fs, err := NewFileStore(path)
		require.NoError(t, err)

		// Write an event to open the file
		event := MustNewEvent(MessageTypeSession, SessionEvent{ID: "sess-1"})
		require.NoError(t, fs.Append(event))

		// Close should succeed
		err = fs.Close()
		require.NoError(t, err)

		// Close again should be safe (no-op)
		err = fs.Close()
		require.NoError(t, err)
	})

	t.Run("close without writing is safe", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "stream.ndjson")

		fs, err := NewFileStore(path)
		require.NoError(t, err)

		// Close without writing anything
		err = fs.Close()
		require.NoError(t, err)
	})
}

func TestFileStoreLastSeq(t *testing.T) {
	t.Parallel()

	t.Run("returns 0 for empty store", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "stream.ndjson")

		fs, err := NewFileStore(path)
		require.NoError(t, err)
		defer fs.Close()

		assert.Equal(t, uint64(0), fs.LastSeq())
	})

	t.Run("returns correct sequence after writes", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "stream.ndjson")

		fs, err := NewFileStore(path)
		require.NoError(t, err)
		defer fs.Close()

		for i := 0; i < 5; i++ {
			event := MustNewEvent(MessageTypeSession, SessionEvent{ID: "sess-1"})
			require.NoError(t, fs.Append(event))
			assert.Equal(t, uint64(i+1), fs.LastSeq())
		}
	})
}

// TestFileStoreHandlesMalformedLines is removed because the new durable-streams
// based implementation manages its own internal storage format and does not
// support manually writing malformed data to its storage. The durable-streams
// library handles data integrity internally.

func TestFileStoreMultipleEventTypes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "stream.ndjson")

	fs, err := NewFileStore(path)
	require.NoError(t, err)
	defer fs.Close()

	// Write different event types
	require.NoError(t, fs.Append(MustNewEvent(MessageTypeSession, SessionEvent{ID: "sess-1"})))
	require.NoError(t, fs.Append(MustNewEvent(MessageTypeTask, TaskEvent{ID: "task-1"})))
	require.NoError(t, fs.Append(MustNewEvent(MessageTypeClaudeEvent, ClaudeEvent{ID: "claude-1"})))
	require.NoError(t, fs.Append(MustNewEvent(MessageTypeInputRequest, InputRequestEvent{ID: "input-1"})))
	require.NoError(t, fs.Append(MustNewEvent(MessageTypeAck, Ack{CommandID: "cmd-1"})))
	require.NoError(t, fs.Append(MustNewEvent(MessageTypeCommand, Command{ID: "cmd-2"})))

	events, err := fs.Read(0)
	require.NoError(t, err)
	assert.Len(t, events, 6)

	// Verify types
	assert.Equal(t, MessageTypeSession, events[0].Type)
	assert.Equal(t, MessageTypeTask, events[1].Type)
	assert.Equal(t, MessageTypeClaudeEvent, events[2].Type)
	assert.Equal(t, MessageTypeInputRequest, events[3].Type)
	assert.Equal(t, MessageTypeAck, events[4].Type)
	assert.Equal(t, MessageTypeCommand, events[5].Type)
}
