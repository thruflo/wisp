package config

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Default values for Config.
const (
	DefaultMaxIterations       = 50
	DefaultMaxBudgetUSD        = 20.0
	DefaultMaxDurationHours    = 4.0
	DefaultNoProgressThreshold = 3
	DefaultServerPort          = 8374
)

// DefaultLimits returns limits with sensible default values.
func DefaultLimits() Limits {
	return Limits{
		MaxIterations:       DefaultMaxIterations,
		MaxBudgetUSD:        DefaultMaxBudgetUSD,
		MaxDurationHours:    DefaultMaxDurationHours,
		NoProgressThreshold: DefaultNoProgressThreshold,
	}
}

// DefaultServerConfig returns a ServerConfig with sensible default values.
func DefaultServerConfig() *ServerConfig {
	return &ServerConfig{
		Port: DefaultServerPort,
	}
}

// DefaultConfig returns a Config with sensible default values.
func DefaultConfig() Config {
	return Config{
		Limits: DefaultLimits(),
	}
}

// ValidationError represents a configuration validation error.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("validation error: %s: %s", e.Field, e.Message)
}

// LoadConfig reads and parses .wisp/config.yaml from the given base path.
// If the file doesn't exist, returns default config.
// Applies defaults for any missing fields.
func LoadConfig(basePath string) (*Config, error) {
	configPath := filepath.Join(basePath, ".wisp", "config.yaml")

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			cfg := DefaultConfig()
			return &cfg, nil
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if err := ValidateConfig(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// ValidateConfig checks that all config values are valid.
func ValidateConfig(cfg *Config) error {
	if cfg.Limits.MaxIterations <= 0 {
		return ValidationError{Field: "limits.max_iterations", Message: "must be positive"}
	}
	if cfg.Limits.MaxBudgetUSD <= 0 {
		return ValidationError{Field: "limits.max_budget_usd", Message: "must be positive"}
	}
	if cfg.Limits.MaxDurationHours <= 0 {
		return ValidationError{Field: "limits.max_duration_hours", Message: "must be positive"}
	}
	if cfg.Limits.NoProgressThreshold <= 0 {
		return ValidationError{Field: "limits.no_progress_threshold", Message: "must be positive"}
	}

	// Validate server config if present
	if cfg.Server != nil {
		if err := ValidateServerConfig(cfg.Server); err != nil {
			return err
		}
	}

	return nil
}

// ValidateServerConfig checks that server config values are valid.
func ValidateServerConfig(cfg *ServerConfig) error {
	if cfg.Port < 0 || cfg.Port > 65535 {
		return ValidationError{Field: "server.port", Message: "must be between 0 and 65535"}
	}
	return nil
}

// LoadSettings reads and parses .wisp/settings.json from the given base path.
func LoadSettings(basePath string) (*Settings, error) {
	settingsPath := filepath.Join(basePath, ".wisp", "settings.json")

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("settings file not found: %s", settingsPath)
		}
		return nil, fmt.Errorf("failed to read settings file: %w", err)
	}

	var settings Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("failed to parse settings file: %w", err)
	}

	return &settings, nil
}

// LoadSession reads and parses session.yaml from .wisp/sessions/<branch>/.
func LoadSession(basePath, branch string) (*Session, error) {
	sessionPath := filepath.Join(basePath, ".wisp", "sessions", branch, "session.yaml")

	data, err := os.ReadFile(sessionPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("session not found: %s", branch)
		}
		return nil, fmt.Errorf("failed to read session file: %w", err)
	}

	var session Session
	if err := yaml.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("failed to parse session file: %w", err)
	}

	if err := ValidateSession(&session); err != nil {
		return nil, err
	}

	return &session, nil
}

// ValidateSession checks that all required session fields are present.
func ValidateSession(session *Session) error {
	if session.Repo == "" {
		return ValidationError{Field: "repo", Message: "required field is empty"}
	}
	if session.Spec == "" {
		return ValidationError{Field: "spec", Message: "required field is empty"}
	}
	if session.Branch == "" {
		return ValidationError{Field: "branch", Message: "required field is empty"}
	}
	if session.SpriteName == "" {
		return ValidationError{Field: "sprite_name", Message: "required field is empty"}
	}
	if session.Status == "" {
		return ValidationError{Field: "status", Message: "required field is empty"}
	}
	return nil
}

// LoadEnvFile parses a .sprite.env file into a map of key-value pairs.
// The file format is KEY=VALUE per line. Lines starting with # are comments.
// Empty lines are ignored.
func LoadEnvFile(basePath string) (map[string]string, error) {
	envPath := filepath.Join(basePath, ".wisp", ".sprite.env")

	file, err := os.Open(envPath)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]string), nil
		}
		return nil, fmt.Errorf("failed to open env file: %w", err)
	}
	defer file.Close()

	env := make(map[string]string)
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Split on first =
		idx := strings.Index(line, "=")
		if idx == -1 {
			return nil, fmt.Errorf("invalid env file line %d: missing '='", lineNum)
		}

		key := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])

		// Strip surrounding quotes (single or double)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}

		if key == "" {
			return nil, fmt.Errorf("invalid env file line %d: empty key", lineNum)
		}

		env[key] = value
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read env file: %w", err)
	}

	return env, nil
}

// IsValidationError checks if an error is a ValidationError.
func IsValidationError(err error) bool {
	var ve ValidationError
	return errors.As(err, &ve)
}
