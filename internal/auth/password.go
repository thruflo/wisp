// Package auth provides password hashing and verification using argon2id.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/term"
)

// Argon2id parameters (recommended for password hashing)
const (
	argonTime    = 3     // iterations
	argonMemory  = 65536 // 64 MB
	argonThreads = 4     // parallelism
	argonKeyLen  = 32    // output length
	saltLength   = 16    // salt length
)

// HashPassword creates an argon2id hash of the given password.
// Returns a string in the format: $argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("failed to generate salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	saltB64 := base64.RawStdEncoding.EncodeToString(salt)
	hashB64 := base64.RawStdEncoding.EncodeToString(hash)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, saltB64, hashB64), nil
}

// VerifyPassword checks if the provided password matches the hash.
// The hash must be in the format: $argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>
func VerifyPassword(password, encodedHash string) (bool, error) {
	// Parse the hash
	params, salt, hash, err := decodeHash(encodedHash)
	if err != nil {
		return false, err
	}

	// Compute hash with the same parameters
	computed := argon2.IDKey([]byte(password), salt, params.time, params.memory, params.threads, params.keyLen)

	// Constant-time comparison
	if subtle.ConstantTimeCompare(hash, computed) == 1 {
		return true, nil
	}
	return false, nil
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
	keyLen  uint32
}

// decodeHash parses an encoded argon2id hash string.
func decodeHash(encodedHash string) (*argonParams, []byte, []byte, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 {
		return nil, nil, nil, fmt.Errorf("invalid hash format: expected 6 parts, got %d", len(parts))
	}

	if parts[1] != "argon2id" {
		return nil, nil, nil, fmt.Errorf("invalid hash algorithm: expected argon2id, got %s", parts[1])
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return nil, nil, nil, fmt.Errorf("invalid version format: %w", err)
	}
	if version != argon2.Version {
		return nil, nil, nil, fmt.Errorf("unsupported argon2 version: %d", version)
	}

	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return nil, nil, nil, fmt.Errorf("invalid params format: %w", err)
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return nil, nil, nil, fmt.Errorf("invalid salt encoding: %w", err)
	}

	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return nil, nil, nil, fmt.Errorf("invalid hash encoding: %w", err)
	}

	return &argonParams{
		memory:  memory,
		time:    time,
		threads: threads,
		keyLen:  uint32(len(hash)),
	}, salt, hash, nil
}

// ErrEmptyPassword is returned when the user enters an empty password.
var ErrEmptyPassword = errors.New("password cannot be empty")

// ErrPasswordMismatch is returned when password confirmation doesn't match.
var ErrPasswordMismatch = errors.New("passwords do not match")

// PromptPassword prompts the user for a password (hidden input).
// Returns the entered password.
func PromptPassword(prompt string) (string, error) {
	fmt.Print(prompt)
	password, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println() // Add newline after hidden input
	if err != nil {
		return "", fmt.Errorf("failed to read password: %w", err)
	}
	return string(password), nil
}

// PromptAndConfirmPassword prompts for a password with confirmation.
// Returns the password if both entries match.
func PromptAndConfirmPassword() (string, error) {
	password, err := PromptPassword("Enter password for web server: ")
	if err != nil {
		return "", err
	}
	if password == "" {
		return "", ErrEmptyPassword
	}

	confirm, err := PromptPassword("Confirm password: ")
	if err != nil {
		return "", err
	}

	if password != confirm {
		return "", ErrPasswordMismatch
	}

	return password, nil
}
