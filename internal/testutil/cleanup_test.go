package testutil

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thruflo/wisp/internal/sprite"
)

func TestRegisterSprite(t *testing.T) {
	// Clear any state from other tests
	ClearRegistry()

	RegisterSprite("test-sprite-1")
	RegisterSprite("test-sprite-2")

	names := RegisteredSprites()
	assert.Len(t, names, 2)
	assert.Contains(t, names, "test-sprite-1")
	assert.Contains(t, names, "test-sprite-2")

	// Cleanup
	ClearRegistry()
}

func TestUnregisterSprite(t *testing.T) {
	ClearRegistry()

	RegisterSprite("sprite-a")
	RegisterSprite("sprite-b")

	UnregisterSprite("sprite-a")

	names := RegisteredSprites()
	assert.Len(t, names, 1)
	assert.Contains(t, names, "sprite-b")
	assert.NotContains(t, names, "sprite-a")

	ClearRegistry()
}

func TestUnregisterSprite_NonExistent(t *testing.T) {
	ClearRegistry()

	// Should not panic when unregistering non-existent sprite
	UnregisterSprite("does-not-exist")

	names := RegisteredSprites()
	assert.Empty(t, names)
}

func TestRegisteredSprites_ReturnsCopy(t *testing.T) {
	ClearRegistry()

	RegisterSprite("sprite-1")

	names1 := RegisteredSprites()
	names2 := RegisteredSprites()

	// Modifying one should not affect the other
	names1 = append(names1, "modified")
	assert.Len(t, names2, 1)

	ClearRegistry()
}

func TestClearRegistry(t *testing.T) {
	ClearRegistry()

	RegisterSprite("sprite-1")
	RegisterSprite("sprite-2")

	assert.Len(t, RegisteredSprites(), 2)

	ClearRegistry()

	assert.Empty(t, RegisteredSprites())
}

func TestCleanupAllSprites_Success(t *testing.T) {
	ClearRegistry()

	mock := sprite.NewMockSpriteClient()

	RegisterSprite("sprite-a")
	RegisterSprite("sprite-b")

	err := CleanupAllSprites(mock)
	require.NoError(t, err)

	// All sprites should be deleted
	deleteCalls := mock.GetDeleteCalls()
	assert.Len(t, deleteCalls, 2)
	assert.Contains(t, deleteCalls, "sprite-a")
	assert.Contains(t, deleteCalls, "sprite-b")

	// Registry should be empty
	assert.Empty(t, RegisteredSprites())
}

func TestCleanupAllSprites_EmptyRegistry(t *testing.T) {
	ClearRegistry()

	mock := sprite.NewMockSpriteClient()

	err := CleanupAllSprites(mock)
	require.NoError(t, err)

	assert.Empty(t, mock.GetDeleteCalls())
}

// mockErrorClient is a client that returns errors on delete.
type mockErrorClient struct {
	sprite.Client
	deleteErr    error
	deleteCalls  []string
	deleteErrors map[string]error // per-sprite errors
}

func (m *mockErrorClient) Delete(ctx context.Context, name string) error {
	m.deleteCalls = append(m.deleteCalls, name)
	if err, ok := m.deleteErrors[name]; ok {
		return err
	}
	return m.deleteErr
}

func TestCleanupAllSprites_PartialFailure(t *testing.T) {
	ClearRegistry()

	mockErr := errors.New("delete failed")
	mock := &mockErrorClient{
		Client:       sprite.NewMockSpriteClient(),
		deleteErrors: map[string]error{"sprite-fail": mockErr},
	}

	RegisterSprite("sprite-ok")
	RegisterSprite("sprite-fail")

	err := CleanupAllSprites(mock)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete failed")

	// Both sprites were attempted
	assert.Len(t, mock.deleteCalls, 2)

	// Only the failed sprite should remain in registry
	names := RegisteredSprites()
	assert.Len(t, names, 1)
	assert.Contains(t, names, "sprite-fail")

	ClearRegistry()
}

func TestCleanupAllSprites_AllFail(t *testing.T) {
	ClearRegistry()

	mockErr := errors.New("delete failed")
	mock := &mockErrorClient{
		Client:    sprite.NewMockSpriteClient(),
		deleteErr: mockErr,
	}

	RegisterSprite("sprite-1")
	RegisterSprite("sprite-2")

	err := CleanupAllSprites(mock)
	require.Error(t, err)

	// All sprites should still be in registry
	names := RegisteredSprites()
	assert.Len(t, names, 2)

	ClearRegistry()
}

func TestRegisterSprite_Idempotent(t *testing.T) {
	ClearRegistry()

	RegisterSprite("sprite-1")
	RegisterSprite("sprite-1")
	RegisterSprite("sprite-1")

	names := RegisteredSprites()
	assert.Len(t, names, 1)

	ClearRegistry()
}

func TestCleanupRegistry_ThreadSafe(t *testing.T) {
	ClearRegistry()

	// Run many concurrent operations to detect race conditions
	// This test relies on -race flag to detect issues
	done := make(chan bool)

	for i := 0; i < 10; i++ {
		go func(n int) {
			for j := 0; j < 100; j++ {
				RegisterSprite("sprite")
				UnregisterSprite("sprite")
				RegisteredSprites()
			}
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	ClearRegistry()
}
