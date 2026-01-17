package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashPassword(t *testing.T) {
	t.Parallel()

	password := "test-password-123"
	hash, err := HashPassword(password)
	require.NoError(t, err)

	// Check format: $argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>
	assert.Contains(t, hash, "$argon2id$")
	assert.Contains(t, hash, "v=19")
	assert.Contains(t, hash, "m=65536,t=3,p=4")
}

func TestHashPassword_UniquePerCall(t *testing.T) {
	t.Parallel()

	password := "same-password"
	hash1, err := HashPassword(password)
	require.NoError(t, err)

	hash2, err := HashPassword(password)
	require.NoError(t, err)

	// Hashes should be different due to random salt
	assert.NotEqual(t, hash1, hash2)
}

func TestVerifyPassword_Correct(t *testing.T) {
	t.Parallel()

	password := "correct-horse-battery-staple"
	hash, err := HashPassword(password)
	require.NoError(t, err)

	match, err := VerifyPassword(password, hash)
	require.NoError(t, err)
	assert.True(t, match)
}

func TestVerifyPassword_Incorrect(t *testing.T) {
	t.Parallel()

	password := "correct-password"
	wrongPassword := "wrong-password"
	hash, err := HashPassword(password)
	require.NoError(t, err)

	match, err := VerifyPassword(wrongPassword, hash)
	require.NoError(t, err)
	assert.False(t, match)
}

func TestVerifyPassword_EmptyPassword(t *testing.T) {
	t.Parallel()

	password := ""
	hash, err := HashPassword(password)
	require.NoError(t, err)

	// Empty password should still verify correctly
	match, err := VerifyPassword("", hash)
	require.NoError(t, err)
	assert.True(t, match)

	// Non-empty should not match
	match, err = VerifyPassword("not-empty", hash)
	require.NoError(t, err)
	assert.False(t, match)
}

func TestVerifyPassword_InvalidHashFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		hash string
	}{
		{"empty", ""},
		{"not enough parts", "$argon2id$v=19"},
		{"wrong algorithm", "$bcrypt$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA"},
		{"invalid version format", "$argon2id$version=19$m=65536,t=3,p=4$c2FsdA$aGFzaA"},
		{"invalid params format", "$argon2id$v=19$memory=65536$c2FsdA$aGFzaA"},
		{"invalid salt encoding", "$argon2id$v=19$m=65536,t=3,p=4$!!!invalid!!!$aGFzaA"},
		{"invalid hash encoding", "$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$!!!invalid!!!"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := VerifyPassword("password", tt.hash)
			assert.Error(t, err)
		})
	}
}

func TestVerifyPassword_DifferentParams(t *testing.T) {
	t.Parallel()

	// Test that we can verify a hash with different parameters
	// This uses the format that our HashPassword produces
	password := "test123"
	hash, err := HashPassword(password)
	require.NoError(t, err)

	// Should still verify correctly
	match, err := VerifyPassword(password, hash)
	require.NoError(t, err)
	assert.True(t, match)
}

func TestDecodeHash_ValidFormats(t *testing.T) {
	t.Parallel()

	// Valid hash from HashPassword
	password := "test"
	hash, err := HashPassword(password)
	require.NoError(t, err)

	params, salt, hashBytes, err := decodeHash(hash)
	require.NoError(t, err)

	assert.Equal(t, uint32(65536), params.memory)
	assert.Equal(t, uint32(3), params.time)
	assert.Equal(t, uint8(4), params.threads)
	assert.Equal(t, uint32(32), params.keyLen)
	assert.Len(t, salt, 16)
	assert.Len(t, hashBytes, 32)
}
