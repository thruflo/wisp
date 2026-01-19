package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig_Default(t *testing.T) {
	t.Parallel()

	// Create temp directory without config file
	tmpDir := t.TempDir()

	cfg, err := LoadConfig(tmpDir)
	require.NoError(t, err)

	// Should return default values
	assert.Equal(t, DefaultMaxIterations, cfg.Limits.MaxIterations)
	assert.Equal(t, DefaultMaxBudgetUSD, cfg.Limits.MaxBudgetUSD)
	assert.Equal(t, DefaultMaxDurationHours, cfg.Limits.MaxDurationHours)
	assert.Equal(t, DefaultNoProgressThreshold, cfg.Limits.NoProgressThreshold)
}

func TestLoadConfig_ValidFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	wispDir := filepath.Join(tmpDir, ".wisp")
	require.NoError(t, os.MkdirAll(wispDir, 0o755))

	configContent := `limits:
  max_iterations: 100
  max_budget_usd: 50.00
  max_duration_hours: 8
  no_progress_threshold: 5
`
	require.NoError(t, os.WriteFile(filepath.Join(wispDir, "config.yaml"), []byte(configContent), 0o644))

	cfg, err := LoadConfig(tmpDir)
	require.NoError(t, err)

	assert.Equal(t, 100, cfg.Limits.MaxIterations)
	assert.Equal(t, 50.0, cfg.Limits.MaxBudgetUSD)
	assert.Equal(t, 8.0, cfg.Limits.MaxDurationHours)
	assert.Equal(t, 5, cfg.Limits.NoProgressThreshold)
}

func TestLoadConfig_PartialFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	wispDir := filepath.Join(tmpDir, ".wisp")
	require.NoError(t, os.MkdirAll(wispDir, 0o755))

	// Only set max_iterations, rest should keep defaults
	configContent := `limits:
  max_iterations: 25
`
	require.NoError(t, os.WriteFile(filepath.Join(wispDir, "config.yaml"), []byte(configContent), 0o644))

	cfg, err := LoadConfig(tmpDir)
	require.NoError(t, err)

	assert.Equal(t, 25, cfg.Limits.MaxIterations)
	assert.Equal(t, DefaultMaxBudgetUSD, cfg.Limits.MaxBudgetUSD)
	assert.Equal(t, DefaultMaxDurationHours, cfg.Limits.MaxDurationHours)
	assert.Equal(t, DefaultNoProgressThreshold, cfg.Limits.NoProgressThreshold)
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	wispDir := filepath.Join(tmpDir, ".wisp")
	require.NoError(t, os.MkdirAll(wispDir, 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(wispDir, "config.yaml"), []byte(`limits: [`), 0o644))

	_, err := LoadConfig(tmpDir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse config file")
}

func TestLoadConfig_ValidationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		field   string
	}{
		{
			name: "zero max_iterations",
			content: `limits:
  max_iterations: 0
  max_budget_usd: 20
  max_duration_hours: 4
  no_progress_threshold: 3
`,
			field: "limits.max_iterations",
		},
		{
			name: "negative max_budget_usd",
			content: `limits:
  max_iterations: 50
  max_budget_usd: -1
  max_duration_hours: 4
  no_progress_threshold: 3
`,
			field: "limits.max_budget_usd",
		},
		{
			name: "zero max_duration_hours",
			content: `limits:
  max_iterations: 50
  max_budget_usd: 20
  max_duration_hours: 0
  no_progress_threshold: 3
`,
			field: "limits.max_duration_hours",
		},
		{
			name: "negative no_progress_threshold",
			content: `limits:
  max_iterations: 50
  max_budget_usd: 20
  max_duration_hours: 4
  no_progress_threshold: -1
`,
			field: "limits.no_progress_threshold",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpDir := t.TempDir()
			wispDir := filepath.Join(tmpDir, ".wisp")
			require.NoError(t, os.MkdirAll(wispDir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(wispDir, "config.yaml"), []byte(tt.content), 0o644))

			_, err := LoadConfig(tmpDir)
			require.Error(t, err)
			assert.True(t, IsValidationError(err))

			var ve ValidationError
			require.ErrorAs(t, err, &ve)
			assert.Equal(t, tt.field, ve.Field)
		})
	}
}

func TestLoadSettings_Valid(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	wispDir := filepath.Join(tmpDir, ".wisp")
	require.NoError(t, os.MkdirAll(wispDir, 0o755))

	settingsContent := `{
  "permissions": {
    "deny": ["Read(~/.ssh/**)", "Edit(~/.ssh/**)"]
  },
  "mcpServers": {
    "playwright": {
      "command": "npx",
      "args": ["@anthropic/mcp-playwright"]
    }
  }
}`
	require.NoError(t, os.WriteFile(filepath.Join(wispDir, "settings.json"), []byte(settingsContent), 0o644))

	settings, err := LoadSettings(tmpDir)
	require.NoError(t, err)

	assert.Equal(t, []string{"Read(~/.ssh/**)", "Edit(~/.ssh/**)"}, settings.Permissions.Deny)
	assert.Contains(t, settings.MCPServers, "playwright")
	assert.Equal(t, "npx", settings.MCPServers["playwright"].Command)
	assert.Equal(t, []string{"@anthropic/mcp-playwright"}, settings.MCPServers["playwright"].Args)
}

func TestLoadSettings_NotFound(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	_, err := LoadSettings(tmpDir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "settings file not found")
}

func TestLoadSettings_InvalidJSON(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	wispDir := filepath.Join(tmpDir, ".wisp")
	require.NoError(t, os.MkdirAll(wispDir, 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(wispDir, "settings.json"), []byte(`{`), 0o644))

	_, err := LoadSettings(tmpDir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse settings file")
}

func TestLoadSession_Valid(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, ".wisp", "sessions", "wisp-feat-auth")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))

	sessionContent := `repo: electric-sql/electric
spec: docs/rfc.md
siblings:
  - TanStack/db
checkpoint: checkpoint-123
branch: wisp/feat-auth
sprite_name: wisp-a1b2c3
started_at: 2026-01-16T10:00:00Z
status: running
`
	require.NoError(t, os.WriteFile(filepath.Join(sessionDir, "session.yaml"), []byte(sessionContent), 0o644))

	session, err := LoadSession(tmpDir, "wisp-feat-auth")
	require.NoError(t, err)

	assert.Equal(t, "electric-sql/electric", session.Repo)
	assert.Equal(t, "docs/rfc.md", session.Spec)
	assert.Len(t, session.Siblings, 1)
	assert.Equal(t, "TanStack/db", session.Siblings[0].Repo)
	assert.Equal(t, "", session.Siblings[0].Ref)
	assert.Equal(t, "checkpoint-123", session.Checkpoint)
	assert.Equal(t, "wisp/feat-auth", session.Branch)
	assert.Equal(t, "wisp-a1b2c3", session.SpriteName)
	assert.Equal(t, time.Date(2026, 1, 16, 10, 0, 0, 0, time.UTC), session.StartedAt)
	assert.Equal(t, SessionStatusRunning, session.Status)
}

func TestLoadSession_NotFound(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	_, err := LoadSession(tmpDir, "nonexistent-branch")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

func TestLoadSession_InvalidYAML(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, ".wisp", "sessions", "test-branch")
	require.NoError(t, os.MkdirAll(sessionDir, 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(sessionDir, "session.yaml"), []byte(`repo: [`), 0o644))

	_, err := LoadSession(tmpDir, "test-branch")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse session file")
}

func TestLoadSession_ValidationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		field   string
	}{
		{
			name: "missing repo",
			content: `spec: docs/rfc.md
branch: wisp/test
sprite_name: wisp-abc
status: running
`,
			field: "repo",
		},
		{
			name: "missing spec",
			content: `repo: owner/repo
branch: wisp/test
sprite_name: wisp-abc
status: running
`,
			field: "spec",
		},
		{
			name: "missing branch",
			content: `repo: owner/repo
spec: docs/rfc.md
sprite_name: wisp-abc
status: running
`,
			field: "branch",
		},
		{
			name: "missing sprite_name",
			content: `repo: owner/repo
spec: docs/rfc.md
branch: wisp/test
status: running
`,
			field: "sprite_name",
		},
		{
			name: "missing status",
			content: `repo: owner/repo
spec: docs/rfc.md
branch: wisp/test
sprite_name: wisp-abc
`,
			field: "status",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpDir := t.TempDir()
			sessionDir := filepath.Join(tmpDir, ".wisp", "sessions", "test-branch")
			require.NoError(t, os.MkdirAll(sessionDir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(sessionDir, "session.yaml"), []byte(tt.content), 0o644))

			_, err := LoadSession(tmpDir, "test-branch")
			require.Error(t, err)
			assert.True(t, IsValidationError(err))

			var ve ValidationError
			require.ErrorAs(t, err, &ve)
			assert.Equal(t, tt.field, ve.Field)
		})
	}
}

func TestLoadEnvFile_Valid(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	wispDir := filepath.Join(tmpDir, ".wisp")
	require.NoError(t, os.MkdirAll(wispDir, 0o755))

	envContent := `# API Keys
GITHUB_TOKEN=ghp_test456
SPRITE_TOKEN=sk-ant-test123

# Empty line above is ok

SOME_VAR=value with spaces
ANOTHER_VAR=no-spaces
`
	require.NoError(t, os.WriteFile(filepath.Join(wispDir, ".sprite.env"), []byte(envContent), 0o644))

	env, err := LoadEnvFile(tmpDir)
	require.NoError(t, err)

	assert.Equal(t, "ghp_test456", env["GITHUB_TOKEN"])
	assert.Equal(t, "sk-ant-test123", env["SPRITE_TOKEN"])
	assert.Equal(t, "value with spaces", env["SOME_VAR"])
	assert.Equal(t, "no-spaces", env["ANOTHER_VAR"])
	assert.Len(t, env, 4)
}

func TestLoadEnvFile_NotFound(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	env, err := LoadEnvFile(tmpDir)
	require.NoError(t, err)
	assert.Empty(t, env)
}

func TestLoadEnvFile_EmptyFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	wispDir := filepath.Join(tmpDir, ".wisp")
	require.NoError(t, os.MkdirAll(wispDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(wispDir, ".sprite.env"), []byte(""), 0o644))

	env, err := LoadEnvFile(tmpDir)
	require.NoError(t, err)
	assert.Empty(t, env)
}

func TestLoadEnvFile_OnlyComments(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	wispDir := filepath.Join(tmpDir, ".wisp")
	require.NoError(t, os.MkdirAll(wispDir, 0o755))

	envContent := `# This is a comment
# Another comment

# Yet another
`
	require.NoError(t, os.WriteFile(filepath.Join(wispDir, ".sprite.env"), []byte(envContent), 0o644))

	env, err := LoadEnvFile(tmpDir)
	require.NoError(t, err)
	assert.Empty(t, env)
}

func TestLoadEnvFile_InvalidFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		errMsg  string
	}{
		{
			name:    "missing equals",
			content: "INVALID_LINE",
			errMsg:  "missing '='",
		},
		{
			name:    "empty key",
			content: "=value",
			errMsg:  "empty key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpDir := t.TempDir()
			wispDir := filepath.Join(tmpDir, ".wisp")
			require.NoError(t, os.MkdirAll(wispDir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(wispDir, ".sprite.env"), []byte(tt.content), 0o644))

			_, err := LoadEnvFile(tmpDir)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errMsg)
		})
	}
}

func TestLoadEnvFile_EmptyValue(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	wispDir := filepath.Join(tmpDir, ".wisp")
	require.NoError(t, os.MkdirAll(wispDir, 0o755))

	envContent := `EMPTY_VALUE=`
	require.NoError(t, os.WriteFile(filepath.Join(wispDir, ".sprite.env"), []byte(envContent), 0o644))

	env, err := LoadEnvFile(tmpDir)
	require.NoError(t, err)
	assert.Equal(t, "", env["EMPTY_VALUE"])
}

func TestLoadEnvFile_ValueWithEquals(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	wispDir := filepath.Join(tmpDir, ".wisp")
	require.NoError(t, os.MkdirAll(wispDir, 0o755))

	envContent := `KEY=value=with=equals`
	require.NoError(t, os.WriteFile(filepath.Join(wispDir, ".sprite.env"), []byte(envContent), 0o644))

	env, err := LoadEnvFile(tmpDir)
	require.NoError(t, err)
	assert.Equal(t, "value=with=equals", env["KEY"])
}

func TestValidationError_Error(t *testing.T) {
	t.Parallel()

	ve := ValidationError{Field: "test.field", Message: "must be valid"}
	assert.Equal(t, "validation error: test.field: must be valid", ve.Error())
}

func TestIsValidationError(t *testing.T) {
	t.Parallel()

	ve := ValidationError{Field: "test", Message: "test"}
	assert.True(t, IsValidationError(ve))
	assert.False(t, IsValidationError(os.ErrNotExist))
}
