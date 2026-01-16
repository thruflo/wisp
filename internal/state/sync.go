package state

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/sprite"
)

// SyncManager handles synchronization between local storage and Sprite.
type SyncManager struct {
	client sprite.Client
	store  *Store
}

// NewSyncManager creates a new SyncManager.
func NewSyncManager(client sprite.Client, store *Store) *SyncManager {
	return &SyncManager{
		client: client,
		store:  store,
	}
}

// SyncToSprite copies state.json, tasks.json, history.json from local to Sprite.
// Files are written to <repoPath>/.wisp/ on the Sprite.
func (m *SyncManager) SyncToSprite(ctx context.Context, spriteName, branch, repoPath string) error {
	// Sync state.json
	state, err := m.store.LoadState(branch)
	if err != nil {
		return fmt.Errorf("failed to load state: %w", err)
	}
	if state != nil {
		stateData, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal state: %w", err)
		}
		statePath := filepath.Join(repoPath, ".wisp", "state.json")
		if err := m.client.WriteFile(ctx, spriteName, statePath, stateData); err != nil {
			return fmt.Errorf("failed to write state to sprite: %w", err)
		}
	}

	// Sync tasks.json
	tasks, err := m.store.LoadTasks(branch)
	if err != nil {
		return fmt.Errorf("failed to load tasks: %w", err)
	}
	if tasks != nil {
		tasksData, err := json.MarshalIndent(tasks, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal tasks: %w", err)
		}
		tasksPath := filepath.Join(repoPath, ".wisp", "tasks.json")
		if err := m.client.WriteFile(ctx, spriteName, tasksPath, tasksData); err != nil {
			return fmt.Errorf("failed to write tasks to sprite: %w", err)
		}
	}

	// Sync history.json
	history, err := m.store.LoadHistory(branch)
	if err != nil {
		return fmt.Errorf("failed to load history: %w", err)
	}
	if history != nil {
		historyData, err := json.MarshalIndent(history, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal history: %w", err)
		}
		historyPath := filepath.Join(repoPath, ".wisp", "history.json")
		if err := m.client.WriteFile(ctx, spriteName, historyPath, historyData); err != nil {
			return fmt.Errorf("failed to write history to sprite: %w", err)
		}
	}

	return nil
}

// SyncFromSprite copies state.json, tasks.json, history.json from Sprite to local.
// Files are read from <repoPath>/.wisp/ on the Sprite.
func (m *SyncManager) SyncFromSprite(ctx context.Context, spriteName, branch, repoPath string) error {
	// Sync state.json
	statePath := filepath.Join(repoPath, ".wisp", "state.json")
	stateData, err := m.client.ReadFile(ctx, spriteName, statePath)
	if err == nil && len(stateData) > 0 {
		var state State
		if err := json.Unmarshal(stateData, &state); err != nil {
			return fmt.Errorf("failed to parse state from sprite: %w", err)
		}
		if err := m.store.SaveState(branch, &state); err != nil {
			return fmt.Errorf("failed to save state locally: %w", err)
		}
	}

	// Sync tasks.json
	tasksPath := filepath.Join(repoPath, ".wisp", "tasks.json")
	tasksData, err := m.client.ReadFile(ctx, spriteName, tasksPath)
	if err == nil && len(tasksData) > 0 {
		var tasks []Task
		if err := json.Unmarshal(tasksData, &tasks); err != nil {
			return fmt.Errorf("failed to parse tasks from sprite: %w", err)
		}
		if err := m.store.SaveTasks(branch, tasks); err != nil {
			return fmt.Errorf("failed to save tasks locally: %w", err)
		}
	}

	// Sync history.json
	historyPath := filepath.Join(repoPath, ".wisp", "history.json")
	historyData, err := m.client.ReadFile(ctx, spriteName, historyPath)
	if err == nil && len(historyData) > 0 {
		var history []History
		if err := json.Unmarshal(historyData, &history); err != nil {
			return fmt.Errorf("failed to parse history from sprite: %w", err)
		}
		if err := m.store.SaveHistory(branch, history); err != nil {
			return fmt.Errorf("failed to save history locally: %w", err)
		}
	}

	return nil
}

// CopySettingsToSprite copies settings.json to ~/.claude/settings.json on the Sprite.
func (m *SyncManager) CopySettingsToSprite(ctx context.Context, spriteName string, settings *config.Settings) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %w", err)
	}

	// Write to Claude Code's settings location
	settingsPath := "/home/sprite/.claude/settings.json"

	// Ensure .claude directory exists
	mkdirCmd := fmt.Sprintf("mkdir -p %s", filepath.Dir(settingsPath))
	cmd, err := m.client.Execute(ctx, spriteName, "", nil, "bash", "-c", mkdirCmd)
	if err != nil {
		return fmt.Errorf("failed to create .claude directory: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("failed to create .claude directory: %w", err)
	}

	if err := m.client.WriteFile(ctx, spriteName, settingsPath, data); err != nil {
		return fmt.Errorf("failed to write settings to sprite: %w", err)
	}

	return nil
}

// CopyTemplatesToSprite copies template files from local templates/ to repo .wisp/ on Sprite.
func (m *SyncManager) CopyTemplatesToSprite(ctx context.Context, spriteName, repoPath, localTemplatesDir string) error {
	// Read all files from the local templates directory
	entries, err := os.ReadDir(localTemplatesDir)
	if err != nil {
		return fmt.Errorf("failed to read templates directory: %w", err)
	}

	// Ensure .wisp directory exists on Sprite
	wispDir := filepath.Join(repoPath, ".wisp")
	mkdirCmd := fmt.Sprintf("mkdir -p %s", wispDir)
	cmd, err := m.client.Execute(ctx, spriteName, "", nil, "bash", "-c", mkdirCmd)
	if err != nil {
		return fmt.Errorf("failed to create .wisp directory: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("failed to create .wisp directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue // Skip subdirectories for now
		}

		localPath := filepath.Join(localTemplatesDir, entry.Name())
		remotePath := filepath.Join(wispDir, entry.Name())

		content, err := os.ReadFile(localPath)
		if err != nil {
			return fmt.Errorf("failed to read template %s: %w", entry.Name(), err)
		}

		if err := m.client.WriteFile(ctx, spriteName, remotePath, content); err != nil {
			return fmt.Errorf("failed to write template %s to sprite: %w", entry.Name(), err)
		}
	}

	return nil
}

// Response represents user input in response.json for NEEDS_INPUT flow.
type Response struct {
	Answer string `json:"answer"`
}

// WriteResponseToSprite writes response.json to the Sprite for NEEDS_INPUT flow.
func (m *SyncManager) WriteResponseToSprite(ctx context.Context, spriteName, repoPath, answer string) error {
	response := Response{Answer: answer}
	data, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal response: %w", err)
	}

	responsePath := filepath.Join(repoPath, ".wisp", "response.json")
	if err := m.client.WriteFile(ctx, spriteName, responsePath, data); err != nil {
		return fmt.Errorf("failed to write response to sprite: %w", err)
	}

	return nil
}

// EnsureWispDirOnSprite creates the .wisp directory on the Sprite if it doesn't exist.
func (m *SyncManager) EnsureWispDirOnSprite(ctx context.Context, spriteName, repoPath string) error {
	wispDir := filepath.Join(repoPath, ".wisp")
	mkdirCmd := fmt.Sprintf("mkdir -p %s", wispDir)
	cmd, err := m.client.Execute(ctx, spriteName, "", nil, "bash", "-c", mkdirCmd)
	if err != nil {
		return fmt.Errorf("failed to create .wisp directory: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("failed to create .wisp directory: %w", err)
	}
	return nil
}
