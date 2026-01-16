package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/thruflo/wisp/internal/config"
	"gopkg.in/yaml.v3"
)

// Store handles local session storage operations.
type Store struct {
	basePath string
}

// NewStore creates a new Store with the given base path.
// The base path should be the project root; sessions will be stored in .wisp/sessions/.
func NewStore(basePath string) *Store {
	return &Store{basePath: basePath}
}

// sessionsDir returns the path to the sessions directory.
func (s *Store) sessionsDir() string {
	return filepath.Join(s.basePath, ".wisp", "sessions")
}

// sessionDir returns the path to a specific session directory.
func (s *Store) sessionDir(branch string) string {
	return filepath.Join(s.sessionsDir(), sanitizeBranch(branch))
}

// sanitizeBranch converts a branch name to a safe directory name.
// Replaces "/" with "-" to avoid nested directories.
func sanitizeBranch(branch string) string {
	result := make([]byte, len(branch))
	for i := 0; i < len(branch); i++ {
		if branch[i] == '/' {
			result[i] = '-'
		} else {
			result[i] = branch[i]
		}
	}
	return string(result)
}

// CreateSession creates a new session directory and writes session.yaml.
func (s *Store) CreateSession(session *config.Session) error {
	dir := s.sessionDir(session.Branch)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create session directory: %w", err)
	}

	sessionPath := filepath.Join(dir, "session.yaml")
	data, err := yaml.Marshal(session)
	if err != nil {
		return fmt.Errorf("failed to marshal session: %w", err)
	}

	if err := os.WriteFile(sessionPath, data, 0o644); err != nil {
		return fmt.Errorf("failed to write session file: %w", err)
	}

	return nil
}

// GetSession reads session.yaml from the branch directory.
func (s *Store) GetSession(branch string) (*config.Session, error) {
	sessionPath := filepath.Join(s.sessionDir(branch), "session.yaml")

	data, err := os.ReadFile(sessionPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("session not found: %s", branch)
		}
		return nil, fmt.Errorf("failed to read session file: %w", err)
	}

	var session config.Session
	if err := yaml.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("failed to parse session file: %w", err)
	}

	return &session, nil
}

// ListSessions enumerates all session directories and returns session info.
func (s *Store) ListSessions() ([]*config.Session, error) {
	sessionsDir := s.sessionsDir()

	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*config.Session{}, nil
		}
		return nil, fmt.Errorf("failed to read sessions directory: %w", err)
	}

	var sessions []*config.Session
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		sessionPath := filepath.Join(sessionsDir, entry.Name(), "session.yaml")
		data, err := os.ReadFile(sessionPath)
		if err != nil {
			continue // Skip directories without session.yaml
		}

		var session config.Session
		if err := yaml.Unmarshal(data, &session); err != nil {
			continue // Skip invalid session files
		}

		sessions = append(sessions, &session)
	}

	return sessions, nil
}

// UpdateSession updates specific fields in session.yaml.
func (s *Store) UpdateSession(branch string, updateFn func(*config.Session)) error {
	session, err := s.GetSession(branch)
	if err != nil {
		return err
	}

	updateFn(session)

	sessionPath := filepath.Join(s.sessionDir(branch), "session.yaml")
	data, err := yaml.Marshal(session)
	if err != nil {
		return fmt.Errorf("failed to marshal session: %w", err)
	}

	if err := os.WriteFile(sessionPath, data, 0o644); err != nil {
		return fmt.Errorf("failed to write session file: %w", err)
	}

	return nil
}

// SaveState writes state.json to the session directory.
func (s *Store) SaveState(branch string, state *State) error {
	dir := s.sessionDir(branch)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create session directory: %w", err)
	}

	statePath := filepath.Join(dir, "state.json")
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	if err := os.WriteFile(statePath, data, 0o644); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	return nil
}

// LoadState reads state.json from the session directory.
func (s *Store) LoadState(branch string) (*State, error) {
	statePath := filepath.Join(s.sessionDir(branch), "state.json")

	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No state file yet
		}
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state file: %w", err)
	}

	return &state, nil
}

// SaveTasks writes tasks.json to the session directory.
func (s *Store) SaveTasks(branch string, tasks []Task) error {
	dir := s.sessionDir(branch)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create session directory: %w", err)
	}

	tasksPath := filepath.Join(dir, "tasks.json")
	data, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal tasks: %w", err)
	}

	if err := os.WriteFile(tasksPath, data, 0o644); err != nil {
		return fmt.Errorf("failed to write tasks file: %w", err)
	}

	return nil
}

// LoadTasks reads tasks.json from the session directory.
func (s *Store) LoadTasks(branch string) ([]Task, error) {
	tasksPath := filepath.Join(s.sessionDir(branch), "tasks.json")

	data, err := os.ReadFile(tasksPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No tasks file yet
		}
		return nil, fmt.Errorf("failed to read tasks file: %w", err)
	}

	var tasks []Task
	if err := json.Unmarshal(data, &tasks); err != nil {
		return nil, fmt.Errorf("failed to parse tasks file: %w", err)
	}

	return tasks, nil
}

// SaveHistory writes history.json to the session directory.
func (s *Store) SaveHistory(branch string, history []History) error {
	dir := s.sessionDir(branch)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create session directory: %w", err)
	}

	historyPath := filepath.Join(dir, "history.json")
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal history: %w", err)
	}

	if err := os.WriteFile(historyPath, data, 0o644); err != nil {
		return fmt.Errorf("failed to write history file: %w", err)
	}

	return nil
}

// LoadHistory reads history.json from the session directory.
func (s *Store) LoadHistory(branch string) ([]History, error) {
	historyPath := filepath.Join(s.sessionDir(branch), "history.json")

	data, err := os.ReadFile(historyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No history file yet
		}
		return nil, fmt.Errorf("failed to read history file: %w", err)
	}

	var history []History
	if err := json.Unmarshal(data, &history); err != nil {
		return nil, fmt.Errorf("failed to parse history file: %w", err)
	}

	return history, nil
}

// AppendHistory adds a new history entry to history.json.
func (s *Store) AppendHistory(branch string, entry History) error {
	history, err := s.LoadHistory(branch)
	if err != nil {
		return err
	}

	history = append(history, entry)
	return s.SaveHistory(branch, history)
}

// DeleteSession removes the session directory and all its contents.
func (s *Store) DeleteSession(branch string) error {
	dir := s.sessionDir(branch)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("failed to delete session directory: %w", err)
	}
	return nil
}

// SessionExists checks if a session directory exists.
func (s *Store) SessionExists(branch string) bool {
	sessionPath := filepath.Join(s.sessionDir(branch), "session.yaml")
	_, err := os.Stat(sessionPath)
	return err == nil
}
