// Package stream provides shared types and utilities for durable stream
// communication between wisp-sprite (on the Sprite VM) and clients (TUI/web).
package stream

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileStore provides file-based persistent storage for stream events.
// It is designed to run on the Sprite VM and provides durability across
// disconnections. Events are stored as newline-delimited JSON (NDJSON)
// with sequence numbers assigned on append.
type FileStore struct {
	// path is the path to the stream file
	path string

	// mu protects concurrent access to the file and sequence counter
	mu sync.Mutex

	// nextSeq is the next sequence number to assign
	nextSeq uint64

	// file is the open file handle for appending
	file *os.File
}

// NewFileStore creates a new FileStore at the given path.
// If the file exists, it reads existing events to determine the next sequence number.
// If the file doesn't exist, it will be created on first Append.
func NewFileStore(path string) (*FileStore, error) {
	fs := &FileStore{
		path:    path,
		nextSeq: 1, // Sequence numbers start at 1
	}

	// If file exists, scan to find the highest sequence number
	if _, err := os.Stat(path); err == nil {
		maxSeq, err := fs.scanMaxSequence()
		if err != nil {
			return nil, fmt.Errorf("failed to scan existing events: %w", err)
		}
		fs.nextSeq = maxSeq + 1
	}

	return fs, nil
}

// scanMaxSequence reads the file and returns the highest sequence number found.
// Returns 0 if the file is empty.
func (fs *FileStore) scanMaxSequence() (uint64, error) {
	file, err := os.Open(fs.path)
	if err != nil {
		return 0, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	var maxSeq uint64
	scanner := bufio.NewScanner(file)
	// Increase buffer size for potentially large events
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			// Skip malformed lines but log/continue
			continue
		}
		if event.Seq > maxSeq {
			maxSeq = event.Seq
		}
	}

	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("failed to scan file: %w", err)
	}

	return maxSeq, nil
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

	// Ensure directory exists
	dir := filepath.Dir(fs.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Open file for appending (create if not exists)
	if fs.file == nil {
		file, err := os.OpenFile(fs.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("failed to open file: %w", err)
		}
		fs.file = file
	}

	// Write event as a single line with newline
	if _, err := fs.file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("failed to write event: %w", err)
	}

	// Sync to ensure durability
	if err := fs.file.Sync(); err != nil {
		return fmt.Errorf("failed to sync file: %w", err)
	}

	fs.nextSeq++
	return nil
}

// Read reads events starting from the given sequence number (inclusive).
// Returns all events with Seq >= fromSeq.
// If fromSeq is 0, all events are returned.
func (fs *FileStore) Read(fromSeq uint64) ([]*Event, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	// If file doesn't exist, return empty slice
	if _, err := os.Stat(fs.path); os.IsNotExist(err) {
		return []*Event{}, nil
	}

	file, err := os.Open(fs.path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	var events []*Event
	scanner := bufio.NewScanner(file)
	// Increase buffer size for potentially large events
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			// Skip malformed lines
			continue
		}

		if event.Seq >= fromSeq {
			events = append(events, &event)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan file: %w", err)
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
			case <-ticker.C:
				events, err := fs.Read(nextSeq)
				if err != nil {
					// Log error but continue polling
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
func (fs *FileStore) Close() error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if fs.file != nil {
		err := fs.file.Close()
		fs.file = nil
		return err
	}
	return nil
}

// Path returns the path to the stream file.
func (fs *FileStore) Path() string {
	return fs.path
}
