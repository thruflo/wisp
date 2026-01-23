package spriteloop

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/thruflo/wisp/internal/stream"
)

// CommandProcessor handles incoming commands from the stream and delivers
// them to the loop for processing. It subscribes to the stream for command
// events and sends acknowledgments after processing.
type CommandProcessor struct {
	// fileStore is used to subscribe to command events and publish acks
	fileStore *stream.FileStore

	// commandCh is the channel to deliver commands to the loop
	commandCh chan<- *stream.Command

	// inputCh is the channel to deliver user input responses
	inputCh chan<- string

	// mu protects pendingInputs
	mu sync.Mutex

	// pendingInputs tracks pending input requests by ID
	pendingInputs map[string]bool

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
		fileStore:     opts.FileStore,
		commandCh:     opts.CommandCh,
		inputCh:       opts.InputCh,
		pendingInputs: make(map[string]bool),
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
func (cp *CommandProcessor) ProcessInputResponse(ir *stream.InputResponse) error {
	// Check if this input request is pending
	cp.mu.Lock()
	isPending := cp.pendingInputs[ir.RequestID]
	if isPending {
		delete(cp.pendingInputs, ir.RequestID)
	}
	cp.mu.Unlock()

	if !isPending {
		// Input request not found - might have been answered already or timed out
		// Still forward it - the loop will validate
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


// RegisterInputRequest registers an input request as pending.
// This allows the CommandProcessor to track which input requests are valid.
func (cp *CommandProcessor) RegisterInputRequest(requestID string) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	cp.pendingInputs[requestID] = true
}

// UnregisterInputRequest removes an input request from the pending list.
func (cp *CommandProcessor) UnregisterInputRequest(requestID string) {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	delete(cp.pendingInputs, requestID)
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
