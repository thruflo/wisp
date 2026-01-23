// Package stream provides shared types and utilities for durable stream
// communication between wisp-sprite (on the Sprite VM) and clients (TUI/web).
//
// This client implements the durable-streams HTTP protocol for communicating
// with a durable-streams server. See: https://github.com/durable-streams/durable-streams
package stream

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultStreamPath is the default path for the wisp event stream.
	DefaultStreamPath = "/wisp/events"

	// HeaderStreamNextOffset is the header containing the next offset after a read.
	HeaderStreamNextOffset = "Stream-Next-Offset"

	// HeaderStreamUpToDate indicates the client is at the tail of the stream.
	HeaderStreamUpToDate = "Stream-Up-To-Date"

	// HeaderStreamCursor is used for CDN cache collision prevention.
	HeaderStreamCursor = "Stream-Cursor"
)

// StreamClient provides HTTP-based access to a durable-streams server.
// It handles connection, subscription, command sending, and automatic reconnection
// with catch-up on missed events.
//
// This client uses the durable-streams HTTP protocol:
// - GET /{path}?offset=X&live=sse for SSE streaming
// - POST /{path} to append events
// - HEAD /{path} for metadata
type StreamClient struct {
	// baseURL is the base URL of the stream server (e.g., "http://localhost:8374")
	baseURL string

	// streamPath is the path to the stream (e.g., "/wisp/events")
	streamPath string

	// httpClient is the HTTP client used for requests
	httpClient *http.Client

	// authToken is the optional authentication token
	authToken string

	// lastOffset is the offset string of the last event received
	lastOffset string

	// lastSeq is a synthetic sequence number for backward compatibility
	// Derived from parsing events in the stream
	lastSeq uint64

	// mu protects lastOffset and lastSeq
	mu sync.RWMutex

	// reconnectInterval is the time to wait between reconnection attempts
	reconnectInterval time.Duration

	// maxReconnectAttempts is the maximum number of reconnection attempts (0 = unlimited)
	maxReconnectAttempts int

	// cursor is used for CDN cache collision prevention in SSE mode
	cursor string
}

// ClientOption configures a StreamClient.
type ClientOption func(*StreamClient)

// WithAuthToken sets the authentication token for the client.
func WithAuthToken(token string) ClientOption {
	return func(c *StreamClient) {
		c.authToken = token
	}
}

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *StreamClient) {
		c.httpClient = client
	}
}

// WithReconnectInterval sets the interval between reconnection attempts.
func WithReconnectInterval(interval time.Duration) ClientOption {
	return func(c *StreamClient) {
		c.reconnectInterval = interval
	}
}

// WithMaxReconnectAttempts sets the maximum number of reconnection attempts.
// Set to 0 for unlimited attempts.
func WithMaxReconnectAttempts(attempts int) ClientOption {
	return func(c *StreamClient) {
		c.maxReconnectAttempts = attempts
	}
}

// WithStreamPath sets a custom stream path (default: "/wisp/events").
func WithStreamPath(path string) ClientOption {
	return func(c *StreamClient) {
		c.streamPath = path
	}
}

// NewStreamClient creates a new StreamClient for the given base URL.
func NewStreamClient(baseURL string, opts ...ClientOption) *StreamClient {
	c := &StreamClient{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		streamPath: DefaultStreamPath,
		httpClient: &http.Client{
			Timeout: 0, // No timeout for streaming connections
		},
		reconnectInterval:    5 * time.Second,
		maxReconnectAttempts: 0, // Unlimited by default
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Connect tests the connection to the stream server by fetching stream metadata.
// Returns an error if the server is not reachable or the stream doesn't exist.
func (c *StreamClient) Connect(ctx context.Context) error {
	// Use HEAD request to check if stream exists per durable-streams protocol
	req, err := http.NewRequestWithContext(ctx, "HEAD", c.baseURL+c.streamPath, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	c.addAuthHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("stream not found at %s", c.streamPath)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned status %d: %s", resp.StatusCode, string(body))
	}

	// Store the current offset from header
	if offset := resp.Header.Get(HeaderStreamNextOffset); offset != "" {
		c.mu.Lock()
		c.lastOffset = offset
		c.mu.Unlock()
	}

	return nil
}

// Subscribe returns a channel that receives events from the stream server.
// It uses Server-Sent Events (SSE) for real-time streaming and automatically
// reconnects and catches up on missed events if the connection is lost.
// The channel is closed when the context is canceled or max reconnect attempts are exceeded.
//
// fromSeq is maintained for backward compatibility. Internally we track offsets.
// If fromSeq is 0, we start from the beginning (offset "0_0").
func (c *StreamClient) Subscribe(ctx context.Context, fromSeq uint64) (<-chan *Event, <-chan error) {
	eventCh := make(chan *Event, 100)
	errCh := make(chan error, 1)

	c.mu.Lock()
	if fromSeq > 0 {
		c.lastSeq = fromSeq - 1
	} else {
		c.lastSeq = 0
		c.lastOffset = "0_0" // Start from beginning
	}
	c.mu.Unlock()

	go c.subscriptionLoop(ctx, eventCh, errCh)

	return eventCh, errCh
}

// subscriptionLoop handles the main subscription loop with reconnection logic.
func (c *StreamClient) subscriptionLoop(ctx context.Context, eventCh chan<- *Event, errCh chan<- error) {
	defer close(eventCh)
	defer close(errCh)

	attempts := 0

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		c.mu.RLock()
		fromOffset := c.lastOffset
		cursor := c.cursor
		c.mu.RUnlock()

		if fromOffset == "" {
			fromOffset = "0_0"
		}

		err := c.streamEvents(ctx, fromOffset, cursor, eventCh)
		if err == nil {
			// Stream ended normally (context canceled)
			return
		}

		// Check if context was canceled
		if ctx.Err() != nil {
			return
		}

		attempts++
		if c.maxReconnectAttempts > 0 && attempts >= c.maxReconnectAttempts {
			errCh <- fmt.Errorf("max reconnection attempts (%d) exceeded: %w", c.maxReconnectAttempts, err)
			return
		}

		// Wait before reconnecting
		select {
		case <-ctx.Done():
			return
		case <-time.After(c.reconnectInterval):
			// Continue to reconnect
		}
	}
}

// streamEvents connects to the SSE endpoint and streams events.
// Uses durable-streams protocol: GET /{path}?offset=X&live=sse
// Returns nil if the context is canceled, or an error if the connection fails.
func (c *StreamClient) streamEvents(ctx context.Context, fromOffset string, cursor string, eventCh chan<- *Event) error {
	// Build URL with durable-streams SSE parameters
	url := fmt.Sprintf("%s%s?offset=%s&live=sse", c.baseURL, c.streamPath, fromOffset)
	if cursor != "" {
		url += "&cursor=" + cursor
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	c.addAuthHeader(req)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned status %d: %s", resp.StatusCode, string(body))
	}

	return c.parseSSEStream(ctx, resp.Body, eventCh)
}

// parseSSEStream parses Server-Sent Events from the response body.
// durable-streams SSE format:
// - event: data - contains the actual data payload
// - event: control - contains metadata like streamNextOffset, streamCursor, upToDate
func (c *StreamClient) parseSSEStream(ctx context.Context, body io.Reader, eventCh chan<- *Event) error {
	scanner := bufio.NewScanner(body)
	// Increase buffer for potentially large events
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var dataLines []string
	var eventType string
	var seqCounter uint64

	c.mu.RLock()
	seqCounter = c.lastSeq
	c.mu.RUnlock()

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		line := scanner.Text()

		// Empty line signals end of event
		if line == "" {
			if len(dataLines) > 0 {
				data := strings.Join(dataLines, "\n")

				if eventType == "control" {
					// Parse control event to extract offset and cursor
					c.handleControlEvent(data)
				} else if eventType == "data" || eventType == "" {
					// Parse as array of events (durable-streams returns JSON arrays)
					events, err := c.parseDataPayload(data, &seqCounter)
					if err != nil {
						// Skip malformed events but continue
						dataLines = nil
						eventType = ""
						continue
					}

					// Send all parsed events
					for _, event := range events {
						select {
						case <-ctx.Done():
							return nil
						case eventCh <- event:
						}
					}
				}

				dataLines = nil
				eventType = ""
			}
			continue
		}

		// Parse SSE fields
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimPrefix(line, "event:")
		} else if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		} else if strings.HasPrefix(line, "data:") {
			// Handle "data:" without space
			dataLines = append(dataLines, strings.TrimPrefix(line, "data:"))
		}
		// Ignore other SSE fields (id:, retry:) for now
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading stream: %w", err)
	}

	return nil
}

// handleControlEvent processes a durable-streams control event.
func (c *StreamClient) handleControlEvent(data string) {
	var control struct {
		StreamNextOffset string `json:"streamNextOffset"`
		StreamCursor     string `json:"streamCursor"`
		UpToDate         bool   `json:"upToDate"`
	}
	if err := json.Unmarshal([]byte(data), &control); err != nil {
		return
	}

	c.mu.Lock()
	if control.StreamNextOffset != "" {
		c.lastOffset = control.StreamNextOffset
	}
	if control.StreamCursor != "" {
		c.cursor = control.StreamCursor
	}
	c.mu.Unlock()
}

// parseDataPayload parses a data payload which may be a single event or array of events.
func (c *StreamClient) parseDataPayload(data string, seqCounter *uint64) ([]*Event, error) {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" || trimmed == "[]" {
		return nil, nil
	}

	var events []*Event

	// Try parsing as array first (durable-streams format)
	if strings.HasPrefix(trimmed, "[") {
		var rawEvents []json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &rawEvents); err != nil {
			// Fall back to single event parsing
			return c.parseSingleEvent(trimmed, seqCounter)
		}

		for _, raw := range rawEvents {
			event, err := UnmarshalEvent(raw)
			if err != nil {
				continue
			}
			*seqCounter++
			event.Seq = *seqCounter

			c.mu.Lock()
			c.lastSeq = event.Seq
			c.mu.Unlock()

			events = append(events, event)
		}
		return events, nil
	}

	// Single event
	return c.parseSingleEvent(trimmed, seqCounter)
}

// parseSingleEvent parses a single JSON event.
func (c *StreamClient) parseSingleEvent(data string, seqCounter *uint64) ([]*Event, error) {
	event, err := UnmarshalEvent([]byte(data))
	if err != nil {
		return nil, err
	}

	// Assign sequence number if not set
	if event.Seq == 0 {
		*seqCounter++
		event.Seq = *seqCounter
	} else {
		*seqCounter = event.Seq
	}

	c.mu.Lock()
	c.lastSeq = event.Seq
	c.mu.Unlock()

	return []*Event{event}, nil
}

// SendCommand sends a command to the stream server by appending it to the stream.
// Per durable-streams protocol, we POST the event data to append it.
// Returns an acknowledgment or an error if the command fails.
func (c *StreamClient) SendCommand(ctx context.Context, cmd *Command) (*Ack, error) {
	// Create command event using State Protocol format
	event, err := NewCommandEvent(cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to create command event: %w", err)
	}

	data, err := event.Marshal()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal command: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+c.streamPath, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	c.addAuthHeader(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send command: %w", err)
	}
	defer resp.Body.Close()

	// Update offset from response header
	if offset := resp.Header.Get(HeaderStreamNextOffset); offset != "" {
		c.mu.Lock()
		c.lastOffset = offset
		c.mu.Unlock()
	}

	// Durable-streams returns 204 No Content on successful append (non-producer)
	// or 200 OK for producer appends
	if resp.StatusCode == http.StatusNoContent {
		// Success - create synthetic ack
		return &Ack{
			CommandID: cmd.ID,
			Status:    AckStatusSuccess,
		}, nil
	}

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted {
		// Try to parse ack from response body
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response: %w", err)
		}

		if len(body) > 0 {
			var ack Ack
			if err := json.Unmarshal(body, &ack); err == nil {
				return &ack, nil
			}
		}

		// Success without explicit ack
		return &Ack{
			CommandID: cmd.ID,
			Status:    AckStatusSuccess,
		}, nil
	}

	body, _ := io.ReadAll(resp.Body)
	return nil, fmt.Errorf("server returned status %d: %s", resp.StatusCode, string(body))
}

// SendKillCommand sends a kill command to stop the loop.
func (c *StreamClient) SendKillCommand(ctx context.Context, commandID string, deleteSprite bool) (*Ack, error) {
	cmd, err := NewKillCommand(commandID, deleteSprite)
	if err != nil {
		return nil, err
	}
	return c.SendCommand(ctx, cmd)
}

// SendBackgroundCommand sends a background command to pause the loop.
func (c *StreamClient) SendBackgroundCommand(ctx context.Context, commandID string) (*Ack, error) {
	cmd := NewBackgroundCommand(commandID)
	return c.SendCommand(ctx, cmd)
}

// SendInputResponse sends an input response event to the stream server.
// This creates a durable input_response event per the State Protocol pattern.
func (c *StreamClient) SendInputResponse(ctx context.Context, responseID, requestID, response string) (*Ack, error) {
	// Create input response using State Protocol format
	ir := &InputResponse{
		ID:        responseID,
		RequestID: requestID,
		Response:  response,
	}
	event, err := NewInputResponseEvent(ir)
	if err != nil {
		return nil, fmt.Errorf("failed to create input response event: %w", err)
	}

	data, err := event.Marshal()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input response: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+c.streamPath, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	c.addAuthHeader(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send input response: %w", err)
	}
	defer resp.Body.Close()

	// Update offset from response header
	if offset := resp.Header.Get(HeaderStreamNextOffset); offset != "" {
		c.mu.Lock()
		c.lastOffset = offset
		c.mu.Unlock()
	}

	// Durable-streams returns 204 No Content on successful append
	if resp.StatusCode == http.StatusNoContent {
		return &Ack{
			CommandID: responseID,
			Status:    AckStatusSuccess,
		}, nil
	}

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response: %w", err)
		}

		if len(body) > 0 {
			var ack Ack
			if err := json.Unmarshal(body, &ack); err == nil {
				return &ack, nil
			}
		}

		return &Ack{
			CommandID: responseID,
			Status:    AckStatusSuccess,
		}, nil
	}

	body, _ := io.ReadAll(resp.Body)
	return nil, fmt.Errorf("server returned status %d: %s", resp.StatusCode, string(body))
}

// GetState fetches the current state snapshot from the server by reading
// all events from the stream and reconstructing state.
func (c *StreamClient) GetState(ctx context.Context) (*StateSnapshot, error) {
	// Read all events from the beginning to build state
	url := c.baseURL + c.streamPath + "?offset=0_0"

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	c.addAuthHeader(req)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get state: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse next offset to track position
	nextOffset := resp.Header.Get(HeaderStreamNextOffset)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Parse the JSON array of events
	var rawEvents []json.RawMessage
	if err := json.Unmarshal(body, &rawEvents); err != nil {
		// Might be empty or single event
		if string(body) == "[]" || len(body) == 0 {
			return &StateSnapshot{LastSeq: 0}, nil
		}
		return nil, fmt.Errorf("failed to parse events: %w", err)
	}

	// Build state from events
	snapshot := &StateSnapshot{
		Tasks: []*TaskEvent{},
	}

	taskByID := make(map[string]*TaskEvent)
	var seqCounter uint64

	for _, raw := range rawEvents {
		event, err := UnmarshalEvent(raw)
		if err != nil {
			continue
		}

		seqCounter++
		event.Seq = seqCounter

		switch event.Type {
		case MessageTypeSession:
			session, err := event.SessionData()
			if err == nil {
				snapshot.Session = session
			}
		case MessageTypeTask:
			task, err := event.TaskData()
			if err == nil {
				taskByID[task.ID] = task
			}
		case MessageTypeInputRequest:
			input, err := event.InputRequestData()
			if err == nil {
				// Presence means pending (State Protocol)
				snapshot.InputRequest = input
			}
		case MessageTypeInputResponse:
			// Response received - clear pending input
			snapshot.InputRequest = nil
		}
	}

	// Convert tasks map to slice
	for _, task := range taskByID {
		snapshot.Tasks = append(snapshot.Tasks, task)
	}

	snapshot.LastSeq = seqCounter

	// Update our tracking
	c.mu.Lock()
	c.lastSeq = seqCounter
	if nextOffset != "" {
		c.lastOffset = nextOffset
	}
	c.mu.Unlock()

	return snapshot, nil
}

// LastSeq returns the sequence number of the last received event.
func (c *StreamClient) LastSeq() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastSeq
}

// LastOffset returns the offset string of the last received event.
func (c *StreamClient) LastOffset() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastOffset
}

// BaseURL returns the base URL of the stream server.
func (c *StreamClient) BaseURL() string {
	return c.baseURL
}

// StreamPath returns the configured stream path.
func (c *StreamClient) StreamPath() string {
	return c.streamPath
}

// addAuthHeader adds the authorization header if a token is configured.
func (c *StreamClient) addAuthHeader(req *http.Request) {
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}
}

// seqFromOffset extracts a synthetic sequence number from an offset string.
// Offset format: "readseq_byteoffset" - we use byteoffset as a proxy for sequence.
func seqFromOffset(offset string) uint64 {
	parts := strings.Split(offset, "_")
	if len(parts) != 2 {
		return 0
	}
	seq, _ := strconv.ParseUint(parts[1], 10, 64)
	return seq
}

// StateSnapshot represents a point-in-time snapshot of the session state.
// This is constructed by reading and processing all events from the stream.
type StateSnapshot struct {
	// Session contains the current session information.
	Session *SessionEvent `json:"session,omitempty"`

	// Tasks contains all current tasks.
	Tasks []*TaskEvent `json:"tasks,omitempty"`

	// LastSeq is the synthetic sequence number of the last event.
	LastSeq uint64 `json:"last_seq"`

	// InputRequest contains the current pending input request, if any.
	InputRequest *InputRequestEvent `json:"input_request,omitempty"`
}
