// Package stream provides shared types and utilities for durable stream
// communication between wisp-sprite (on the Sprite VM) and clients (TUI/web).
package stream

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// StreamClient provides HTTP-based access to a stream server running on a Sprite.
// It handles connection, subscription, command sending, and automatic reconnection
// with catch-up on missed events.
type StreamClient struct {
	// baseURL is the base URL of the stream server (e.g., "http://localhost:8374")
	baseURL string

	// httpClient is the HTTP client used for requests
	httpClient *http.Client

	// authToken is the optional authentication token
	authToken string

	// lastSeq is the sequence number of the last event received
	lastSeq uint64

	// mu protects lastSeq
	mu sync.RWMutex

	// reconnectInterval is the time to wait between reconnection attempts
	reconnectInterval time.Duration

	// maxReconnectAttempts is the maximum number of reconnection attempts (0 = unlimited)
	maxReconnectAttempts int
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

// NewStreamClient creates a new StreamClient for the given base URL.
func NewStreamClient(baseURL string, opts ...ClientOption) *StreamClient {
	c := &StreamClient{
		baseURL: strings.TrimSuffix(baseURL, "/"),
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

// Connect tests the connection to the stream server by fetching the current state.
// Returns an error if the server is not reachable.
func (c *StreamClient) Connect(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/state", nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	c.addAuthHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// Subscribe returns a channel that receives events from the stream server.
// It uses Server-Sent Events (SSE) for real-time streaming and automatically
// reconnects and catches up on missed events if the connection is lost.
// The channel is closed when the context is canceled or max reconnect attempts are exceeded.
// If fromSeq is 0, all events from the beginning are returned.
func (c *StreamClient) Subscribe(ctx context.Context, fromSeq uint64) (<-chan *Event, <-chan error) {
	eventCh := make(chan *Event, 100)
	errCh := make(chan error, 1)

	c.mu.Lock()
	if fromSeq > 0 {
		c.lastSeq = fromSeq - 1
	} else {
		c.lastSeq = 0
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
		fromSeq := c.lastSeq + 1
		c.mu.RUnlock()

		err := c.streamEvents(ctx, fromSeq, eventCh)
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
// Returns nil if the context is canceled, or an error if the connection fails.
func (c *StreamClient) streamEvents(ctx context.Context, fromSeq uint64, eventCh chan<- *Event) error {
	url := fmt.Sprintf("%s/stream?from_seq=%d", c.baseURL, fromSeq)
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
func (c *StreamClient) parseSSEStream(ctx context.Context, body io.Reader, eventCh chan<- *Event) error {
	scanner := bufio.NewScanner(body)
	// Increase buffer for potentially large events
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var dataLines []string

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
				event, err := UnmarshalEvent([]byte(data))
				if err != nil {
					// Skip malformed events but continue
					dataLines = nil
					continue
				}

				// Update lastSeq before sending
				c.mu.Lock()
				if event.Seq > c.lastSeq {
					c.lastSeq = event.Seq
				}
				c.mu.Unlock()

				select {
				case <-ctx.Done():
					return nil
				case eventCh <- event:
				}

				dataLines = nil
			}
			continue
		}

		// Parse SSE format: "data: {...json...}"
		if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		} else if strings.HasPrefix(line, "data:") {
			// Handle "data:" without space
			dataLines = append(dataLines, strings.TrimPrefix(line, "data:"))
		}
		// Ignore other SSE fields (event:, id:, retry:) for now
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading stream: %w", err)
	}

	return nil
}

// SendCommand sends a command to the stream server and waits for acknowledgment.
// Returns the acknowledgment or an error if the command fails.
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

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/command", bytes.NewReader(data))
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("server returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse acknowledgment
	var ack Ack
	if err := json.Unmarshal(body, &ack); err != nil {
		return nil, fmt.Errorf("failed to parse acknowledgment: %w", err)
	}

	return &ack, nil
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

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/input", bytes.NewReader(data))
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("server returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse acknowledgment
	var ack Ack
	if err := json.Unmarshal(body, &ack); err != nil {
		return nil, fmt.Errorf("failed to parse acknowledgment: %w", err)
	}

	return &ack, nil
}

// GetState fetches the current state snapshot from the server.
func (c *StreamClient) GetState(ctx context.Context) (*StateSnapshot, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/state", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	c.addAuthHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get state: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned status %d: %s", resp.StatusCode, string(body))
	}

	var state StateSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		return nil, fmt.Errorf("failed to decode state: %w", err)
	}

	return &state, nil
}

// LastSeq returns the sequence number of the last received event.
func (c *StreamClient) LastSeq() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastSeq
}

// BaseURL returns the base URL of the stream server.
func (c *StreamClient) BaseURL() string {
	return c.baseURL
}

// addAuthHeader adds the authorization header if a token is configured.
func (c *StreamClient) addAuthHeader(req *http.Request) {
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}
}

// StateSnapshot represents a point-in-time snapshot of the session state.
// This is returned by the /state endpoint.
type StateSnapshot struct {
	// Session contains the current session information.
	Session *SessionEvent `json:"session,omitempty"`

	// Tasks contains all current tasks.
	Tasks []*TaskEvent `json:"tasks,omitempty"`

	// LastSeq is the sequence number of the last event in the stream.
	LastSeq uint64 `json:"last_seq"`

	// InputRequest contains the current pending input request, if any.
	InputRequest *InputRequestEvent `json:"input_request,omitempty"`
}
