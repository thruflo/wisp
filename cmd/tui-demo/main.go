// tui-demo is a manual test program for verifying TUI view rendering.
// Run with: go run ./cmd/tui-demo
//
// This displays each view in sequence, demonstrating:
// - Summary view with various states
// - Tail view with streaming content
// - Input view with question prompt
//
// Press keys to interact with each view.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/thruflo/wisp/internal/tui"
)

func main() {
	fmt.Println("TUI Demo - View Rendering Test")
	fmt.Println("===============================")
	fmt.Println()
	fmt.Println("This demo will test the TUI views. You will see:")
	fmt.Println("1. Summary view (press 't' for tail, 'esc' to exit)")
	fmt.Println("2. Tail view (press 't' or 'd' to return)")
	fmt.Println("3. Input view (type response, press Enter to submit)")
	fmt.Println()
	fmt.Println("Press Enter to start...")
	fmt.Scanln()

	// Run the interactive demo
	if err := runDemo(); err != nil {
		fmt.Fprintf(os.Stderr, "Demo error: %v\n", err)
		os.Exit(1)
	}
}

func runDemo() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create TUI
	ui := tui.NewTUI(os.Stdout)

	// Set initial state
	ui.SetState(tui.ViewState{
		Branch:         "feat-user-auth",
		Iteration:      5,
		MaxIterations:  50,
		Status:         "CONTINUE",
		CompletedTasks: 3,
		TotalTasks:     7,
		LastSummary:    "Implemented JWT token validation",
	})

	// Add some lines to tail view
	ui.AppendTailLine("=== Claude Code Output ===")
	ui.AppendTailLine("")
	ui.AppendTailLine("Reading tasks.json...")
	ui.AppendTailLine("Found 7 tasks, 3 completed")
	ui.AppendTailLine("")
	ui.AppendTailLine("Starting task: Implement password hashing")
	ui.AppendTailLine("  - Analyzing existing auth code...")
	ui.AppendTailLine("  - Using bcrypt for password hashing")
	ui.AppendTailLine("  - Writing hashPassword function")
	ui.AppendTailLine("  - Writing verifyPassword function")
	ui.AppendTailLine("")
	ui.AppendTailLine("Running tests...")
	ui.AppendTailLine("  PASS: TestHashPassword")
	ui.AppendTailLine("  PASS: TestVerifyPassword")
	ui.AppendTailLine("  PASS: TestInvalidPassword")
	ui.AppendTailLine("")
	ui.AppendTailLine("All tests passed!")

	// Start the TUI in a goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- ui.Run(ctx)
	}()

	// Handle actions
	go func() {
		for action := range ui.Actions() {
			switch action.Action {
			case tui.ActionAttach:
				// Simulate attach by showing a message then returning
				ui.AppendTailLine("")
				ui.AppendTailLine(">>> Attach requested (would shell into Sprite)")
				ui.SetView(tui.ViewTail)

			case tui.ActionKill:
				// Show confirmation then exit
				ui.AppendTailLine("")
				ui.AppendTailLine(">>> Kill requested - exiting demo")
				cancel()

			case tui.ActionBackground:
				cancel()

			case tui.ActionQuit:
				cancel()

			case tui.ActionSubmitInput:
				ui.AppendTailLine("")
				ui.AppendTailLine(fmt.Sprintf(">>> Input received: %q", action.Input))
				// Simulate continuing after input
				state := ui.GetState()
				state.Status = "CONTINUE"
				state.Question = ""
				ui.SetState(state)
				ui.SetView(tui.ViewSummary)

			case tui.ActionCancelInput:
				ui.AppendTailLine("")
				ui.AppendTailLine(">>> Input cancelled")
			}
		}
	}()

	// Simulate state changes in background
	go func() {
		// Wait for TUI to start
		time.Sleep(2 * time.Second)

		// Simulate iteration progress
		for i := 6; i <= 7; i++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
				state := ui.GetState()
				state.Iteration = i
				state.CompletedTasks = i - 2
				state.LastSummary = fmt.Sprintf("Completed task %d", i-1)
				ui.SetState(state)

				ui.AppendTailLine("")
				ui.AppendTailLine(fmt.Sprintf(">>> Iteration %d complete", i))
			}
		}

		// After a few iterations, show NEEDS_INPUT
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
			state := ui.GetState()
			state.Status = "NEEDS_INPUT"
			state.Question = "The API spec is ambiguous. Should user sessions expire after 24 hours or 7 days?"
			ui.SetState(state)
			ui.ShowInput(state.Question)
			ui.Bell()
		}
	}()

	// Wait for TUI to exit
	return <-errCh
}
