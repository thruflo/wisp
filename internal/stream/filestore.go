// Package stream provides shared types and utilities for durable stream
// communication between wisp-sprite (on the Sprite VM) and clients (TUI/web).
package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/durable-streams/durable-streams/packages/caddy-plugin/store"
)

// streamPath is the internal path used for the durable-streams store.
const streamPath = "/wisp/events"

// FileStore provides file-based persistent storage for stream events.
// It wraps the durable-streams FileStore to provide durability across
// disconnections. Events are stored as newline-delimited JSON (NDJSON)
// with sequence numbers assigned on append.
type FileStore struct {
	// path is the original path provided (for backwards compatibility)
	path string

	// store is the underlying durable-streams file store
	store *store.FileStore

	// mu protects concurrent access to the sequence counter and closed state
	mu sync.Mutex

	// nextSeq is the next sequence number to assign (1-based for our API)
	nextSeq uint64

	// closed indicates whether Close has been called
	closed bool

	// longPoll notifies subscribers of new events
	longPoll *longPollManager
}

// longPollManager manages channels waiting for new events.
type longPollManager struct {
	mu      sync.Mutex
	waiters []chan struct{}
}

func (lp *longPollManager) notify() {
	lp.mu.Lock()
	defer lp.mu.Unlock()
	for _, ch := range lp.waiters {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (lp *longPollManager) register(ch chan struct{}) {
	lp.mu.Lock()
	defer lp.mu.Unlock()
	lp.waiters = append(lp.waiters, ch)
}

func (lp *longPollManager) unregister(ch chan struct{}) {
	lp.mu.Lock()
	defer lp.mu.Unlock()
	for i, w := range lp.waiters {
		if w == ch {
			lp.waiters = append(lp.waiters[:i], lp.waiters[i+1:]...)
			break
		}
	}
}

// NewFileStore creates a new FileStore at the given path.
// If the file exists, it reads existing events to determine the next sequence number.
// If the file doesn't exist, it will be created on first Append.
func NewFileStore(path string) (*FileStore, error) {
	// Determine the data directory from the path
	// The path is expected to be something like /var/local/wisp/session/stream.ndjson
	// We'll use the parent directory for the durable-streams data
	dir := filepath.Dir(path)
	dataDir := filepath.Join(dir, ".stream-data")

	// Create the durable-streams file store
	dsStore, err := store.NewFileStore(store.FileStoreConfig{
		DataDir:        dataDir,
		MaxFileHandles: 10,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create durable-streams store: %w", err)
	}

	// Create the stream (idempotent if already exists)
	_, _, err = dsStore.Create(streamPath, store.CreateOptions{
		ContentType: "application/json",
	})
	if err != nil {
		dsStore.Close()
		return nil, fmt.Errorf("failed to create stream: %w", err)
	}

	fs := &FileStore{
		path:     path,
		store:    dsStore,
		nextSeq:  1,
		longPoll: &longPollManager{},
	}

	// Scan existing events to determine next sequence number
	if err := fs.scanMaxSequence(); err != nil {
		dsStore.Close()
		return nil, fmt.Errorf("failed to scan existing events: %w", err)
	}

	return fs, nil
}

// scanMaxSequence reads all events and finds the highest sequence number.
func (fs *FileStore) scanMaxSequence() error {
	messages, _, err := fs.store.Read(streamPath, store.ZeroOffset)
	if err != nil {
		if err == store.ErrStreamNotFound {
			return nil
		}
		return err
	}

	var maxSeq uint64
	for _, msg := range messages {
		var event Event
		if err := json.Unmarshal(msg.Data, &event); err != nil {
			continue // Skip malformed events
		}
		if event.Seq > maxSeq {
			maxSeq = event.Seq
		}
	}

	fs.nextSeq = maxSeq + 1
	return nil
}

// Append writes an event to the stream file with an assigned sequence number.
// The event's Seq field will be updated with the assigned sequence number.
// This operation is atomic with respect to other Append and Read operations.
func (fs *FileStore) Append(event *Event) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Assign sequence number
	event.Seq = fs.nextSeq

	// Serialize event
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	// Append to the durable-streams store
	_, err = fs.store.Append(streamPath, data, store.AppendOptions{})
	if err != nil {
		return fmt.Errorf("failed to append event: %w", err)
	}

	fs.nextSeq++

	// Notify subscribers
	fs.longPoll.notify()

	return nil
}

// Read reads events starting from the given sequence number (inclusive).
// Returns all events with Seq >= fromSeq.
// If fromSeq is 0, all events are returned.
func (fs *FileStore) Read(fromSeq uint64) ([]*Event, error) {
	messages, _, err := fs.store.Read(streamPath, store.ZeroOffset)
	if err != nil {
		if err == store.ErrStreamNotFound {
			return []*Event{}, nil
		}
		return nil, fmt.Errorf("failed to read stream: %w", err)
	}

	var events []*Event
	for _, msg := range messages {
		var event Event
		if err := json.Unmarshal(msg.Data, &event); err != nil {
			continue // Skip malformed events
		}

		if event.Seq >= fromSeq {
			eventCopy := event
			events = append(events, &eventCopy)
		}
	}

	return events, nil
}

// Subscribe returns a channel that receives events as they are written.
// It uses polling internally to check for new events at the specified interval.
// The channel is closed when the context is canceled.
// fromSeq specifies the starting sequence number (inclusive); use 0 for all events.
func (fs *FileStore) Subscribe(ctx context.Context, fromSeq uint64, pollInterval time.Duration) (<-chan *Event, error) {
	ch := make(chan *Event, 100) // Buffer to prevent blocking writers

	go func() {
		defer close(ch)

		nextSeq := fromSeq
		if nextSeq == 0 {
			nextSeq = 1
		}

		// Register for notifications
		notifyCh := make(chan struct{}, 1)
		fs.longPoll.register(notifyCh)
		defer fs.longPoll.unregister(notifyCh)

		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()

		// Do an initial read immediately
		events, err := fs.Read(nextSeq)
		if err == nil {
			for _, event := range events {
				select {
				case <-ctx.Done():
					return
				case ch <- event:
					if event.Seq >= nextSeq {
						nextSeq = event.Seq + 1
					}
				}
			}
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-notifyCh:
				// New data available, read immediately
				events, err := fs.Read(nextSeq)
				if err != nil {
					continue
				}
				for _, event := range events {
					select {
					case <-ctx.Done():
						return
					case ch <- event:
						if event.Seq >= nextSeq {
							nextSeq = event.Seq + 1
						}
					}
				}
			case <-ticker.C:
				events, err := fs.Read(nextSeq)
				if err != nil {
					continue
				}
				for _, event := range events {
					select {
					case <-ctx.Done():
						return
					case ch <- event:
						if event.Seq >= nextSeq {
							nextSeq = event.Seq + 1
						}
					}
				}
			}
		}
	}()

	return ch, nil
}

// LastSeq returns the sequence number of the last event written,
// or 0 if no events have been written.
func (fs *FileStore) LastSeq() uint64 {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if fs.nextSeq <= 1 {
		return 0
	}
	return fs.nextSeq - 1
}

// Close closes the file store and releases resources.
// It is safe to call Close multiple times.
func (fs *FileStore) Close() error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if fs.closed {
		return nil
	}
	fs.closed = true

	return fs.store.Close()
}

// Path returns the path to the stream file.
func (fs *FileStore) Path() string {
	return fs.path
}

// ensureLegacyPath ensures the original path directory exists for backwards compatibility.
// This is a no-op since durable-streams handles its own storage.
func ensureLegacyPath(path string) error {
	dir := filepath.Dir(path)
	return os.MkdirAll(dir, 0755)
}
