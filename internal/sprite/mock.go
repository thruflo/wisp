package sprite

import (
	"bytes"
	"context"
	"io"
	"sync"
)

// MockSpriteClient implements Client for integration testing.
// It provides fine-grained control over command execution results and file system state.
// This mock is exported for use by integration tests in other packages.
type MockSpriteClient struct {
	mu sync.Mutex

	// File system state (mock filesystem)
	files map[string][]byte

	// Command execution control
	executeFunc func(ctx context.Context, name string, dir string, env []string, args ...string) (*Cmd, error)

	// Exists responses per sprite name
	existsResponses map[string]bool
	existsErr       error

	// ReadFile error override (takes precedence over files map)
	readFileErrs map[string]error

	// Tracking
	createCalls  []MockCreateCall
	deleteCalls  []string
	executeCalls []MockExecuteCall
	writeCalls   []MockWriteCall
	readCalls    []MockReadCall
	existsCalls  []string
}

// MockCreateCall records a Create call.
type MockCreateCall struct {
	Name       string
	Checkpoint string
}

// MockExecuteCall records an Execute call.
type MockExecuteCall struct {
	Name string
	Dir  string
	Env  []string
	Args []string
}

// MockWriteCall records a WriteFile call.
type MockWriteCall struct {
	Name    string
	Path    string
	Content []byte
}

// MockReadCall records a ReadFile call.
type MockReadCall struct {
	Name string
	Path string
}

// NewMockSpriteClient creates a new MockSpriteClient with default configuration.
func NewMockSpriteClient() *MockSpriteClient {
	return &MockSpriteClient{
		files:           make(map[string][]byte),
		existsResponses: make(map[string]bool),
		readFileErrs:    make(map[string]error),
	}
}

// Create records the create call and returns nil.
func (m *MockSpriteClient) Create(ctx context.Context, name string, checkpoint string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createCalls = append(m.createCalls, MockCreateCall{Name: name, Checkpoint: checkpoint})
	return nil
}

// Delete records the delete call and returns nil.
func (m *MockSpriteClient) Delete(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteCalls = append(m.deleteCalls, name)
	return nil
}

// Exists returns the configured response for the given sprite name.
// If no response is configured, returns true (default behavior).
func (m *MockSpriteClient) Exists(ctx context.Context, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.existsCalls = append(m.existsCalls, name)

	if m.existsErr != nil {
		return false, m.existsErr
	}

	if exists, ok := m.existsResponses[name]; ok {
		return exists, nil
	}
	return true, nil // Default: sprite exists
}

// Execute runs the configured execute function or returns a successful mock command.
func (m *MockSpriteClient) Execute(ctx context.Context, name string, dir string, env []string, args ...string) (*Cmd, error) {
	m.mu.Lock()
	m.executeCalls = append(m.executeCalls, MockExecuteCall{Name: name, Dir: dir, Env: env, Args: args})
	execFn := m.executeFunc
	m.mu.Unlock()

	if execFn != nil {
		return execFn(ctx, name, dir, env, args...)
	}

	// Default: return a successful command
	return &Cmd{
		Stdout: io.NopCloser(bytes.NewBuffer(nil)),
		Stderr: io.NopCloser(bytes.NewBuffer(nil)),
	}, nil
}

// ExecuteOutput records the call and returns empty successful output.
func (m *MockSpriteClient) ExecuteOutput(ctx context.Context, name string, dir string, env []string, args ...string) (stdout, stderr []byte, exitCode int, err error) {
	m.mu.Lock()
	m.executeCalls = append(m.executeCalls, MockExecuteCall{Name: name, Dir: dir, Env: env, Args: args})
	m.mu.Unlock()

	// Default: return empty successful output
	return nil, nil, 0, nil
}

// WriteFile records the call and stores content in the mock filesystem.
func (m *MockSpriteClient) WriteFile(ctx context.Context, name string, path string, content []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writeCalls = append(m.writeCalls, MockWriteCall{Name: name, Path: path, Content: content})
	m.files[path] = content
	return nil
}

// ReadFile returns content from the mock filesystem.
// Returns io.EOF if the file doesn't exist.
func (m *MockSpriteClient) ReadFile(ctx context.Context, name string, path string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readCalls = append(m.readCalls, MockReadCall{Name: name, Path: path})

	// Check for configured error first
	if err, ok := m.readFileErrs[path]; ok {
		return nil, err
	}

	if content, ok := m.files[path]; ok {
		return content, nil
	}
	return nil, io.EOF
}

// SetFile sets a file in the mock filesystem.
func (m *MockSpriteClient) SetFile(path string, content []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[path] = content
}

// GetFile gets a file from the mock filesystem.
func (m *MockSpriteClient) GetFile(path string) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	content, ok := m.files[path]
	return content, ok
}

// SetExecuteFunc sets a custom execute function.
func (m *MockSpriteClient) SetExecuteFunc(fn func(ctx context.Context, name string, dir string, env []string, args ...string) (*Cmd, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.executeFunc = fn
}

// SetExistsResponse configures the Exists response for a specific sprite name.
func (m *MockSpriteClient) SetExistsResponse(name string, exists bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.existsResponses[name] = exists
}

// SetExistsError configures the Exists method to return an error.
func (m *MockSpriteClient) SetExistsError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.existsErr = err
}

// SetReadFileResponse configures the ReadFile response for a specific path.
// If err is non-nil, ReadFile will return that error.
// If err is nil, the content will be stored in the mock filesystem.
func (m *MockSpriteClient) SetReadFileResponse(path string, content []byte, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.readFileErrs[path] = err
	} else {
		m.files[path] = content
		delete(m.readFileErrs, path)
	}
}

// GetWriteCalls returns a copy of the recorded WriteFile calls.
func (m *MockSpriteClient) GetWriteCalls() []MockWriteCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]MockWriteCall, len(m.writeCalls))
	copy(result, m.writeCalls)
	return result
}

// GetExecuteCalls returns a copy of the recorded Execute calls.
func (m *MockSpriteClient) GetExecuteCalls() []MockExecuteCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]MockExecuteCall, len(m.executeCalls))
	copy(result, m.executeCalls)
	return result
}

// GetCreateCalls returns a copy of the recorded Create calls.
func (m *MockSpriteClient) GetCreateCalls() []MockCreateCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]MockCreateCall, len(m.createCalls))
	copy(result, m.createCalls)
	return result
}

// GetDeleteCalls returns a copy of the recorded Delete calls.
func (m *MockSpriteClient) GetDeleteCalls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.deleteCalls))
	copy(result, m.deleteCalls)
	return result
}

// GetReadCalls returns a copy of the recorded ReadFile calls.
func (m *MockSpriteClient) GetReadCalls() []MockReadCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]MockReadCall, len(m.readCalls))
	copy(result, m.readCalls)
	return result
}

// GetExistsCalls returns a copy of the recorded Exists calls.
func (m *MockSpriteClient) GetExistsCalls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.existsCalls))
	copy(result, m.existsCalls)
	return result
}

// Reset clears all recorded calls and resets configuration.
func (m *MockSpriteClient) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files = make(map[string][]byte)
	m.existsResponses = make(map[string]bool)
	m.readFileErrs = make(map[string]error)
	m.executeFunc = nil
	m.existsErr = nil
	m.createCalls = nil
	m.deleteCalls = nil
	m.executeCalls = nil
	m.writeCalls = nil
	m.readCalls = nil
	m.existsCalls = nil
}

// Verify MockSpriteClient implements Client interface.
var _ Client = (*MockSpriteClient)(nil)
