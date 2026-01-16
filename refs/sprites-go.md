# Sprites Go SDK Reference

> Relevant usage patterns for wisp orchestrator.

## Installation

```bash
go get github.com/superfly/sprites-go
```

## Client Setup

```go
import sprites "github.com/superfly/sprites-go"

client := sprites.New(os.Getenv("SPRITE_TOKEN"))

// with options
client := sprites.New(token,
    sprites.WithBaseURL("https://api.sprites.dev"),
    sprites.WithHTTPClient(&http.Client{Timeout: 30 * time.Second}),
)
```

## Sprite Lifecycle

```go
ctx := context.Background()

// create
sprite, err := client.CreateSprite(ctx, "my-sprite", nil)

// get handle (doesn't verify existence)
sprite := client.Sprite("my-sprite")

// get and verify exists
sprite, err := client.GetSprite(ctx, "my-sprite")

// list all (auto-pagination)
sprites, err := client.ListAllSprites(ctx, "")

// list with prefix filter
sprites, err := client.ListAllSprites(ctx, "wisp-")

// delete
err := client.DeleteSprite(ctx, "my-sprite")
```

## Command Execution

Mirrors `os/exec.Cmd` API:

```go
// basic execution
cmd := sprite.Command("echo", "hello")
err := cmd.Run()

// capture output
cmd := sprite.Command("ls", "-la")
output, err := cmd.Output()

// combined stdout+stderr
output, err := cmd.CombinedOutput()

// with context/timeout
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
cmd := sprite.CommandContext(ctx, "long-running-task")

// working directory and environment
cmd := sprite.Command("npm", "test")
cmd.Dir = "/home/sprite/project"
cmd.Env = []string{"NODE_ENV=test", "CI=true"}
```

## I/O Pipes

```go
cmd := sprite.Command("cat")
stdin, _ := cmd.StdinPipe()
stdout, _ := cmd.StdoutPipe()

cmd.Start()

io.WriteString(stdin, "content")
stdin.Close()

output, _ := io.ReadAll(stdout)
cmd.Wait()
```

## File Transfer (via pipes)

**Write file to sprite:**
```go
cmd := sprite.Command("bash", "-c", "cat > /path/to/file")
stdin, _ := cmd.StdinPipe()
cmd.Start()
stdin.Write([]byte(content))
stdin.Close()
cmd.Wait()
```

**Read file from sprite:**
```go
cmd := sprite.Command("cat", "/path/to/file")
output, err := cmd.Output()
```

**Alternative: CLI -f flag** (not in SDK, requires shelling out):
```bash
sprite exec -f local.txt:/remote/path.txt "true"
```

## TTY Mode

```go
cmd := sprite.Command("bash")
cmd.SetTTY(true)
cmd.SetTTYSize(24, 80)

cmd.Stdin = os.Stdin
cmd.Stdout = os.Stdout
cmd.Stderr = os.Stderr

cmd.Start()
cmd.Resize(30, 100)  // resize later
cmd.Wait()
```

## Detachable Sessions

```go
// start detachable session
cmd := sprite.CreateDetachableSession("npm", "run", "dev")
cmd.Stdout = os.Stdout
cmd.Start()

// list sessions
sessions, _ := client.ListSessions(ctx, "my-sprite")
for _, s := range sessions {
    fmt.Printf("%s: %s\n", s.ID, s.Command)
}

// attach to existing session
cmd = sprite.AttachSessionContext(ctx, sessionID)
cmd.Stdout = os.Stdout
cmd.Run()
```

## Checkpoints

```go
// create checkpoint (streams progress)
stream, err := sprite.CreateCheckpoint(ctx)
defer stream.Close()
stream.ProcessAll(func(msg *sprites.StreamMessage) error {
    fmt.Println(msg.Data)
    return nil
})

// with comment
stream, err := sprite.CreateCheckpointWithComment(ctx, "before risky change")

// restore checkpoint
stream, err := sprite.RestoreCheckpoint(ctx, "v1")
defer stream.Close()
stream.ProcessAll(func(msg *sprites.StreamMessage) error {
    fmt.Println(msg.Data)
    return nil
})

// list checkpoints
checkpoints, err := sprite.ListCheckpoints(ctx, "")
for _, cp := range checkpoints {
    fmt.Printf("%s  %s  %s\n", cp.ID, cp.CreateTime.Format(time.RFC3339), cp.Comment)
}

// get checkpoint info
cp, err := sprite.GetCheckpoint(ctx, "v1")
```

## Port Forwarding

```go
// single port
session, err := client.ProxyPort(ctx, "my-sprite", 3000, 3000)
defer session.Close()

// multiple ports
sessions, err := client.ProxyPorts(ctx, "my-sprite", []sprites.PortMapping{
    {LocalPort: 3000, RemotePort: 3000},
    {LocalPort: 8080, RemotePort: 80},
})
```

## Error Handling

```go
err := cmd.Run()
if err != nil {
    if exitErr, ok := err.(*sprites.ExitError); ok {
        fmt.Printf("exit code: %d\n", exitErr.ExitCode())
    } else {
        // network or other error
        log.Fatal(err)
    }
}
```

## Key Types

```go
type SpriteInfo struct {
    ID           string
    Name         string
    Organization string
    URL          string
    URLSettings  *URLSettings
    Status       string
    CreatedAt    time.Time
    UpdatedAt    time.Time
}

type Session struct {
    ID           string
    Command      string
    Workdir      string
    Created      time.Time
    IsActive     bool
    LastActivity *time.Time
}

type Checkpoint struct {
    ID         string
    Comment    string
    CreateTime time.Time
}
```

## Sprite Environment

- **Home directory**: `/home/sprite/`
- **Storage**: 100 GB persistent
- **Pre-installed**: Node.js, Python, Go, Ruby, Rust, Elixir, Java, Bun, Deno, Git, Claude CLI
- **Hibernation**: auto-hibernates after 30s inactivity; filesystem persists, RAM doesn't
- **Wake time**: ~100-500ms normal, 1-2s cold start

## Internal API (from within sprite)

```bash
# checkpoint management from inside sprite
sprite-env checkpoints list
sprite-env checkpoints create
sprite-env checkpoints restore v1

# or via unix socket
curl --unix-socket /.sprite/api.sock http://sprite/v1/checkpoints
```

## CLI Commands (for reference)

```bash
sprite create <name>                    # create sprite
sprite use <name>                       # set active sprite for directory
sprite list [--prefix <prefix>]         # list sprites
sprite destroy                          # destroy sprite

sprite exec "command"                   # run command
sprite exec -tty "command"              # run with TTY
sprite exec -f src:dest "command"       # upload file before exec
sprite exec -d /dir "command"           # set working directory
sprite exec -e KEY=val "command"        # set env vars

sprite console                          # interactive shell

sprite sessions list                    # list sessions
sprite sessions attach <id>             # attach to session
sprite sessions kill <id>               # kill session

sprite checkpoint create [--comment ""] # create checkpoint
sprite checkpoint list                  # list checkpoints
sprite restore <version>                # restore checkpoint

sprite proxy <port>                     # forward port
sprite url                              # show sprite URL
```
