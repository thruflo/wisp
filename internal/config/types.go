package config

import "time"

// Limits defines operational boundaries for a wisp session.
type Limits struct {
	MaxIterations       int     `yaml:"max_iterations"`
	MaxBudgetUSD        float64 `yaml:"max_budget_usd"`
	MaxDurationHours    float64 `yaml:"max_duration_hours"`
	NoProgressThreshold int     `yaml:"no_progress_threshold"`
}

// Config represents the .wisp/config.yaml file.
type Config struct {
	Limits Limits `yaml:"limits"`
}

// Permissions defines Claude Code permission rules.
type Permissions struct {
	Deny []string `json:"deny"`
}

// MCPServer defines an MCP server configuration.
type MCPServer struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// Settings represents the .wisp/settings.json file (Claude Code settings).
type Settings struct {
	Permissions Permissions          `json:"permissions"`
	MCPServers  map[string]MCPServer `json:"mcpServers,omitempty"`
}

// Session represents a .wisp/sessions/<branch>/session.yaml file.
type Session struct {
	Repo       string    `yaml:"repo"`
	Spec       string    `yaml:"spec"`
	Siblings   []string  `yaml:"siblings,omitempty"`
	Checkpoint string    `yaml:"checkpoint,omitempty"`
	Branch     string    `yaml:"branch"`
	SpriteName string    `yaml:"sprite_name"`
	StartedAt  time.Time `yaml:"started_at"`
	Status     string    `yaml:"status"`
}

// Session status values.
const (
	SessionStatusRunning   = "running"
	SessionStatusStopped   = "stopped"
	SessionStatusCompleted = "completed"
)
