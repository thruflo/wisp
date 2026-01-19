package config

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestConfig_YAMLMarshal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config Config
		want   string
	}{
		{
			name: "default limits",
			config: Config{
				Limits: Limits{
					MaxIterations:       50,
					MaxBudgetUSD:        20.00,
					MaxDurationHours:    4,
					NoProgressThreshold: 3,
				},
			},
			want: `limits:
    max_iterations: 50
    max_budget_usd: 20
    max_duration_hours: 4
    no_progress_threshold: 3
`,
		},
		{
			name: "fractional budget",
			config: Config{
				Limits: Limits{
					MaxIterations:       100,
					MaxBudgetUSD:        15.50,
					MaxDurationHours:    2.5,
					NoProgressThreshold: 5,
				},
			},
			want: `limits:
    max_iterations: 100
    max_budget_usd: 15.5
    max_duration_hours: 2.5
    no_progress_threshold: 5
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := yaml.Marshal(tt.config)
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))
		})
	}
}

func TestConfig_YAMLUnmarshal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    Config
		wantErr bool
	}{
		{
			name: "valid config",
			input: `limits:
  max_iterations: 50
  max_budget_usd: 20.00
  max_duration_hours: 4
  no_progress_threshold: 3
`,
			want: Config{
				Limits: Limits{
					MaxIterations:       50,
					MaxBudgetUSD:        20.00,
					MaxDurationHours:    4,
					NoProgressThreshold: 3,
				},
			},
			wantErr: false,
		},
		{
			name: "partial config with defaults",
			input: `limits:
  max_iterations: 25
`,
			want: Config{
				Limits: Limits{
					MaxIterations: 25,
				},
			},
			wantErr: false,
		},
		{
			name:    "invalid yaml",
			input:   `limits: [`,
			want:    Config{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got Config
			err := yaml.Unmarshal([]byte(tt.input), &got)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSettings_JSONMarshal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings Settings
		want     string
	}{
		{
			name: "permissions only",
			settings: Settings{
				Permissions: Permissions{
					Deny: []string{
						"Read(~/.ssh/**)",
						"Edit(~/.ssh/**)",
					},
				},
			},
			want: `{"permissions":{"deny":["Read(~/.ssh/**)","Edit(~/.ssh/**)"]}}`,
		},
		{
			name: "with mcp server",
			settings: Settings{
				Permissions: Permissions{
					Deny: []string{"Read(**/.env)"},
				},
				MCPServers: map[string]MCPServer{
					"playwright": {
						Command: "npx",
						Args:    []string{"@anthropic/mcp-playwright"},
					},
				},
			},
			want: `{"permissions":{"deny":["Read(**/.env)"]},"mcpServers":{"playwright":{"command":"npx","args":["@anthropic/mcp-playwright"]}}}`,
		},
		{
			name: "mcp server without args",
			settings: Settings{
				Permissions: Permissions{
					Deny: []string{},
				},
				MCPServers: map[string]MCPServer{
					"simple": {
						Command: "simple-server",
					},
				},
			},
			want: `{"permissions":{"deny":[]},"mcpServers":{"simple":{"command":"simple-server"}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := json.Marshal(tt.settings)
			require.NoError(t, err)
			assert.JSONEq(t, tt.want, string(got))
		})
	}
}

func TestSettings_JSONUnmarshal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    Settings
		wantErr bool
	}{
		{
			name:  "full settings",
			input: `{"permissions":{"deny":["Read(~/.aws/**)","Edit(~/.aws/**)"]},"mcpServers":{"playwright":{"command":"npx","args":["@anthropic/mcp-playwright"]}}}`,
			want: Settings{
				Permissions: Permissions{
					Deny: []string{"Read(~/.aws/**)", "Edit(~/.aws/**)"},
				},
				MCPServers: map[string]MCPServer{
					"playwright": {
						Command: "npx",
						Args:    []string{"@anthropic/mcp-playwright"},
					},
				},
			},
			wantErr: false,
		},
		{
			name:  "minimal settings",
			input: `{"permissions":{"deny":[]}}`,
			want: Settings{
				Permissions: Permissions{
					Deny: []string{},
				},
			},
			wantErr: false,
		},
		{
			name:    "invalid json",
			input:   `{"permissions":}`,
			want:    Settings{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got Settings
			err := json.Unmarshal([]byte(tt.input), &got)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSession_YAMLMarshal(t *testing.T) {
	t.Parallel()

	startTime := time.Date(2026, 1, 16, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		session Session
		want    string
	}{
		{
			name: "full session",
			session: Session{
				Repo:       "electric-sql/electric",
				Spec:       "docs/rfc.md",
				Siblings:   []SiblingRepo{{Repo: "TanStack/db"}},
				Checkpoint: "checkpoint-123",
				Branch:     "wisp/feat-auth",
				SpriteName: "wisp-a1b2c3",
				StartedAt:  startTime,
				Status:     SessionStatusRunning,
			},
			want: `repo: electric-sql/electric
spec: docs/rfc.md
siblings:
    - repo: TanStack/db
checkpoint: checkpoint-123
branch: wisp/feat-auth
sprite_name: wisp-a1b2c3
started_at: 2026-01-16T10:00:00Z
status: running
`,
		},
		{
			name: "session without optional fields",
			session: Session{
				Repo:       "owner/repo",
				Spec:       "spec.md",
				Branch:     "wisp/feature",
				SpriteName: "wisp-xyz",
				StartedAt:  startTime,
				Status:     SessionStatusStopped,
			},
			want: `repo: owner/repo
spec: spec.md
branch: wisp/feature
sprite_name: wisp-xyz
started_at: 2026-01-16T10:00:00Z
status: stopped
`,
		},
		{
			name: "completed session",
			session: Session{
				Repo:       "org/project",
				Spec:       "rfc.md",
				Branch:     "wisp/done",
				SpriteName: "wisp-done1",
				StartedAt:  startTime,
				Status:     SessionStatusCompleted,
			},
			want: `repo: org/project
spec: rfc.md
branch: wisp/done
sprite_name: wisp-done1
started_at: 2026-01-16T10:00:00Z
status: completed
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := yaml.Marshal(tt.session)
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))
		})
	}
}

func TestSession_YAMLUnmarshal(t *testing.T) {
	t.Parallel()

	startTime := time.Date(2026, 1, 16, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		input   string
		want    Session
		wantErr bool
	}{
		{
			name: "full session",
			input: `repo: electric-sql/electric
spec: docs/rfc.md
siblings:
  - TanStack/db
checkpoint: checkpoint-123
branch: wisp/feat-auth
sprite_name: wisp-a1b2c3
started_at: 2026-01-16T10:00:00Z
status: running
`,
			want: Session{
				Repo:       "electric-sql/electric",
				Spec:       "docs/rfc.md",
				Siblings:   []SiblingRepo{{Repo: "TanStack/db"}},
				Checkpoint: "checkpoint-123",
				Branch:     "wisp/feat-auth",
				SpriteName: "wisp-a1b2c3",
				StartedAt:  startTime,
				Status:     SessionStatusRunning,
			},
			wantErr: false,
		},
		{
			name: "minimal session",
			input: `repo: owner/repo
spec: spec.md
branch: wisp/feature
sprite_name: wisp-xyz
started_at: 2026-01-16T10:00:00Z
status: stopped
`,
			want: Session{
				Repo:       "owner/repo",
				Spec:       "spec.md",
				Branch:     "wisp/feature",
				SpriteName: "wisp-xyz",
				StartedAt:  startTime,
				Status:     SessionStatusStopped,
			},
			wantErr: false,
		},
		{
			name: "multiple siblings",
			input: `repo: owner/repo
spec: spec.md
siblings:
  - org1/repo1
  - org2/repo2
  - org3/repo3
branch: wisp/multi
sprite_name: wisp-multi
started_at: 2026-01-16T10:00:00Z
status: running
`,
			want: Session{
				Repo:       "owner/repo",
				Spec:       "spec.md",
				Siblings:   []SiblingRepo{{Repo: "org1/repo1"}, {Repo: "org2/repo2"}, {Repo: "org3/repo3"}},
				Branch:     "wisp/multi",
				SpriteName: "wisp-multi",
				StartedAt:  startTime,
				Status:     SessionStatusRunning,
			},
			wantErr: false,
		},
		{
			name:    "invalid yaml",
			input:   `repo: [`,
			want:    Session{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got Session
			err := yaml.Unmarshal([]byte(tt.input), &got)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestConfig_YAMLRoundTrip(t *testing.T) {
	t.Parallel()

	config := Config{
		Limits: Limits{
			MaxIterations:       50,
			MaxBudgetUSD:        20.00,
			MaxDurationHours:    4,
			NoProgressThreshold: 3,
		},
	}

	data, err := yaml.Marshal(config)
	require.NoError(t, err)

	var got Config
	err = yaml.Unmarshal(data, &got)
	require.NoError(t, err)
	assert.Equal(t, config, got)
}

func TestSettings_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	settings := Settings{
		Permissions: Permissions{
			Deny: []string{
				"Read(~/.ssh/**)",
				"Edit(~/.ssh/**)",
				"Read(~/.aws/**)",
				"Edit(~/.aws/**)",
			},
		},
		MCPServers: map[string]MCPServer{
			"playwright": {
				Command: "npx",
				Args:    []string{"@anthropic/mcp-playwright"},
			},
		},
	}

	data, err := json.MarshalIndent(settings, "", "  ")
	require.NoError(t, err)

	var got Settings
	err = json.Unmarshal(data, &got)
	require.NoError(t, err)
	assert.Equal(t, settings, got)
}

func TestSession_YAMLRoundTrip(t *testing.T) {
	t.Parallel()

	session := Session{
		Repo:       "electric-sql/electric",
		Spec:       "docs/rfc.md",
		Siblings:   []SiblingRepo{{Repo: "TanStack/db"}, {Repo: "other/repo"}},
		Checkpoint: "checkpoint-123",
		Branch:     "wisp/feat-auth",
		SpriteName: "wisp-a1b2c3",
		StartedAt:  time.Date(2026, 1, 16, 10, 0, 0, 0, time.UTC),
		Status:     SessionStatusRunning,
	}

	data, err := yaml.Marshal(session)
	require.NoError(t, err)

	var got Session
	err = yaml.Unmarshal(data, &got)
	require.NoError(t, err)
	assert.Equal(t, session, got)
}
