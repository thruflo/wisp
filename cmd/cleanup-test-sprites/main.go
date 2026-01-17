// Command cleanup-test-sprites deletes orphaned test sprites.
//
// Test sprites are created with names matching "wisp-test-*" pattern.
// This utility lists and deletes sprites older than a configurable threshold.
//
// Usage:
//
//	cleanup-test-sprites [flags]
//
// Examples:
//
//	# List orphan sprites (dry run)
//	cleanup-test-sprites
//
//	# Delete sprites older than 1 hour
//	cleanup-test-sprites --force
//
//	# Delete sprites older than 30 minutes
//	cleanup-test-sprites --force --max-age 30m
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	sprites "github.com/superfly/sprites-go"
	"github.com/thruflo/wisp/internal/config"
)

const (
	testSpritePrefix = "wisp-test-"
	defaultMaxAge    = 1 * time.Hour
)

func main() {
	var (
		force  bool
		maxAge time.Duration
	)

	flag.BoolVar(&force, "force", false, "Actually delete sprites (default is dry run)")
	flag.DurationVar(&maxAge, "max-age", defaultMaxAge, "Delete sprites older than this duration")
	flag.Parse()

	if err := run(force, maxAge); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(force bool, maxAge time.Duration) error {
	token, err := loadSpriteToken()
	if err != nil {
		return err
	}

	client := sprites.New(token)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// List all sprites with the test prefix
	allSprites, err := client.ListAllSprites(ctx, testSpritePrefix)
	if err != nil {
		return fmt.Errorf("failed to list sprites: %w", err)
	}

	// Filter to sprites older than maxAge
	cutoff := time.Now().Add(-maxAge)
	var toDelete []*sprites.Sprite
	for _, s := range allSprites {
		if s.CreatedAt.Before(cutoff) {
			toDelete = append(toDelete, s)
		}
	}

	if len(toDelete) == 0 {
		fmt.Printf("No orphan sprites found matching %q older than %v\n", testSpritePrefix, maxAge)
		return nil
	}

	fmt.Printf("Found %d orphan sprite(s) matching %q older than %v:\n\n", len(toDelete), testSpritePrefix, maxAge)

	for _, s := range toDelete {
		age := time.Since(s.CreatedAt).Round(time.Second)
		fmt.Printf("  %s (created %v ago)\n", s.Name(), age)
	}
	fmt.Println()

	if !force {
		fmt.Println("Dry run - no sprites deleted. Use --force to delete.")
		return nil
	}

	// Delete sprites
	fmt.Println("Deleting sprites...")
	var errs []string
	deleted := 0

	for _, s := range toDelete {
		deleteCtx, deleteCancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := client.DeleteSprite(deleteCtx, s.Name())
		deleteCancel()

		if err != nil {
			errs = append(errs, fmt.Sprintf("  %s: %v", s.Name(), err))
		} else {
			fmt.Printf("  Deleted %s\n", s.Name())
			deleted++
		}
	}

	fmt.Printf("\nDeleted %d/%d sprites\n", deleted, len(toDelete))

	if len(errs) > 0 {
		return fmt.Errorf("failed to delete some sprites:\n%s", strings.Join(errs, "\n"))
	}

	return nil
}

// loadSpriteToken loads SPRITE_TOKEN from environment or .wisp/.sprite.env file.
func loadSpriteToken() (string, error) {
	// Try environment variable first (for CI/CD)
	if token := os.Getenv("SPRITE_TOKEN"); token != "" {
		return token, nil
	}

	// Try loading from project's .wisp/.sprite.env
	projectRoot := findProjectRoot()
	if projectRoot != "" {
		envVars, err := config.LoadEnvFile(projectRoot)
		if err == nil {
			if token := envVars["SPRITE_TOKEN"]; token != "" {
				return token, nil
			}
		}
	}

	return "", fmt.Errorf("SPRITE_TOKEN not found; set environment variable or add to .wisp/.sprite.env")
}

// findProjectRoot walks up from the current directory to find the .wisp directory.
func findProjectRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}

	for {
		wispDir := dir + "/.wisp"
		if info, err := os.Stat(wispDir); err == nil && info.IsDir() {
			return dir
		}

		parent := dir[:strings.LastIndex(dir, "/")]
		if parent == "" || parent == dir {
			return ""
		}
		dir = parent
	}
}
