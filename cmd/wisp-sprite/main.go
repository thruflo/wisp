// Package main provides the wisp-sprite binary entry point.
//
// wisp-sprite runs on the Sprite VM and executes the Claude Code iteration loop.
// It provides an HTTP server for streaming events and receiving commands from
// TUI and web clients. The loop continues until completion, blockage, or user
// action (kill/background).
//
// Usage:
//
//	wisp-sprite [flags]
//
// Flags:
//
//	-port           HTTP server port (default: 8374)
//	-session-dir    Session files directory (default: /var/local/wisp/session)
//	-work-dir       Working directory for Claude (default: /var/local/wisp/repos)
//	-template-dir   Template directory (default: /var/local/wisp/templates)
//	-token          Bearer token for authentication (optional)
//	-session-id     Session identifier (default: branch name from session-dir)
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/thruflo/wisp/internal/spriteloop"
	"github.com/thruflo/wisp/internal/stream"
)

// Default paths on the Sprite VM.
const (
	defaultPort        = 8374
	defaultSessionDir  = "/var/local/wisp/session"
	defaultRepoPath    = "/var/local/wisp/repos"
	defaultTemplateDir = "/var/local/wisp/templates"
	streamFileName     = "stream.ndjson"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Parse command-line flags
	var (
		port        = flag.Int("port", defaultPort, "HTTP server port")
		sessionDir  = flag.String("session-dir", defaultSessionDir, "Session files directory")
		workDir     = flag.String("work-dir", defaultRepoPath, "Working directory for Claude")
		templateDir = flag.String("template-dir", defaultTemplateDir, "Template files directory")
		token       = flag.String("token", "", "Bearer token for authentication")
		sessionID   = flag.String("session-id", "", "Session identifier")
	)
	flag.Parse()

	// Derive session ID from session directory if not provided
	sid := *sessionID
	if sid == "" {
		// Try to read from session state or use directory name
		sid = filepath.Base(*sessionDir)
		if sid == "session" {
			sid = "default"
		}
	}

	// Validate directories exist
	if err := validateDir(*sessionDir); err != nil {
		return fmt.Errorf("invalid session-dir: %w", err)
	}
	if err := validateDir(*workDir); err != nil {
		return fmt.Errorf("invalid work-dir: %w", err)
	}
	if err := validateDir(*templateDir); err != nil {
		return fmt.Errorf("invalid template-dir: %w", err)
	}

	// Create context with cancellation for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("Received signal %v, shutting down...", sig)
		cancel()
	}()

	// Initialize FileStore for event persistence
	streamPath := filepath.Join(*sessionDir, streamFileName)
	fileStore, err := stream.NewFileStore(streamPath)
	if err != nil {
		return fmt.Errorf("failed to create FileStore: %w", err)
	}
	defer fileStore.Close()

	log.Printf("FileStore initialized at %s (last seq: %d)", streamPath, fileStore.LastSeq())

	// Create command and input channels
	commandCh := make(chan *stream.Command, 10)
	inputCh := make(chan string, 1)

	// Create the Loop
	executor := spriteloop.NewLocalExecutor()
	loop := spriteloop.NewLoop(spriteloop.LoopOptions{
		SessionID:   sid,
		RepoPath:    *workDir,
		SessionDir:  *sessionDir,
		TemplateDir: *templateDir,
		Limits:      spriteloop.DefaultLimits(),
		ClaudeConfig: spriteloop.DefaultClaudeConfig(),
		FileStore:    fileStore,
		Executor:     executor,
		StartTime:    time.Now(),
	})

	// Create the CommandProcessor
	cmdProcessor := spriteloop.NewCommandProcessor(spriteloop.CommandProcessorOptions{
		FileStore: fileStore,
		CommandCh: commandCh,
		InputCh:   inputCh,
	})

	// Create the HTTP Server
	server := spriteloop.NewServer(spriteloop.ServerOptions{
		Port:             *port,
		Token:            *token,
		FileStore:        fileStore,
		CommandProcessor: cmdProcessor,
		Loop:             loop,
	})

	// Start the HTTP server in background
	if err := server.Start(); err != nil {
		return fmt.Errorf("failed to start HTTP server: %w", err)
	}
	log.Printf("HTTP server started on port %d", server.Port())

	// Start command processor in background
	cmdCtx, cmdCancel := context.WithCancel(ctx)
	defer cmdCancel()
	go func() {
		if err := cmdProcessor.Run(cmdCtx); err != nil && err != context.Canceled {
			log.Printf("CommandProcessor error: %v", err)
		}
	}()

	// Route commands from HTTP server to loop
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case cmd := <-commandCh:
				select {
				case loop.CommandCh() <- cmd:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	// Run the main loop
	log.Printf("Starting iteration loop for session %s", sid)
	result := loop.Run(ctx)

	// Graceful shutdown of HTTP server
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := server.Stop(shutdownCtx); err != nil {
		log.Printf("Error stopping HTTP server: %v", err)
	}

	// Log result
	log.Printf("Loop completed: reason=%s, iterations=%d", result.Reason, result.Iterations)
	if result.Error != nil {
		log.Printf("Loop error: %v", result.Error)
	}

	// Return error for crash exit
	if result.Reason == spriteloop.ExitReasonCrash {
		return fmt.Errorf("loop crashed: %w", result.Error)
	}

	return nil
}

// validateDir checks if a directory exists and is accessible.
func validateDir(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("directory does not exist: %s", path)
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", path)
	}
	return nil
}
