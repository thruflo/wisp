# Wisp development guide

Go CLI tool for orchestrating Claude Code loops on Sprites.

Wisp automates RFC implementation using autonomous Claude Code loops
in isolated Sprites. Developer provides an RFC, wisp generates tasks,
runs Claude until completion or blockage, produces a PR.

## Project structure

```
cmd/
  wisp/             # main CLI entry point
  wisp-sprite/      # binary that runs on Sprite VM
internal/
  cli/              # command implementations
  config/           # configuration loading
  loop/             # orchestration-only loop (manages Sprite lifecycle)
  server/           # web server for browser-based UI
  spriteloop/       # iteration loop running on Sprite VM
  sprite/           # Sprite client wrapper
  state/            # state file handling
  stream/           # durable stream types and client
  tui/              # terminal UI
pkg/                # public API (if any)
```

## Durable stream architecture

Wisp uses a durable stream architecture to ensure Claude output is never lost
during network disconnections. The key components are:

### Stream package (`internal/stream`)

- `types.go` - Event types: session, task, claude_event, input_request, command, ack
- `filestore.go` - FileStore for persisting events as NDJSON on Sprite
- `client.go` - StreamClient for HTTP/SSE-based event consumption

### Spriteloop package (`internal/spriteloop`)

Runs on the Sprite VM via the `wisp-sprite` binary:

- `loop.go` - Core iteration logic (previously in internal/loop)
- `claude.go` - Claude process execution and output streaming
- `commands.go` - Command processing (kill, background, input_response)
- `server.go` - HTTP server exposing /stream, /command, /state endpoints

### Event flow

```
[Sprite VM]                              [Client]
Claude process                           TUI / Web
     ↓ output
FileStore.Append()                       StreamClient.Subscribe()
     ↓ writes                                 ↑ reads
stream.ndjson ←─────────── SSE ──────────────┘
```

### Message types

```go
// Sprite → Client
MessageTypeSession      // session state update
MessageTypeTask         // task state update
MessageTypeClaudeEvent  // Claude output event
MessageTypeInputRequest // request for user input
MessageTypeAck          // command acknowledgment

// Client → Sprite
MessageTypeCommand      // kill, background, input_response
```

## Go conventions

- Go 1.21+ with modules
- `gofmt` and `go vet` must pass
- Follow [Effective Go](https://go.dev/doc/effective_go)
- Errors are values — handle explicitly, no panics for recoverable errors
- Accept interfaces, return structs
- Keep packages focused and minimal

## CLI patterns

Use `cobra` for command structure:

```go
var startCmd = &cobra.Command{
    Use:   "start",
    Short: "Start a new session",
    RunE:  runStart,
}
```

- Commands in `internal/cli/`
- One file per command
- `RunE` over `Run` (return errors, don't os.Exit)
- Flags defined in `init()`, validated in `RunE`

## Error handling

```go
// wrap errors with context
return fmt.Errorf("failed to create sprite: %w", err)

// check specific errors
if errors.Is(err, os.ErrNotExist) { ... }

// custom error types for domain errors
type SessionNotFoundError struct { Branch string }
func (e SessionNotFoundError) Error() string { ... }
```

## Configuration

- Use `viper` for config file loading
- Environment variables override file values
- Validate early, fail fast

```go
type Config struct {
    Limits LimitsConfig `yaml:"limits"`
}

func Load(path string) (*Config, error) { ... }
func (c *Config) Validate() error { ... }
```

## Build

When you build:

- always use `go install ./cmd/wisp`
- never use `go build -o wisp ./cmd/wisp`

### Cross-compiling wisp-sprite

The `wisp-sprite` binary runs on Sprite VMs (Linux/amd64). Build with:

```bash
make build-sprite
# or manually:
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/wisp-sprite ./cmd/wisp-sprite
```

The binary is statically linked (no CGO) to run in the minimal Sprite environment.

## Testing

- Table-driven tests
- `testify/assert` for assertions
- `t.Parallel()` where safe
- Test files alongside source: `foo.go`, `foo_test.go`

```go
func TestParseState(t *testing.T) {
    tests := []struct {
        name    string
        input   []byte
        want    *State
        wantErr bool
    }{
        {"valid", []byte(`{"status":"CONTINUE"}`), &State{Status: "CONTINUE"}, false},
        {"invalid json", []byte(`{`), nil, true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := ParseState(tt.input)
            if tt.wantErr {
                assert.Error(t, err)
                return
            }
            assert.NoError(t, err)
            assert.Equal(t, tt.want, got)
        })
    }
}
```

Integration tests in `internal/integration/` with build tag:

```go
//go:build integration

func TestFullWorkflow(t *testing.T) { ... }
```

Run with: `go test -tags=integration ./internal/integration/...`

Key integration tests:
- `stream_reconnect_test.go` - Tests disconnect/reconnect scenarios for durable streams

IMPORTANT: When you run integration tests that could hang, make sure you use tight timeouts.

## Dependencies

```
github.com/spf13/cobra      # CLI framework
github.com/spf13/viper      # configuration
github.com/superfly/sprites-go  # Sprites SDK
golang.org/x/term           # terminal raw mode
github.com/stretchr/testify # test assertions
```

## Terminal handling

For TUI, use raw mode via `x/term`:

```go
oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
if err != nil { return err }
defer term.Restore(int(os.Stdin.Fd()), oldState)
```

ANSI escapes for display:
- `\033[2J` — clear screen
- `\033[H` — cursor home
- `\033[K` — clear line
- `\a` — bell

## JSON handling

```go
// state files
type State struct {
    Status   string `json:"status"`
    Summary  string `json:"summary"`
    Question string `json:"question,omitempty"`
    Error    string `json:"error,omitempty"`
}

// marshal with indent for human-readable files
json.MarshalIndent(state, "", "  ")
```

## Context and cancellation

Pass `context.Context` through call chain:

```go
func (s *Session) Run(ctx context.Context) error {
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
            if err := s.runIteration(ctx); err != nil {
                return err
            }
        }
    }
}
```

## Sprites SDK patterns

```go
client := sprites.New(token)
sprite, err := client.CreateSprite(ctx, name, nil)

// execute command
cmd := sprite.Command("claude", "-p", prompt)
cmd.Dir = workDir
cmd.Env = env
output, err := cmd.CombinedOutput()

// file transfer via pipes
cmd := sprite.Command("bash", "-c", "cat > "+path)
stdin, _ := cmd.StdinPipe()
cmd.Start()
stdin.Write(content)
stdin.Close()
cmd.Wait()
```

## Build and run

```bash
go build -o wisp ./cmd/wisp
go test ./...
go test -tags=integration ./...
```

## Refs

- Claude Code CLI `refs/claude-code-cli.md`
- Sprites Go client `refs/sprites-do.md` (also ../../sandbox/sprites-go)
