package config

import (
	"strings"
	"time"
)

// Limits defines operational boundaries for a wisp session.
type Limits struct {
	MaxIterations       int     `yaml:"max_iterations"`
	MaxBudgetUSD        float64 `yaml:"max_budget_usd"`
	MaxDurationHours    float64 `yaml:"max_duration_hours"`
	NoProgressThreshold int     `yaml:"no_progress_threshold"`
}

// ServerConfig defines optional web server configuration.
type ServerConfig struct {
	Port         int    `yaml:"port"`
	PasswordHash string `yaml:"password_hash"`
}

// Config represents the .wisp/config.yaml file.
type Config struct {
	Limits Limits        `yaml:"limits"`
	Server *ServerConfig `yaml:"server,omitempty"`
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

// SiblingRepo represents a sibling repository with optional ref.
type SiblingRepo struct {
	Repo string `yaml:"repo"`
	Ref  string `yaml:"ref,omitempty"`
}

// UnmarshalYAML implements yaml.Unmarshaler for backwards compatibility.
// Supports both legacy string format ("org/repo") and new struct format.
func (s *SiblingRepo) UnmarshalYAML(unmarshal func(interface{}) error) error {
	// Try string first (legacy format)
	var str string
	if err := unmarshal(&str); err == nil {
		s.Repo = str
		s.Ref = ""
		return nil
	}
	// Then try struct format
	type siblingAlias SiblingRepo
	return unmarshal((*siblingAlias)(s))
}

// Session represents a .wisp/sessions/<branch>/session.yaml file.
type Session struct {
	Repo       string        `yaml:"repo"`
	Ref        string        `yaml:"ref,omitempty"`      // Base ref to branch from
	Spec       string        `yaml:"spec"`
	Continue   bool          `yaml:"continue,omitempty"` // Continue on existing branch
	Siblings   []SiblingRepo `yaml:"siblings,omitempty"` // Sibling repos with optional refs
	Checkpoint string        `yaml:"checkpoint,omitempty"`
	Branch     string        `yaml:"branch"`
	SpriteName string        `yaml:"sprite_name"`
	StartedAt  time.Time     `yaml:"started_at"`
	Status     string        `yaml:"status"`
}

// Session status values.
const (
	SessionStatusRunning   = "running"
	SessionStatusStopped   = "stopped"
	SessionStatusCompleted = "completed"
)

// ParseRepoRef parses "org/repo" or "org/repo@ref" format.
// Returns (repo, ref) where ref is empty string if not specified.
func ParseRepoRef(s string) (repo, ref string) {
	if idx := strings.LastIndex(s, "@"); idx != -1 {
		return s[:idx], s[idx+1:]
	}
	return s, ""
}
