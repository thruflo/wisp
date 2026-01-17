package testutil

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/sprite"
	"github.com/thruflo/wisp/internal/state"
)

// SetupTestDir creates a temporary directory with the .wisp directory structure
// for testing. Returns the temp directory path and a Store.
// The directory is automatically cleaned up when the test completes.
func SetupTestDir(t *testing.T) (string, *state.Store) {
	t.Helper()

	tmpDir := t.TempDir()

	// Create .wisp directory structure
	wispDir := filepath.Join(tmpDir, ".wisp")
	dirs := []string{
		wispDir,
		filepath.Join(wispDir, "sessions"),
		filepath.Join(wispDir, "templates", "default"),
	}
	for _, dir := range dirs {
		require.NoError(t, os.MkdirAll(dir, 0755))
	}

	// Write config.yaml with test defaults
	configContent := `limits:
  max_iterations: 10
  max_budget_usd: 5.00
  max_duration_hours: 1
  no_progress_threshold: 3
`
	require.NoError(t, os.WriteFile(filepath.Join(wispDir, "config.yaml"), []byte(configContent), 0644))

	// Write settings.json
	settings := config.Settings{
		Permissions: config.Permissions{
			Deny: []string{"Read(~/.ssh/**)", "Edit(~/.ssh/**)"},
		},
		MCPServers: map[string]config.MCPServer{},
	}
	settingsData, err := json.MarshalIndent(settings, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(wispDir, "settings.json"), settingsData, 0644))

	// Write .sprite.env with placeholder tokens (tests should override if needed)
	envContent := "GITHUB_TOKEN=test-token\nSPRITE_TOKEN=test-key\n"
	require.NoError(t, os.WriteFile(filepath.Join(wispDir, ".sprite.env"), []byte(envContent), 0644))

	// Write minimal template files
	templateDir := filepath.Join(wispDir, "templates", "default")
	require.NoError(t, os.WriteFile(filepath.Join(templateDir, "context.md"), []byte("# Context"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(templateDir, "create-tasks.md"), []byte("# Create Tasks"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(templateDir, "update-tasks.md"), []byte("# Update Tasks"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(templateDir, "review-tasks.md"), []byte("# Review Tasks"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(templateDir, "iterate.md"), []byte("# Iterate"), 0644))

	store := state.NewStore(tmpDir)
	return tmpDir, store
}

// Credentials holds credentials for real Sprite tests.
type Credentials struct {
	SpriteToken string
	GitHubToken string
}

// LoadTestCredentials loads credentials from .wisp/.sprite.env at the project root
// or from environment variables. Skips the test if SPRITE_TOKEN is not available.
func LoadTestCredentials(t *testing.T) *Credentials {
	t.Helper()

	creds := &Credentials{}

	// Try loading from .wisp/.sprite.env at project root
	projectRoot := FindProjectRoot(t)
	if projectRoot != "" {
		envVars, err := config.LoadEnvFile(projectRoot)
		if err == nil {
			creds.SpriteToken = envVars["SPRITE_TOKEN"]
			creds.GitHubToken = envVars["GITHUB_TOKEN"]
		}
	}

	// Fall back to environment variables (for CI/CD)
	if creds.SpriteToken == "" {
		creds.SpriteToken = os.Getenv("SPRITE_TOKEN")
	}
	if creds.GitHubToken == "" {
		creds.GitHubToken = os.Getenv("GITHUB_TOKEN")
	}

	// Skip if no token available
	if creds.SpriteToken == "" {
		t.Skip("SPRITE_TOKEN not available, skipping real Sprite test")
	}

	return creds
}

// TryLoadTestCredentials loads credentials but returns nil instead of skipping
// if credentials are not available. Useful for TestMain.
func TryLoadTestCredentials() *Credentials {
	creds := &Credentials{}

	// Try loading from .wisp/.sprite.env at project root
	projectRoot := findProjectRootNoTest()
	if projectRoot != "" {
		envVars, err := config.LoadEnvFile(projectRoot)
		if err == nil {
			creds.SpriteToken = envVars["SPRITE_TOKEN"]
			creds.GitHubToken = envVars["GITHUB_TOKEN"]
		}
	}

	// Fall back to environment variables (for CI/CD)
	if creds.SpriteToken == "" {
		creds.SpriteToken = os.Getenv("SPRITE_TOKEN")
	}
	if creds.GitHubToken == "" {
		creds.GitHubToken = os.Getenv("GITHUB_TOKEN")
	}

	if creds.SpriteToken == "" {
		return nil
	}

	return creds
}

// FindProjectRoot walks up from the current directory to find the .wisp directory.
func FindProjectRoot(t *testing.T) string {
	t.Helper()
	return findProjectRootNoTest()
}

// findProjectRootNoTest is the non-test version of FindProjectRoot.
func findProjectRootNoTest() string {
	// Start from the current working directory
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}

	// Walk up looking for .wisp directory
	for {
		wispDir := filepath.Join(dir, ".wisp")
		if info, err := os.Stat(wispDir); err == nil && info.IsDir() {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root
			return ""
		}
		dir = parent
	}
}

// MustMarshalJSON marshals a value to JSON, failing the test on error.
// Uses indented format for readability.
func MustMarshalJSON(t *testing.T, v interface{}) []byte {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	return data
}

// MustUnmarshalJSON unmarshals JSON data into v, failing the test on error.
func MustUnmarshalJSON(t *testing.T, data []byte, v interface{}) {
	t.Helper()
	require.NoError(t, json.Unmarshal(data, v))
}

// WriteTestFile writes content to a file in the test directory.
// Creates parent directories as needed.
func WriteTestFile(t *testing.T, basePath, relativePath string, content []byte) {
	t.Helper()
	fullPath := filepath.Join(basePath, relativePath)
	require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0755))
	require.NoError(t, os.WriteFile(fullPath, content, 0644))
}

// RealSpriteEnv holds credentials and client for real Sprite tests.
type RealSpriteEnv struct {
	SpriteToken string
	GitHubToken string
	Client      sprite.Client
}

// SetupRealSpriteEnv loads credentials and creates a real Sprite client.
// Skips the test if SPRITE_TOKEN is not available or if running in short mode.
func SetupRealSpriteEnv(t *testing.T) *RealSpriteEnv {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping real Sprite test in short mode")
	}

	creds := LoadTestCredentials(t)

	return &RealSpriteEnv{
		SpriteToken: creds.SpriteToken,
		GitHubToken: creds.GitHubToken,
		Client:      sprite.NewSDKClient(creds.SpriteToken),
	}
}

// TrySetupRealSpriteEnv loads credentials and creates a client, returning nil
// if credentials are not available. Useful for TestMain.
func TrySetupRealSpriteEnv() *RealSpriteEnv {
	creds := TryLoadTestCredentials()
	if creds == nil {
		return nil
	}

	return &RealSpriteEnv{
		SpriteToken: creds.SpriteToken,
		GitHubToken: creds.GitHubToken,
		Client:      sprite.NewSDKClient(creds.SpriteToken),
	}
}

// GenerateTestSpriteName creates a unique Sprite name for testing.
// Format: wisp-test-{hash}-{timestamp} where hash is derived from the test name.
// The name is kept short to comply with Sprite naming limits.
func GenerateTestSpriteName(t *testing.T) string {
	t.Helper()

	// Create a short hash from test name for uniqueness across concurrent tests
	h := sha256.Sum256([]byte(t.Name()))
	hash := hex.EncodeToString(h[:])[:8]

	// Use last 8 digits of Unix nano timestamp for uniqueness across runs
	timestamp := time.Now().UnixNano() % 100000000

	return fmt.Sprintf("wisp-test-%s-%d", hash, timestamp)
}

// CleanupSprite safely deletes a Sprite, logging but not failing on errors.
// Use with t.Cleanup() to ensure Sprites are deleted after tests.
func CleanupSprite(t *testing.T, client sprite.Client, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := client.Delete(ctx, name); err != nil {
		// Log but don't fail - the Sprite may already be deleted or never created
		t.Logf("cleanup: failed to delete Sprite %s: %v", name, err)
	} else {
		t.Logf("cleanup: deleted Sprite %s", name)
	}
}

// WaitForSpriteReady polls until the Sprite can execute commands.
// Returns error if the Sprite isn't ready within maxWait.
func WaitForSpriteReady(t *testing.T, client sprite.Client, name string, maxWait time.Duration) error {
	t.Helper()

	deadline := time.Now().Add(maxWait)
	pollInterval := 1 * time.Second

	for time.Now().Before(deadline) {
		// Try to execute a simple command with a short timeout
		ready := make(chan bool, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd, err := client.Execute(ctx, name, "", nil, "echo", "ready")
			if err != nil {
				ready <- false
				return
			}
			// Read output in goroutine to avoid blocking
			go io.Copy(io.Discard, cmd.Stdout)
			go io.Copy(io.Discard, cmd.Stderr)
			if cmd.Wait() == nil {
				ready <- true
				return
			}
			ready <- false
		}()

		select {
		case isReady := <-ready:
			if isReady {
				t.Logf("sprite %s ready after %v", name, maxWait-time.Until(deadline))
				return nil
			}
		case <-time.After(6 * time.Second):
			// Timeout on this attempt, try again
		}
		time.Sleep(pollInterval)
	}

	return fmt.Errorf("sprite %s not ready after %v", name, maxWait)
}
