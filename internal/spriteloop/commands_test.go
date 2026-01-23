package spriteloop

import (
	"context"
	"testing"
	"time"

	"github.com/thruflo/wisp/internal/stream"
)

func TestNewCommandProcessor(t *testing.T) {
	cmdCh := make(chan *stream.Command, 10)
	inputCh := make(chan string, 1)

	cp := NewCommandProcessor(CommandProcessorOptions{
		CommandCh: cmdCh,
		InputCh:   inputCh,
	})

	if cp == nil {
		t.Fatal("Expected non-nil CommandProcessor")
	}
	if cp.pendingInputs == nil {
		t.Error("Expected pendingInputs map to be initialized")
	}
}

func TestCommandProcessorKillCommand(t *testing.T) {
	tmpDir := t.TempDir()
	fs, err := stream.NewFileStore(tmpDir + "/stream.ndjson")
	if err != nil {
		t.Fatalf("Failed to create FileStore: %v", err)
	}
	defer fs.Close()

	cmdCh := make(chan *stream.Command, 10)
	inputCh := make(chan string, 1)

	cp := NewCommandProcessor(CommandProcessorOptions{
		FileStore: fs,
		CommandCh: cmdCh,
		InputCh:   inputCh,
	})

	// Create a kill command
	cmd, err := stream.NewKillCommand("cmd-1", false)
	if err != nil {
		t.Fatalf("Failed to create kill command: %v", err)
	}

	// Process the command
	err = cp.ProcessCommand(cmd)
	if err != nil {
		t.Errorf("ProcessCommand returned error: %v", err)
	}

	// Check that command was sent to channel
	select {
	case received := <-cmdCh:
		if received.ID != "cmd-1" {
			t.Errorf("Expected command ID 'cmd-1', got %q", received.ID)
		}
		if received.Type != stream.CommandTypeKill {
			t.Errorf("Expected command type Kill, got %v", received.Type)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected command to be sent to channel")
	}
}

func TestCommandProcessorBackgroundCommand(t *testing.T) {
	tmpDir := t.TempDir()
	fs, err := stream.NewFileStore(tmpDir + "/stream.ndjson")
	if err != nil {
		t.Fatalf("Failed to create FileStore: %v", err)
	}
	defer fs.Close()

	cmdCh := make(chan *stream.Command, 10)
	inputCh := make(chan string, 1)

	cp := NewCommandProcessor(CommandProcessorOptions{
		FileStore: fs,
		CommandCh: cmdCh,
		InputCh:   inputCh,
	})

	// Create a background command
	cmd := stream.NewBackgroundCommand("cmd-2")

	// Process the command
	err = cp.ProcessCommand(cmd)
	if err != nil {
		t.Errorf("ProcessCommand returned error: %v", err)
	}

	// Check that command was sent to channel
	select {
	case received := <-cmdCh:
		if received.ID != "cmd-2" {
			t.Errorf("Expected command ID 'cmd-2', got %q", received.ID)
		}
		if received.Type != stream.CommandTypeBackground {
			t.Errorf("Expected command type Background, got %v", received.Type)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected command to be sent to channel")
	}
}

func TestCommandProcessorInputResponse(t *testing.T) {
	tmpDir := t.TempDir()
	fs, err := stream.NewFileStore(tmpDir + "/stream.ndjson")
	if err != nil {
		t.Fatalf("Failed to create FileStore: %v", err)
	}
	defer fs.Close()

	cmdCh := make(chan *stream.Command, 10)
	inputCh := make(chan string, 1)

	cp := NewCommandProcessor(CommandProcessorOptions{
		FileStore: fs,
		CommandCh: cmdCh,
		InputCh:   inputCh,
	})

	// Register a pending input request
	cp.RegisterInputRequest("req-1")

	// Create an input response (now a separate event type, not a command)
	ir := &stream.InputResponse{
		ID:        "response-1",
		RequestID: "req-1",
		Response:  "user response",
	}

	// Process the input response
	err = cp.ProcessInputResponse(ir)
	if err != nil {
		t.Errorf("ProcessInputResponse returned error: %v", err)
	}

	// Check that response was sent to input channel
	select {
	case received := <-inputCh:
		if received != "user response" {
			t.Errorf("Expected response 'user response', got %q", received)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Expected response to be sent to input channel")
	}

	// Check that ack was published
	events, err := fs.Read(0)
	if err != nil {
		t.Fatalf("Failed to read events: %v", err)
	}

	foundAck := false
	for _, e := range events {
		if e.Type == stream.MessageTypeAck {
			ack, _ := e.AckData()
			if ack.CommandID == "response-1" && ack.Status == stream.AckStatusSuccess {
				foundAck = true
				break
			}
		}
	}
	if !foundAck {
		t.Error("Expected success ack to be published")
	}

	// Verify input request was removed from pending
	cp.mu.Lock()
	if cp.pendingInputs["req-1"] {
		t.Error("Expected input request to be removed from pending")
	}
	cp.mu.Unlock()
}

func TestCommandProcessorUnknownCommand(t *testing.T) {
	tmpDir := t.TempDir()
	fs, err := stream.NewFileStore(tmpDir + "/stream.ndjson")
	if err != nil {
		t.Fatalf("Failed to create FileStore: %v", err)
	}
	defer fs.Close()

	cp := NewCommandProcessor(CommandProcessorOptions{
		FileStore: fs,
	})

	// Create a command with unknown type
	cmd := &stream.Command{
		ID:   "cmd-4",
		Type: stream.CommandType("unknown"),
	}

	// Process the command - should return error
	err = cp.ProcessCommand(cmd)
	if err == nil {
		t.Error("Expected error for unknown command type")
	}

	// Check that error ack was published
	events, err := fs.Read(0)
	if err != nil {
		t.Fatalf("Failed to read events: %v", err)
	}

	foundErrorAck := false
	for _, e := range events {
		if e.Type == stream.MessageTypeAck {
			ack, _ := e.AckData()
			if ack.CommandID == "cmd-4" && ack.Status == stream.AckStatusError {
				foundErrorAck = true
				break
			}
		}
	}
	if !foundErrorAck {
		t.Error("Expected error ack to be published")
	}
}

func TestCommandProcessorRegisterInputRequest(t *testing.T) {
	cp := NewCommandProcessor(CommandProcessorOptions{})

	// Register an input request
	cp.RegisterInputRequest("req-1")

	cp.mu.Lock()
	if !cp.pendingInputs["req-1"] {
		t.Error("Expected input request to be registered")
	}
	cp.mu.Unlock()

	// Unregister
	cp.UnregisterInputRequest("req-1")

	cp.mu.Lock()
	if cp.pendingInputs["req-1"] {
		t.Error("Expected input request to be unregistered")
	}
	cp.mu.Unlock()
}

func TestCommandProcessorRunWithCancelledContext(t *testing.T) {
	tmpDir := t.TempDir()
	fs, err := stream.NewFileStore(tmpDir + "/stream.ndjson")
	if err != nil {
		t.Fatalf("Failed to create FileStore: %v", err)
	}
	defer fs.Close()

	cp := NewCommandProcessor(CommandProcessorOptions{
		FileStore: fs,
	})

	// Cancel context immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Run should return immediately with context error
	err = cp.Run(ctx)
	if err != context.Canceled {
		t.Errorf("Expected context.Canceled, got %v", err)
	}
}

func TestCommandProcessorRunWithNilFileStore(t *testing.T) {
	cp := NewCommandProcessor(CommandProcessorOptions{})

	err := cp.Run(context.Background())
	if err == nil {
		t.Error("Expected error for nil FileStore")
	}
}

func TestCommandProcessorRunProcessesCommands(t *testing.T) {
	tmpDir := t.TempDir()
	fs, err := stream.NewFileStore(tmpDir + "/stream.ndjson")
	if err != nil {
		t.Fatalf("Failed to create FileStore: %v", err)
	}
	defer fs.Close()

	cmdCh := make(chan *stream.Command, 10)

	cp := NewCommandProcessor(CommandProcessorOptions{
		FileStore: fs,
		CommandCh: cmdCh,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start processor in background
	processorDone := make(chan error, 1)
	go func() {
		processorDone <- cp.Run(ctx)
	}()

	// Give it a moment to start
	time.Sleep(50 * time.Millisecond)

	// Write a command to the stream
	cmd, _ := stream.NewKillCommand("stream-cmd-1", false)
	cmdEvent, _ := stream.NewCommandEvent(cmd)
	if err := fs.Append(cmdEvent); err != nil {
		t.Fatalf("Failed to append command: %v", err)
	}

	// Wait for command to be processed
	select {
	case received := <-cmdCh:
		if received.ID != "stream-cmd-1" {
			t.Errorf("Expected command ID 'stream-cmd-1', got %q", received.ID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Expected command to be received")
	}

	// Cancel and wait for processor to finish
	cancel()
	select {
	case err := <-processorDone:
		if err != context.Canceled {
			t.Errorf("Expected context.Canceled, got %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Processor did not stop")
	}
}

func TestCommandProcessorLastProcessedSeq(t *testing.T) {
	cp := NewCommandProcessor(CommandProcessorOptions{})

	if cp.GetLastProcessedSeq() != 0 {
		t.Error("Expected initial last processed seq to be 0")
	}

	cp.SetLastProcessedSeq(42)
	if cp.GetLastProcessedSeq() != 42 {
		t.Errorf("Expected last processed seq to be 42, got %d", cp.GetLastProcessedSeq())
	}
}

func TestCommandProcessorInputResponseNoChannelReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	fs, err := stream.NewFileStore(tmpDir + "/stream.ndjson")
	if err != nil {
		t.Fatalf("Failed to create FileStore: %v", err)
	}
	defer fs.Close()

	cmdCh := make(chan *stream.Command, 10)
	// No input channel - should return error
	cp := NewCommandProcessor(CommandProcessorOptions{
		FileStore: fs,
		CommandCh: cmdCh,
		InputCh:   nil,
	})

	// Create an input response (now a separate event type)
	ir := &stream.InputResponse{
		ID:        "response-1",
		RequestID: "req-2",
		Response:  "response",
	}

	// Process the input response - should fail with no input channel
	err = cp.ProcessInputResponse(ir)
	if err == nil {
		t.Error("Expected error when no input channel is available")
	}
}

func TestCommandProcessorChannelFull(t *testing.T) {
	tmpDir := t.TempDir()
	fs, err := stream.NewFileStore(tmpDir + "/stream.ndjson")
	if err != nil {
		t.Fatalf("Failed to create FileStore: %v", err)
	}
	defer fs.Close()

	// Create a full channel (buffered with 0)
	cmdCh := make(chan *stream.Command) // Unbuffered, will block

	cp := NewCommandProcessor(CommandProcessorOptions{
		FileStore: fs,
		CommandCh: cmdCh,
	})

	// Create a kill command
	cmd, _ := stream.NewKillCommand("cmd-6", false)

	// Process the command - should fail because channel is full
	err = cp.ProcessCommand(cmd)
	if err == nil {
		t.Error("Expected error for full channel")
	}

	// Check that error ack was published
	events, err := fs.Read(0)
	if err != nil {
		t.Fatalf("Failed to read events: %v", err)
	}

	foundErrorAck := false
	for _, e := range events {
		if e.Type == stream.MessageTypeAck {
			ack, _ := e.AckData()
			if ack.CommandID == "cmd-6" && ack.Status == stream.AckStatusError {
				foundErrorAck = true
				break
			}
		}
	}
	if !foundErrorAck {
		t.Error("Expected error ack to be published for full channel")
	}
}
