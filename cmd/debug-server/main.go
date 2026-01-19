// Simple standalone server for debugging auth flow.
// Run with: go run ./cmd/debug-server
// Make sure to rebuild web assets first: cd web && npm run build
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/thruflo/wisp/internal/auth"
	"github.com/thruflo/wisp/internal/server"
	"github.com/thruflo/wisp/web"
)

func main() {
	password := "test123"
	if len(os.Args) > 1 {
		password = os.Args[1]
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to hash password: %v\n", err)
		os.Exit(1)
	}

	// Check if web/dist exists (fresh build)
	if stat, err := os.Stat("./web/dist"); err != nil || !stat.IsDir() {
		fmt.Fprintln(os.Stderr, "Warning: ./web/dist not found. Run 'cd web && npm run build' first.")
		fmt.Fprintln(os.Stderr, "Using embedded assets (may be stale).")
	} else {
		fmt.Println("Using live assets from ./web/dist")
	}

	srv, err := server.NewServer(&server.Config{
		Port:         8375,
		PasswordHash: hash,
		Assets:       web.GetAssets("./web/dist"),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create server: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle Ctrl+C
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nShutting down...")
		cancel()
	}()

	go srv.Start(ctx)

	fmt.Printf("Server running on http://localhost:%d\n", srv.Port())
	fmt.Printf("Password: %s\n", password)
	fmt.Println("\nTest with:")
	fmt.Printf("  curl -X POST http://localhost:%d/auth -H 'Content-Type: application/json' -d '{\"password\":\"%s\"}'\n", srv.Port(), password)

	<-ctx.Done()
	srv.Stop()
}
