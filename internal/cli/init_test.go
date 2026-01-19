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

	// Reset flags to default
	initTemplate = "default"
	initForce = false

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
		assert.Contains(t, string(content), "GITHUB_TOKEN=")
		assert.Contains(t, string(content), "SPRITE_TOKEN=")
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

	// Set custom template name and reset force flag
	initTemplate = "custom"
	initForce = false

	err = runInit(initCmd, []string{})
	require.NoError(t, err)

	// Verify custom template directory
	templateDir := filepath.Join(tmpDir, ".wisp", "templates", "custom")
	assertDirExists(t, templateDir)
	assertFileExists(t, filepath.Join(templateDir, "context.md"))
}

func TestInitCommandFailsIfTemplateExists(t *testing.T) {
	tmpDir := t.TempDir()
	originalDir, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalDir)

	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	// Create .wisp directory and template directory first
	wispDir := filepath.Join(tmpDir, ".wisp")
	templateDir := filepath.Join(wispDir, "templates", "default")
	err = os.MkdirAll(templateDir, 0755)
	require.NoError(t, err)

	// Reset flags
	initTemplate = "default"
	initForce = false

	// Init should fail when template already exists
	err = runInit(initCmd, []string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
	assert.Contains(t, err.Error(), "--force")
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

func TestInitTemplateOnlyCreation(t *testing.T) {
	tmpDir := t.TempDir()
	originalDir, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalDir)

	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	// First, do a full init with default template
	initTemplate = "default"
	initForce = false
	err = runInit(initCmd, []string{})
	require.NoError(t, err)

	wispDir := filepath.Join(tmpDir, ".wisp")

	// Record config.yaml modification time to verify it's not rewritten
	configPath := filepath.Join(wispDir, "config.yaml")
	configInfo, err := os.Stat(configPath)
	require.NoError(t, err)
	configModTime := configInfo.ModTime()

	// Now init with a new template name (should create only the template)
	initTemplate = "website"
	initForce = false
	err = runInit(initCmd, []string{})
	require.NoError(t, err)

	// Verify new template directory was created
	newTemplateDir := filepath.Join(wispDir, "templates", "website")
	assertDirExists(t, newTemplateDir)
	assertFileExists(t, filepath.Join(newTemplateDir, "context.md"))

	// Verify config.yaml was NOT rewritten (same modification time)
	configInfoAfter, err := os.Stat(configPath)
	require.NoError(t, err)
	assert.Equal(t, configModTime, configInfoAfter.ModTime(), "config.yaml should not be modified during template-only creation")

	// Verify original template still exists
	assertDirExists(t, filepath.Join(wispDir, "templates", "default"))
}

func TestInitForceOverwritesTemplate(t *testing.T) {
	tmpDir := t.TempDir()
	originalDir, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalDir)

	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	// First, do a full init with default template
	initTemplate = "default"
	initForce = false
	err = runInit(initCmd, []string{})
	require.NoError(t, err)

	wispDir := filepath.Join(tmpDir, ".wisp")
	templateDir := filepath.Join(wispDir, "templates", "default")

	// Modify an existing template file to verify it gets overwritten
	contextPath := filepath.Join(templateDir, "context.md")
	originalContent, err := os.ReadFile(contextPath)
	require.NoError(t, err)
	err = os.WriteFile(contextPath, []byte("modified content"), 0644)
	require.NoError(t, err)

	// Try to init with default template again without --force (should fail)
	initTemplate = "default"
	initForce = false
	err = runInit(initCmd, []string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")

	// Verify content was NOT overwritten
	content, err := os.ReadFile(contextPath)
	require.NoError(t, err)
	assert.Equal(t, "modified content", string(content))

	// Now init with --force (should succeed and overwrite)
	initTemplate = "default"
	initForce = true
	err = runInit(initCmd, []string{})
	require.NoError(t, err)

	// Verify content WAS overwritten with original template content
	content, err = os.ReadFile(contextPath)
	require.NoError(t, err)
	assert.Equal(t, string(originalContent), string(content))
}

func TestInitTemplateOnlyHasCorrectContent(t *testing.T) {
	tmpDir := t.TempDir()
	originalDir, err := os.Getwd()
	require.NoError(t, err)
	defer os.Chdir(originalDir)

	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	// Create minimal .wisp structure without templates
	wispDir := filepath.Join(tmpDir, ".wisp")
	err = os.MkdirAll(wispDir, 0755)
	require.NoError(t, err)

	// Init with a template (template-only creation since .wisp exists)
	initTemplate = "backend"
	initForce = false
	err = runInit(initCmd, []string{})
	require.NoError(t, err)

	// Verify all expected template files exist with proper content
	templateDir := filepath.Join(wispDir, "templates", "backend")
	expectedTemplates := []string{
		"context.md",
		"create-tasks.md",
		"update-tasks.md",
		"review-tasks.md",
		"iterate.md",
		"generate-pr.md",
	}

	for _, tmpl := range expectedTemplates {
		path := filepath.Join(templateDir, tmpl)
		assertFileExists(t, path)

		content, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.NotEmpty(t, content, "template %s should have content", tmpl)

		// Verify content is meaningful (not just empty or placeholder)
		assert.True(t, len(content) > 50, "template %s should have substantial content", tmpl)
	}
}
