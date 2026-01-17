package testutil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thruflo/wisp/internal/config"
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
