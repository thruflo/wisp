package spriteloop

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/thruflo/wisp/internal/stream"
)

// CommandProcessor handles incoming commands from the stream and delivers
// them to the loop for processing. It subscribes to the stream for command
// events and sends acknowledgments after processing.
//
// Input state is tracked via the stream (State Protocol bidirectional sync):
// - input_request events mark pending inputs
// - input_response events mark completed inputs
// This replaces the previous in-memory pendingInputs map.
type CommandProcessor struct {
	// fileStore is used to subscribe to command events and publish acks
	fileStore *stream.FileStore

	// commandCh is the channel to deliver commands to the loop
	commandCh chan<- *stream.Command

	// inputCh is the channel to deliver user input responses
	inputCh chan<- string

	// lastProcessedSeq tracks the last processed command sequence
	lastProcessedSeq uint64
}

// CommandProcessorOptions holds configuration for creating a CommandProcessor.
type CommandProcessorOptions struct {
	FileStore *stream.FileStore
	CommandCh chan<- *stream.Command
	InputCh   chan<- string
}

// NewCommandProcessor creates a new CommandProcessor with the given options.
func NewCommandProcessor(opts CommandProcessorOptions) *CommandProcessor {
	return &CommandProcessor{
		fileStore: opts.FileStore,
		commandCh: opts.CommandCh,
		inputCh:   opts.InputCh,
	}
}

// Run starts the command processor and listens for commands from the stream.
// It blocks until the context is canceled.
func (cp *CommandProcessor) Run(ctx context.Context) error {
	if cp.fileStore == nil {
		return errors.New("fileStore is required")
	}

	// Subscribe to the stream starting from the last processed sequence
	eventCh, err := cp.fileStore.Subscribe(ctx, cp.lastProcessedSeq+1, 100*time.Millisecond)
	if err != nil {
		return fmt.Errorf("failed to subscribe to stream: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-eventCh:
			if !ok {
				// Channel closed, context must have been canceled
				return ctx.Err()
			}

			// Update last processed sequence
			cp.lastProcessedSeq = event.Seq

			// Process command or input_response events
			switch event.Type {
			case stream.MessageTypeCommand:
				if err := cp.processCommandEvent(event); err != nil {
					// Log error but continue processing
					continue
				}
			case stream.MessageTypeInputResponse:
				if err := cp.processInputResponseEvent(event); err != nil {
					// Log error but continue processing
					continue
				}
			}
		}
	}
}

// processCommandEvent processes a command event from the stream.
func (cp *CommandProcessor) processCommandEvent(event *stream.Event) error {
	cmd, err := event.CommandData()
	if err != nil {
		return fmt.Errorf("failed to unmarshal command data: %w", err)
	}

	return cp.ProcessCommand(cmd)
}

// ProcessCommand processes a single command and sends the appropriate
// response. This can be called directly for commands received via HTTP
// rather than through stream subscription.
func (cp *CommandProcessor) ProcessCommand(cmd *stream.Command) error {
	switch cmd.Type {
	case stream.CommandTypeKill:
		return cp.handleKill(cmd)
	case stream.CommandTypeBackground:
		return cp.handleBackground(cmd)
	default:
		cp.publishAck(cmd.ID, fmt.Errorf("unknown command type: %s", cmd.Type))
		return fmt.Errorf("unknown command type: %s", cmd.Type)
	}
}

// processInputResponseEvent processes an input_response event from the stream.
func (cp *CommandProcessor) processInputResponseEvent(event *stream.Event) error {
	ir, err := event.InputResponseData()
	if err != nil {
		return fmt.Errorf("failed to unmarshal input response data: %w", err)
	}

	return cp.ProcessInputResponse(ir)
}

// ProcessInputResponse processes a single input response. This can be called
// directly for responses received via HTTP rather than through stream subscription.
//
// Per the State Protocol bidirectional sync pattern:
// - Input state is validated by checking the stream for pending input_request events
// - The response is forwarded to the loop via inputCh if available
// - The response is already durably stored in the stream (appended by the client)
func (cp *CommandProcessor) ProcessInputResponse(ir *stream.InputResponse) error {
	// Per State Protocol: validate by checking stream for pending input_request.
	// The presence of an input_request without a matching input_response indicates pending.
	// This replaces the previous in-memory pendingInputs map.
	if cp.fileStore != nil {
		isPending := cp.isInputRequestPending(ir.RequestID)
		if !isPending {
			// Input request not found or already answered
			// Still forward it - the loop will validate and handle appropriately
		}
	}

	// Try to send to inputCh (direct path for NEEDS_INPUT)
	if cp.inputCh != nil {
		select {
		case cp.inputCh <- ir.Response:
			cp.publishAck(ir.ID, nil)
			return nil
		default:
			// Channel full
			cp.publishAck(ir.ID, errors.New("input channel full"))
			return errors.New("input channel full")
		}
	}

	cp.publishAck(ir.ID, errors.New("no channel available for input response"))
	return errors.New("no channel available for input response")
}

// isInputRequestPending checks if an input request is pending by examining the stream.
// Per the State Protocol: presence of input_request without matching input_response = pending.
func (cp *CommandProcessor) isInputRequestPending(requestID string) bool {
	if cp.fileStore == nil {
		return false
	}

	events, err := cp.fileStore.Read(0)
	if err != nil {
		return false
	}

	hasRequest := false
	hasResponse := false

	for _, event := range events {
		switch event.Type {
		case stream.MessageTypeInputRequest:
			ir, err := event.InputRequestData()
			if err == nil && ir.ID == requestID {
				hasRequest = true
			}
		case stream.MessageTypeInputResponse:
			ir, err := event.InputResponseData()
			if err == nil && ir.RequestID == requestID {
				hasResponse = true
			}
		}
	}

	return hasRequest && !hasResponse
}

// handleKill processes a kill command to stop the loop.
func (cp *CommandProcessor) handleKill(cmd *stream.Command) error {
	// Send command to loop
	if cp.commandCh != nil {
		select {
		case cp.commandCh <- cmd:
		default:
			// Channel full or closed, send error ack
			cp.publishAck(cmd.ID, errors.New("command channel full"))
			return errors.New("command channel full")
		}
	}

	// Ack is sent by the loop after processing
	return nil
}

// handleBackground processes a background command to pause the loop.
func (cp *CommandProcessor) handleBackground(cmd *stream.Command) error {
	// Send command to loop
	if cp.commandCh != nil {
		select {
		case cp.commandCh <- cmd:
		default:
			// Channel full or closed, send error ack
			cp.publishAck(cmd.ID, errors.New("command channel full"))
			return errors.New("command channel full")
		}
	}

	// Ack is sent by the loop after processing
	return nil
}


// RegisterInputRequest is deprecated - input state is now tracked via stream events.
// This is a no-op kept for backward compatibility.
// Per State Protocol: input_request events in the stream mark pending inputs.
func (cp *CommandProcessor) RegisterInputRequest(requestID string) {
	// No-op: input state is tracked via stream events (State Protocol bidirectional sync)
}

// UnregisterInputRequest is deprecated - input state is now tracked via stream events.
// This is a no-op kept for backward compatibility.
// Per State Protocol: input_response events in the stream mark completed inputs.
func (cp *CommandProcessor) UnregisterInputRequest(requestID string) {
	// No-op: input state is tracked via stream events (State Protocol bidirectional sync)
}

// publishAck publishes an acknowledgment event to the stream.
func (cp *CommandProcessor) publishAck(commandID string, err error) {
	if cp.fileStore == nil {
		return
	}

	var ack *stream.Ack
	if err != nil {
		ack = stream.NewErrorAck(commandID, err)
	} else {
		ack = stream.NewSuccessAck(commandID)
	}

	event, eventErr := stream.NewAckEvent(ack)
	if eventErr != nil {
		return
	}

	cp.fileStore.Append(event)
}

// SetLastProcessedSeq sets the last processed sequence number.
// This is useful when resuming from a known position.
func (cp *CommandProcessor) SetLastProcessedSeq(seq uint64) {
	cp.lastProcessedSeq = seq
}

// GetLastProcessedSeq returns the last processed sequence number.
func (cp *CommandProcessor) GetLastProcessedSeq() uint64 {
	return cp.lastProcessedSeq
}
