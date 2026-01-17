package sprite

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockClient implements Client for testing.
type MockClient struct {
	// Track calls
	CreateCalls  []CreateCall
	ExecuteCalls []ExecuteCall
	WriteCalls   []WriteCall
	ReadCalls    []ReadCall
	DeleteCalls  []string
	ExistsCalls  []string

	// Configure responses
	CreateErr  error
	ExecuteErr error
	WriteErr   error
	ReadErr    error
	DeleteErr  error
	ExistsErr  error

	// ReadFile responses
	ReadResponses map[string][]byte

	// Execute responses
	ExecuteStdout []byte
	ExecuteStderr []byte

	// Exists responses
	ExistsResponses map[string]bool
}

type CreateCall struct {
	Name       string
	Checkpoint string
}

type ExecuteCall struct {
	Name string
	Dir  string
	Env  []string
	Args []string
}

type WriteCall struct {
	Name    string
	Path    string
	Content []byte
}

type ReadCall struct {
	Name string
	Path string
}

// NewMockClient creates a new MockClient with default responses.
func NewMockClient() *MockClient {
	return &MockClient{
		ReadResponses:   make(map[string][]byte),
		ExistsResponses: make(map[string]bool),
	}
}

func (m *MockClient) Create(ctx context.Context, name string, checkpoint string) error {
	m.CreateCalls = append(m.CreateCalls, CreateCall{Name: name, Checkpoint: checkpoint})
	return m.CreateErr
}

func (m *MockClient) Execute(ctx context.Context, name string, dir string, env []string, args ...string) (*Cmd, error) {
	m.ExecuteCalls = append(m.ExecuteCalls, ExecuteCall{Name: name, Dir: dir, Env: env, Args: args})
	if m.ExecuteErr != nil {
		return nil, m.ExecuteErr
	}

	// Create mock cmd with readable pipes
	stdout := io.NopCloser(bytes.NewReader(m.ExecuteStdout))
	stderr := io.NopCloser(bytes.NewReader(m.ExecuteStderr))

	return &Cmd{
		cmd:    nil, // Mock doesn't have real cmd
		Stdout: stdout,
		Stderr: stderr,
	}, nil
}

func (m *MockClient) ExecuteOutput(ctx context.Context, name string, dir string, env []string, args ...string) (stdout, stderr []byte, exitCode int, err error) {
	m.ExecuteCalls = append(m.ExecuteCalls, ExecuteCall{Name: name, Dir: dir, Env: env, Args: args})
	if m.ExecuteErr != nil {
		return nil, nil, -1, m.ExecuteErr
	}
	return m.ExecuteStdout, m.ExecuteStderr, 0, nil
}

func (m *MockClient) ExecuteOutputWithRetry(ctx context.Context, name string, dir string, env []string, args ...string) (stdout, stderr []byte, exitCode int, err error) {
	return m.ExecuteOutput(ctx, name, dir, env, args...)
}

func (m *MockClient) WriteFile(ctx context.Context, name string, path string, content []byte) error {
	m.WriteCalls = append(m.WriteCalls, WriteCall{Name: name, Path: path, Content: content})
	return m.WriteErr
}

func (m *MockClient) ReadFile(ctx context.Context, name string, path string) ([]byte, error) {
	m.ReadCalls = append(m.ReadCalls, ReadCall{Name: name, Path: path})
	if m.ReadErr != nil {
		return nil, m.ReadErr
	}
	if content, ok := m.ReadResponses[path]; ok {
		return content, nil
	}
	return nil, errors.New("file not found")
}

func (m *MockClient) Delete(ctx context.Context, name string) error {
	m.DeleteCalls = append(m.DeleteCalls, name)
	return m.DeleteErr
}

func (m *MockClient) Exists(ctx context.Context, name string) (bool, error) {
	m.ExistsCalls = append(m.ExistsCalls, name)
	if m.ExistsErr != nil {
		return false, m.ExistsErr
	}
	if exists, ok := m.ExistsResponses[name]; ok {
		return exists, nil
	}
	return false, nil
}

func TestMockClient_Create(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		spriteName string
		checkpoint string
		err        error
		wantErr    bool
	}{
		{
			name:       "create without checkpoint",
			spriteName: "wisp-abc123",
			checkpoint: "",
			err:        nil,
			wantErr:    false,
		},
		{
			name:       "create with checkpoint",
			spriteName: "wisp-def456",
			checkpoint: "v1",
			err:        nil,
			wantErr:    false,
		},
		{
			name:       "create error",
			spriteName: "wisp-err",
			checkpoint: "",
			err:        errors.New("failed to create"),
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mock := NewMockClient()
			mock.CreateErr = tt.err

			err := mock.Create(context.Background(), tt.spriteName, tt.checkpoint)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			require.Len(t, mock.CreateCalls, 1)
			assert.Equal(t, tt.spriteName, mock.CreateCalls[0].Name)
			assert.Equal(t, tt.checkpoint, mock.CreateCalls[0].Checkpoint)
		})
	}
}

func TestMockClient_Execute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		spriteName string
		dir        string
		env        []string
		args       []string
		stdout     []byte
		stderr     []byte
		err        error
		wantErr    bool
	}{
		{
			name:       "execute simple command",
			spriteName: "wisp-abc",
			dir:        "/home/sprite/project",
			env:        []string{"FOO=bar"},
			args:       []string{"echo", "hello"},
			stdout:     []byte("hello\n"),
			stderr:     []byte{},
			err:        nil,
			wantErr:    false,
		},
		{
			name:       "execute with error",
			spriteName: "wisp-err",
			dir:        "",
			env:        nil,
			args:       []string{"false"},
			stdout:     []byte{},
			stderr:     []byte("error message"),
			err:        errors.New("command failed"),
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mock := NewMockClient()
			mock.ExecuteErr = tt.err
			mock.ExecuteStdout = tt.stdout
			mock.ExecuteStderr = tt.stderr

			cmd, err := mock.Execute(context.Background(), tt.spriteName, tt.dir, tt.env, tt.args...)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, cmd)
			} else {
				require.NoError(t, err)
				require.NotNil(t, cmd)

				// Read stdout
				stdout, err := io.ReadAll(cmd.Stdout)
				require.NoError(t, err)
				assert.Equal(t, string(tt.stdout), string(stdout))

				// Read stderr
				stderr, err := io.ReadAll(cmd.Stderr)
				require.NoError(t, err)
				assert.Equal(t, string(tt.stderr), string(stderr))
			}

			require.Len(t, mock.ExecuteCalls, 1)
			assert.Equal(t, tt.spriteName, mock.ExecuteCalls[0].Name)
			assert.Equal(t, tt.dir, mock.ExecuteCalls[0].Dir)
			assert.Equal(t, tt.env, mock.ExecuteCalls[0].Env)
			assert.Equal(t, tt.args, mock.ExecuteCalls[0].Args)
		})
	}
}

func TestMockClient_WriteFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		spriteName string
		path       string
		content    []byte
		err        error
		wantErr    bool
	}{
		{
			name:       "write file",
			spriteName: "wisp-abc",
			path:       "/home/sprite/project/file.txt",
			content:    []byte("file content"),
			err:        nil,
			wantErr:    false,
		},
		{
			name:       "write error",
			spriteName: "wisp-err",
			path:       "/readonly/file.txt",
			content:    []byte("content"),
			err:        errors.New("permission denied"),
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mock := NewMockClient()
			mock.WriteErr = tt.err

			err := mock.WriteFile(context.Background(), tt.spriteName, tt.path, tt.content)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			require.Len(t, mock.WriteCalls, 1)
			assert.Equal(t, tt.spriteName, mock.WriteCalls[0].Name)
			assert.Equal(t, tt.path, mock.WriteCalls[0].Path)
			assert.Equal(t, tt.content, mock.WriteCalls[0].Content)
		})
	}
}

func TestMockClient_ReadFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		spriteName string
		path       string
		response   []byte
		err        error
		wantErr    bool
	}{
		{
			name:       "read file",
			spriteName: "wisp-abc",
			path:       "/home/sprite/project/file.txt",
			response:   []byte("file content"),
			err:        nil,
			wantErr:    false,
		},
		{
			name:       "read error",
			spriteName: "wisp-err",
			path:       "/nonexistent/file.txt",
			response:   nil,
			err:        errors.New("file not found"),
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mock := NewMockClient()
			mock.ReadErr = tt.err
			if tt.response != nil {
				mock.ReadResponses[tt.path] = tt.response
			}

			content, err := mock.ReadFile(context.Background(), tt.spriteName, tt.path)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, content)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.response, content)
			}

			require.Len(t, mock.ReadCalls, 1)
			assert.Equal(t, tt.spriteName, mock.ReadCalls[0].Name)
			assert.Equal(t, tt.path, mock.ReadCalls[0].Path)
		})
	}
}

func TestMockClient_Delete(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		spriteName string
		err        error
		wantErr    bool
	}{
		{
			name:       "delete sprite",
			spriteName: "wisp-abc123",
			err:        nil,
			wantErr:    false,
		},
		{
			name:       "delete error",
			spriteName: "wisp-nonexistent",
			err:        errors.New("sprite not found"),
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mock := NewMockClient()
			mock.DeleteErr = tt.err

			err := mock.Delete(context.Background(), tt.spriteName)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			require.Len(t, mock.DeleteCalls, 1)
			assert.Equal(t, tt.spriteName, mock.DeleteCalls[0])
		})
	}
}

func TestMockClient_Exists(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		spriteName string
		exists     bool
		err        error
		wantErr    bool
	}{
		{
			name:       "sprite exists",
			spriteName: "wisp-abc123",
			exists:     true,
			err:        nil,
			wantErr:    false,
		},
		{
			name:       "sprite does not exist",
			spriteName: "wisp-nonexistent",
			exists:     false,
			err:        nil,
			wantErr:    false,
		},
		{
			name:       "exists check error",
			spriteName: "wisp-err",
			exists:     false,
			err:        errors.New("network error"),
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mock := NewMockClient()
			mock.ExistsErr = tt.err
			mock.ExistsResponses[tt.spriteName] = tt.exists

			exists, err := mock.Exists(context.Background(), tt.spriteName)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.exists, exists)
			}

			require.Len(t, mock.ExistsCalls, 1)
			assert.Equal(t, tt.spriteName, mock.ExistsCalls[0])
		})
	}
}

func TestGenerateSpriteName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		repo         string
		branch       string
		expectedSlug string
	}{
		{
			name:         "basic branch",
			repo:         "electric-sql/electric",
			branch:       "foo",
			expectedSlug: "foo",
		},
		{
			name:         "branch with wisp/ prefix",
			repo:         "electric-sql/electric",
			branch:       "wisp/feat-auth",
			expectedSlug: "feat-auth",
		},
		{
			name:         "branch with special chars",
			repo:         "TanStack/db",
			branch:       "feature/add_user",
			expectedSlug: "feature-add-user",
		},
		{
			name:         "uppercase branch",
			repo:         "electric-sql/electric",
			branch:       "Feature/MyBranch",
			expectedSlug: "feature-mybranch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := GenerateSpriteName(tt.repo, tt.branch)

			// Check format: wisp-<slug>-<6-hex-chars>
			assert.True(t, strings.HasPrefix(got, "wisp-"), "expected prefix 'wisp-', got %s", got)
			assert.True(t, strings.HasPrefix(got, "wisp-"+tt.expectedSlug+"-"),
				"expected slug %q in name, got %s", tt.expectedSlug, got)

			// Extract the random suffix (last 6 chars)
			parts := strings.Split(got, "-")
			suffix := parts[len(parts)-1]
			assert.Equal(t, 6, len(suffix), "expected 6-char random suffix, got %d chars: %s", len(suffix), suffix)
		})
	}
}

func TestGenerateSpriteName_RandomSuffix(t *testing.T) {
	t.Parallel()

	repo := "org/repo"
	branch := "feature"

	// Generate multiple names and verify they're different (random)
	names := make(map[string]bool)
	for i := 0; i < 10; i++ {
		name := GenerateSpriteName(repo, branch)
		names[name] = true
	}

	// With random suffixes, we should get 10 unique names
	assert.Equal(t, 10, len(names), "expected 10 unique names with random suffixes")
}

func TestGenerateSpriteName_SlugTruncation(t *testing.T) {
	t.Parallel()

	repo := "org/repo"
	branch := "this-is-a-very-long-branch-name-that-should-be-truncated"

	got := GenerateSpriteName(repo, branch)

	// The slug portion should be max 20 chars
	// Format: wisp-<slug>-<6-hex>
	// Total should be: 5 (wisp-) + max 20 (slug) + 1 (-) + 6 (hex) = max 32
	assert.True(t, len(got) <= 32, "name too long: %s (len=%d)", got, len(got))
	assert.True(t, strings.HasPrefix(got, "wisp-"), "expected prefix 'wisp-', got %s", got)
}

// Verify Client interface is satisfied
var _ Client = (*MockClient)(nil)
var _ Client = (*SDKClient)(nil)
