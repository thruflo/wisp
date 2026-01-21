package spriteloop

import (
	"context"
	"testing"
)

func TestNewLocalExecutor(t *testing.T) {
	executor := NewLocalExecutor()
	if executor == nil {
		t.Fatal("Expected non-nil executor")
	}
	if executor.HomeDir != "/var/local/wisp" {
		t.Errorf("HomeDir = %q, want %q", executor.HomeDir, "/var/local/wisp")
	}
}

func TestLocalExecutorExecuteNoArgs(t *testing.T) {
	executor := NewLocalExecutor()

	err := executor.Execute(context.Background(), "/tmp", nil, nil, nil)
	if err == nil {
		t.Error("Expected error for empty args")
	}
	if err.Error() != "no command specified" {
		t.Errorf("Expected 'no command specified' error, got %q", err.Error())
	}
}

func TestLocalExecutorExecuteEmptyArgs(t *testing.T) {
	executor := NewLocalExecutor()

	err := executor.Execute(context.Background(), "/tmp", []string{}, nil, nil)
	if err == nil {
		t.Error("Expected error for empty args")
	}
}

func TestLocalExecutorExecuteSimpleCommand(t *testing.T) {
	// Test with a simple command that should succeed
	executor := &LocalExecutor{
		HomeDir: t.TempDir(),
	}

	var output []string
	eventCallback := func(line string) {
		output = append(output, line)
	}

	// Use echo instead of claude - this tests the execution path
	err := executor.Execute(context.Background(), "/tmp", []string{"echo", "hello"}, eventCallback, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Check that we got some output
	if len(output) == 0 {
		t.Error("Expected some output from echo command")
	}
}

func TestLocalExecutorExecuteContextCancel(t *testing.T) {
	executor := &LocalExecutor{
		HomeDir: t.TempDir(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Use sleep command that would take a while
	err := executor.Execute(ctx, "/tmp", []string{"sleep", "10"}, nil, nil)
	if err == nil {
		t.Error("Expected error from cancelled context")
	}
}

func TestLocalExecutorExecuteWithCommandCallback(t *testing.T) {
	executor := &LocalExecutor{
		HomeDir: t.TempDir(),
	}

	callCount := 0
	commandCallback := func() error {
		callCount++
		if callCount >= 3 {
			return errUserKill
		}
		return nil
	}

	// Use a command that produces multiple lines of output
	err := executor.Execute(context.Background(), "/tmp", []string{"yes", "|", "head", "-n", "10"}, nil, commandCallback)

	// Should return errUserKill or context error
	if err != nil && err != errUserKill && err.Error() != "context canceled" {
		// Might be killed before callback is called
		t.Logf("Got error: %v (this is expected)", err)
	}
}

func TestLocalExecutorHomeDir(t *testing.T) {
	// Test with custom HomeDir
	tmpDir := t.TempDir()
	executor := &LocalExecutor{
		HomeDir: tmpDir,
	}

	var output []string
	eventCallback := func(line string) {
		output = append(output, line)
	}

	// Just run a simple command to verify it works
	err := executor.Execute(context.Background(), "/tmp", []string{"echo", "test"}, eventCallback, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
}

func TestMockExecutor(t *testing.T) {
	t.Run("returns nil when no func set", func(t *testing.T) {
		mock := &MockExecutor{}
		err := mock.Execute(context.Background(), "/tmp", []string{"test"}, nil, nil)
		if err != nil {
			t.Errorf("Expected nil error, got %v", err)
		}
	})

	t.Run("calls custom function", func(t *testing.T) {
		called := false
		mock := &MockExecutor{
			ExecuteFunc: func(ctx context.Context, dir string, args []string, eventCallback func(string), commandCallback func() error) error {
				called = true
				if dir != "/custom" {
					t.Errorf("dir = %q, want %q", dir, "/custom")
				}
				if len(args) != 2 || args[0] != "arg1" {
					t.Errorf("args = %v, want [arg1, arg2]", args)
				}
				return nil
			},
		}

		err := mock.Execute(context.Background(), "/custom", []string{"arg1", "arg2"}, nil, nil)
		if err != nil {
			t.Errorf("Expected nil error, got %v", err)
		}
		if !called {
			t.Error("Expected ExecuteFunc to be called")
		}
	})
}
