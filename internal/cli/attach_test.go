package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/state"
)

func TestAttachCommand_RequiresBranchArg(t *testing.T) {
	// The command should require exactly one argument (the branch)
	assert.Equal(t, "attach <branch>", attachCmd.Use)

	// Verify args validation is set to ExactArgs(1)
	err := attachCmd.Args(attachCmd, []string{})
	assert.Error(t, err)

	err = attachCmd.Args(attachCmd, []string{"branch1", "branch2"})
	assert.Error(t, err)

	err = attachCmd.Args(attachCmd, []string{"wisp/my-feature"})
	assert.NoError(t, err)
}

func TestAttachCommand_SessionNotFound(t *testing.T) {
	// Create a temp directory for the test
	tmpDir := t.TempDir()

	// Initialize store without creating any sessions
	store := state.NewStore(tmpDir)

	// Attempt to get a non-existent session
	_, err := store.GetSession("wisp/nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

func TestAttachCommand_LoadsSessionForSpriteName(t *testing.T) {
	// Create a temp directory for the test
	tmpDir := t.TempDir()

	// Create .wisp/sessions directory
	sessionsDir := filepath.Join(tmpDir, ".wisp", "sessions")
	require.NoError(t, os.MkdirAll(sessionsDir, 0o755))

	// Initialize store and create a session
	store := state.NewStore(tmpDir)

	session := &config.Session{
		Repo:       "test-org/test-repo",
		Spec:       "docs/rfc.md",
		Branch:     "wisp/test-feature",
		SpriteName: "wisp-abc123",
		StartedAt:  time.Now(),
		Status:     config.SessionStatusRunning,
	}

	// Create the session
	require.NoError(t, store.CreateSession(session))

	// Verify we can load the session and get the sprite name
	loaded, err := store.GetSession("wisp/test-feature")
	require.NoError(t, err)
	assert.Equal(t, "wisp-abc123", loaded.SpriteName)
}

func TestAttachCommand_ShortDescription(t *testing.T) {
	assert.Equal(t, "Shell into a Sprite for a wisp session", attachCmd.Short)
}

func TestAttachCommand_ExpectedBehavior(t *testing.T) {
	// Document expected behavior without actually running sprite console:
	// 1. Load session from local storage
	// 2. Get sprite name from session
	// 3. Shell out to 'sprite console <name>'
	// 4. Restore terminal on exit

	// This test documents the sequence but doesn't execute it
	// since 'sprite console' requires actual Sprite infrastructure
	t.Skip("Requires actual Sprite infrastructure")
}
