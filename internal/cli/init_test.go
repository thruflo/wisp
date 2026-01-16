package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thruflo/wisp/internal/config"
)

func TestInitCommand(t *testing.T) {
	// Create temp directory and change to it
	tmpDir := t.TempDir()
	originalDir, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalDir)

	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	// Reset template flag to default
	initTemplate = "default"

	// Run init command
	err = runInit(initCmd, []string{})
	require.NoError(t, err)

	wispDir := filepath.Join(tmpDir, ".wisp")

	// Verify directory structure
	t.Run("creates directory structure", func(t *testing.T) {
		assertDirExists(t, wispDir)
		assertDirExists(t, filepath.Join(wispDir, "sessions"))
		assertDirExists(t, filepath.Join(wispDir, "templates", "default"))
	})

	// Verify config.yaml
	t.Run("creates config.yaml with defaults", func(t *testing.T) {
		configPath := filepath.Join(wispDir, "config.yaml")
		assertFileExists(t, configPath)

		// Load and verify config
		cfg, err := config.LoadConfig(tmpDir)
		require.NoError(t, err)

		assert.Equal(t, 50, cfg.Limits.MaxIterations)
		assert.Equal(t, 20.0, cfg.Limits.MaxBudgetUSD)
		assert.Equal(t, 4.0, cfg.Limits.MaxDurationHours)
		assert.Equal(t, 3, cfg.Limits.NoProgressThreshold)
	})

	// Verify settings.json
	t.Run("creates settings.json with deny rules and MCP", func(t *testing.T) {
		settingsPath := filepath.Join(wispDir, "settings.json")
		assertFileExists(t, settingsPath)

		data, err := os.ReadFile(settingsPath)
		require.NoError(t, err)

		var settings config.Settings
		err = json.Unmarshal(data, &settings)
		require.NoError(t, err)

		// Verify deny rules
		assert.Contains(t, settings.Permissions.Deny, "Read(~/.ssh/**)")
		assert.Contains(t, settings.Permissions.Deny, "Edit(~/.ssh/**)")
		assert.Contains(t, settings.Permissions.Deny, "Read(~/.aws/**)")
		assert.Contains(t, settings.Permissions.Deny, "Read(**/.env)")

		// Verify Playwright MCP
		assert.Contains(t, settings.MCPServers, "playwright")
		assert.Equal(t, "npx", settings.MCPServers["playwright"].Command)
		assert.Equal(t, []string{"@anthropic/mcp-playwright"}, settings.MCPServers["playwright"].Args)
	})

	// Verify .sprite.env
	t.Run("creates .sprite.env placeholder", func(t *testing.T) {
		envPath := filepath.Join(wispDir, ".sprite.env")
		assertFileExists(t, envPath)

		content, err := os.ReadFile(envPath)
		require.NoError(t, err)
		assert.Contains(t, string(content), "ANTHROPIC_API_KEY=")
		assert.Contains(t, string(content), "GITHUB_TOKEN=")
	})

	// Verify .gitignore
	t.Run("creates .gitignore", func(t *testing.T) {
		gitignorePath := filepath.Join(wispDir, ".gitignore")
		assertFileExists(t, gitignorePath)

		content, err := os.ReadFile(gitignorePath)
		require.NoError(t, err)
		assert.Contains(t, string(content), ".sprite.env")
	})

	// Verify template files
	t.Run("creates template files", func(t *testing.T) {
		templateDir := filepath.Join(wispDir, "templates", "default")

		templates := []string{
			"context.md",
			"create-tasks.md",
			"update-tasks.md",
			"review-tasks.md",
			"iterate.md",
		}

		for _, tmpl := range templates {
			path := filepath.Join(templateDir, tmpl)
			assertFileExists(t, path)

			content, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.NotEmpty(t, content, "template %s should have content", tmpl)
		}
	})
}

func TestInitCommandWithCustomTemplate(t *testing.T) {
	tmpDir := t.TempDir()
	originalDir, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalDir)

	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	// Set custom template name
	initTemplate = "custom"

	err = runInit(initCmd, []string{})
	require.NoError(t, err)

	// Verify custom template directory
	templateDir := filepath.Join(tmpDir, ".wisp", "templates", "custom")
	assertDirExists(t, templateDir)
	assertFileExists(t, filepath.Join(templateDir, "context.md"))
}

func TestInitCommandFailsIfExists(t *testing.T) {
	tmpDir := t.TempDir()
	originalDir, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalDir)

	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	// Create .wisp directory first
	err = os.Mkdir(filepath.Join(tmpDir, ".wisp"), 0755)
	require.NoError(t, err)

	// Reset template flag
	initTemplate = "default"

	// Init should fail
	err = runInit(initCmd, []string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func assertDirExists(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err, "directory should exist: %s", path)
	assert.True(t, info.IsDir(), "should be a directory: %s", path)
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err, "file should exist: %s", path)
	assert.False(t, info.IsDir(), "should be a file: %s", path)
}
