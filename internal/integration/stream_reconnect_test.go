//go:build integration

// stream_reconnect_test.go tests the disconnect/reconnect scenario for the
// durable stream architecture. This verifies that:
// - The stream server continues appending events during client disconnect
// - Clients can reconnect and catch up on missed events using stored lastSeq
// - No events are lost during the disconnect/reconnect cycle
package integration

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thruflo/wisp/internal/spriteloop"
	"github.com/thruflo/wisp/internal/stream"
)

// TestStreamDisconnectReconnect verifies the durable stream architecture handles
// disconnections gracefully. This simulates the core value proposition of the
// RFC: network disconnects don't lose work.
func TestStreamDisconnectReconnect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	t.Run("client can catch up on missed events after reconnect", func(t *testing.T) {
		// This test demonstrates the core value of durable streams:
		// - Events persist in FileStore during client disconnect
		// - A new subscription from lastSeq recovers all missed events
		// - No Claude output is lost

		tmpDir := t.TempDir()
		streamPath := filepath.Join(tmpDir, "stream.ndjson")

		fileStore, err := stream.NewFileStore(streamPath)
		require.NoError(t, err)
		defer fileStore.Close()

		port := 19700 + time.Now().Nanosecond()%500

		server := spriteloop.NewServer(spriteloop.ServerOptions{
			Port:         port,
			FileStore:    fileStore,
			PollInterval: 50 * time.Millisecond,
		})

		err = server.Start()
		if err != nil {
			t.Skipf("Could not start server (port %d in use?): %v", port, err)
		}

		serverURL := fmt.Sprintf("http://localhost:%d", port)

		// Phase 1: Client connects and receives initial events
		for i := 0; i < 3; i++ {
			event, err := stream.NewEvent(stream.MessageTypeClaudeEvent, &stream.ClaudeEvent{
				ID:        fmt.Sprintf("event-phase1-%d", i),
				SessionID: "test-session",
				Iteration: 1,
				Sequence:  i,
				Message:   fmt.Sprintf("Claude output line %d", i),
			})
			require.NoError(t, err)
			require.NoError(t, fileStore.Append(event))
		}

		client := stream.NewStreamClient(serverURL)

		ctx1, cancel1 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel1()

		err = client.Connect(ctx1)
		require.NoError(t, err, "client should connect to server")

		eventCh, errCh := client.Subscribe(ctx1, 0)

		receivedPhase1 := collectEvents(t, eventCh, errCh, 3, 2*time.Second)
		require.Len(t, receivedPhase1, 3, "should receive all 3 initial events")
		assert.Equal(t, "event-phase1-0", mustClaudeEventID(t, receivedPhase1[0]))

		// Record last sequence for reconnection
		lastSeqBeforeDisconnect := client.LastSeq()
		t.Logf("Last seq before disconnect: %d", lastSeqBeforeDisconnect)

		// Phase 2: Simulate disconnect - stop server, write events to FileStore
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		err = server.Stop(stopCtx)
		stopCancel()
		require.NoError(t, err, "server should stop gracefully")
		cancel1() // Cancel first subscription

		t.Log("Server stopped - simulating disconnect")

		// Write events while server is down (simulating Sprite continuing work)
		for i := 0; i < 5; i++ {
			event, err := stream.NewEvent(stream.MessageTypeClaudeEvent, &stream.ClaudeEvent{
				ID:        fmt.Sprintf("event-disconnected-%d", i),
				SessionID: "test-session",
				Iteration: 2,
				Sequence:  i,
				Message:   fmt.Sprintf("Claude output during disconnect %d", i),
			})
			require.NoError(t, err)
			require.NoError(t, fileStore.Append(event))
		}

		t.Logf("Wrote 5 events while disconnected. FileStore lastSeq: %d", fileStore.LastSeq())

		// Phase 3: Restart server and reconnect
		server2 := spriteloop.NewServer(spriteloop.ServerOptions{
			Port:         port,
			FileStore:    fileStore,
			PollInterval: 50 * time.Millisecond,
		})

		err = server2.Start()
		require.NoError(t, err, "server should restart successfully")
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			server2.Stop(ctx)
		}()

		t.Log("Server restarted")

		// Write more events after server restart
		for i := 0; i < 2; i++ {
			event, err := stream.NewEvent(stream.MessageTypeClaudeEvent, &stream.ClaudeEvent{
				ID:        fmt.Sprintf("event-phase3-%d", i),
				SessionID: "test-session",
				Iteration: 3,
				Sequence:  i,
				Message:   fmt.Sprintf("Claude output after reconnect %d", i),
			})
			require.NoError(t, err)
			require.NoError(t, fileStore.Append(event))
		}

		// Create new subscription from where we left off
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel2()

		// Reconnect from lastSeq + 1 to catch up on missed events
		eventCh2, errCh2 := client.Subscribe(ctx2, lastSeqBeforeDisconnect+1)

		// Should receive: 5 events during disconnect + 2 after = 7
		receivedAfterReconnect := collectEvents(t, eventCh2, errCh2, 7, 3*time.Second)

		t.Logf("Received %d events after reconnect", len(receivedAfterReconnect))

		require.Len(t, receivedAfterReconnect, 7,
			"should receive exactly 7 events (5 during disconnect + 2 after)")

		// Verify sequence numbers are correct (no gaps, no duplicates)
		for i, event := range receivedAfterReconnect {
			expectedSeq := lastSeqBeforeDisconnect + 1 + uint64(i)
			assert.Equal(t, expectedSeq, event.Seq,
				"event %d should have seq %d, got %d", i, expectedSeq, event.Seq)
		}

		// Verify first missed event is "event-disconnected-0"
		assert.Equal(t, "event-disconnected-0", mustClaudeEventID(t, receivedAfterReconnect[0]))

		// Verify total events across both subscriptions
		totalReceived := len(receivedPhase1) + len(receivedAfterReconnect)
		t.Logf("Total events received: %d (expected 10)", totalReceived)
		assert.Equal(t, 10, totalReceived, "should receive all 10 events total")
	})

	t.Run("client catches up from specific sequence number", func(t *testing.T) {
		// This test verifies that a new client can join mid-stream and catch up
		tmpDir := t.TempDir()
		streamPath := filepath.Join(tmpDir, "stream.ndjson")

		fileStore, err := stream.NewFileStore(streamPath)
		require.NoError(t, err)
		defer fileStore.Close()

		// Write 10 events
		for i := 0; i < 10; i++ {
			event, err := stream.NewEvent(stream.MessageTypeClaudeEvent, &stream.ClaudeEvent{
				ID:        fmt.Sprintf("event-%d", i),
				SessionID: "test-session",
				Iteration: 1,
				Sequence:  i,
			})
			require.NoError(t, err)
			require.NoError(t, fileStore.Append(event))
		}

		port := 19750 + time.Now().Nanosecond()%500
		server := spriteloop.NewServer(spriteloop.ServerOptions{
			Port:         port,
			FileStore:    fileStore,
			PollInterval: 50 * time.Millisecond,
		})

		err = server.Start()
		if err != nil {
			t.Skipf("Could not start server: %v", err)
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			server.Stop(ctx)
		}()

		// Client connects and subscribes from seq 6 (should get events 6-10)
		client := stream.NewStreamClient(fmt.Sprintf("http://localhost:%d", port))

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		eventCh, errCh := client.Subscribe(ctx, 6)

		received := collectEvents(t, eventCh, errCh, 5, 3*time.Second)

		require.Len(t, received, 5, "should receive 5 events (seq 6-10)")

		// Verify sequences
		expectedSeqs := []uint64{6, 7, 8, 9, 10}
		for i, event := range received {
			assert.Equal(t, expectedSeqs[i], event.Seq, "event %d should have seq %d", i, expectedSeqs[i])
		}
	})

	t.Run("Claude output not lost during disconnect", func(t *testing.T) {
		// This test simulates continuous Claude output during a network disconnect,
		// verifying that all output is recoverable via the durable stream.
		tmpDir := t.TempDir()
		streamPath := filepath.Join(tmpDir, "stream.ndjson")

		fileStore, err := stream.NewFileStore(streamPath)
		require.NoError(t, err)
		defer fileStore.Close()

		port := 19800 + time.Now().Nanosecond()%500
		server := spriteloop.NewServer(spriteloop.ServerOptions{
			Port:         port,
			FileStore:    fileStore,
			PollInterval: 50 * time.Millisecond,
		})

		err = server.Start()
		if err != nil {
			t.Skipf("Could not start server: %v", err)
		}

		serverURL := fmt.Sprintf("http://localhost:%d", port)

		// Simulate Claude producing output continuously
		var wg sync.WaitGroup
		claudeOutputDone := make(chan struct{})

		wg.Add(1)
		go func() {
			defer wg.Done()
			iteration := 0
			for {
				select {
				case <-claudeOutputDone:
					return
				default:
					event, _ := stream.NewEvent(stream.MessageTypeClaudeEvent, &stream.ClaudeEvent{
						ID:        fmt.Sprintf("claude-%d", iteration),
						SessionID: "test-session",
						Iteration: 1,
						Sequence:  iteration,
						Message:   map[string]any{"type": "text", "text": fmt.Sprintf("line %d", iteration)},
					})
					fileStore.Append(event)
					iteration++
					time.Sleep(50 * time.Millisecond) // Simulate Claude output rate
				}
			}
		}()

		// Let Claude produce some output
		time.Sleep(200 * time.Millisecond)

		// Client connects
		client := stream.NewStreamClient(serverURL)

		ctx1, cancel1 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel1()

		eventCh, errCh := client.Subscribe(ctx1, 0)

		// Collect some events
		phase1Events := collectEvents(t, eventCh, errCh, 3, 2*time.Second)
		require.GreaterOrEqual(t, len(phase1Events), 3, "should receive initial events")
		lastSeqBefore := client.LastSeq()

		// Stop the server (simulating network issue)
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
		server.Stop(stopCtx)
		stopCancel()
		cancel1() // Cancel first subscription

		// Claude continues producing during disconnect
		time.Sleep(200 * time.Millisecond)

		eventsWrittenDuringDisconnect := fileStore.LastSeq() - lastSeqBefore
		t.Logf("Events written during disconnect: %d", eventsWrittenDuringDisconnect)
		require.Greater(t, eventsWrittenDuringDisconnect, uint64(0),
			"should have events written during disconnect")

		// Restart server
		server2 := spriteloop.NewServer(spriteloop.ServerOptions{
			Port:         port,
			FileStore:    fileStore,
			PollInterval: 50 * time.Millisecond,
		})
		err = server2.Start()
		require.NoError(t, err)
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			server2.Stop(ctx)
		}()

		// Let Claude produce a bit more
		time.Sleep(200 * time.Millisecond)

		// Stop Claude
		close(claudeOutputDone)
		wg.Wait()

		totalEventsInStore := fileStore.LastSeq()

		// Create new subscription from where we left off
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel2()

		eventCh2, errCh2 := client.Subscribe(ctx2, lastSeqBefore+1)

		// Calculate expected remaining events
		expectedRemaining := int(totalEventsInStore - lastSeqBefore)
		if expectedRemaining > 30 {
			expectedRemaining = 30 // Cap to avoid waiting too long
		}
		phase2Events := collectEvents(t, eventCh2, errCh2, expectedRemaining, 3*time.Second)

		t.Logf("Total events in store: %d", totalEventsInStore)
		t.Logf("Events received in phase 1: %d (seqs 1-%d)", len(phase1Events), lastSeqBefore)
		t.Logf("Events received in phase 2: %d (seqs %d-%d)", len(phase2Events), lastSeqBefore+1, totalEventsInStore)

		// Key assertion: all events written during disconnect should be recovered
		// The phase2Events should include all events from lastSeqBefore+1 to totalEventsInStore
		assert.GreaterOrEqual(t, len(phase2Events), int(eventsWrittenDuringDisconnect),
			"should have recovered at least all events written during disconnect (got %d, wrote %d during disconnect)",
			len(phase2Events), eventsWrittenDuringDisconnect)

		// Verify the first event in phase2 is the first missed event
		if len(phase2Events) > 0 {
			assert.Equal(t, lastSeqBefore+1, phase2Events[0].Seq,
				"first recovered event should have seq %d", lastSeqBefore+1)
		}

		// Verify sequences are contiguous (no gaps)
		for i := 1; i < len(phase2Events); i++ {
			assert.Equal(t, phase2Events[i-1].Seq+1, phase2Events[i].Seq,
				"sequences should be contiguous: %d -> %d at index %d", phase2Events[i-1].Seq, phase2Events[i].Seq, i)
		}

		t.Logf("SUCCESS: All %d events written during disconnect were recovered", eventsWrittenDuringDisconnect)
	})
}

// collectEvents reads events from the channel until count is reached or timeout expires.
func collectEvents(t *testing.T, eventCh <-chan *stream.Event, errCh <-chan error, count int, timeout time.Duration) []*stream.Event {
	t.Helper()

	var events []*stream.Event
	deadline := time.After(timeout)

	for len(events) < count {
		select {
		case event, ok := <-eventCh:
			if !ok {
				t.Logf("Event channel closed after receiving %d events", len(events))
				return events
			}
			events = append(events, event)
		case err, ok := <-errCh:
			if ok && err != nil {
				t.Logf("Error from stream: %v (continuing to collect)", err)
				// Don't fail immediately - reconnection errors are expected during disconnect test
			}
		case <-deadline:
			t.Logf("Timeout collecting events: got %d of %d", len(events), count)
			return events
		}
	}

	return events
}

// mustClaudeEventID extracts the ID from a ClaudeEvent, failing if extraction fails.
func mustClaudeEventID(t *testing.T, event *stream.Event) string {
	t.Helper()

	if event.Type != stream.MessageTypeClaudeEvent {
		t.Fatalf("expected claude_event, got %s", event.Type)
	}

	data, err := event.ClaudeEventData()
	require.NoError(t, err)
	return data.ID
}
