<img width="98" src="docs/wisp-icon.png" />

# Wisp - Ralphing on Sprites

Wisp automates spec-driven development using pure bash loop Ralph in isolated [Sprites](https://sprites.dev).

## Requirements

- `claude` (locally authenticated)
- `GITHUB_TOKEN` (for cloning and PR creation)
- `SPRITE_TOKEN`

## Install

```bash
go install github.com/thruflo/wisp/cmd/wisp@latest
```

## Setup

```bash
cd your-project
wisp init
```

Edit `.wisp/.sprite.env` with your credentials:

```bash
GITHUB_TOKEN="..."
SPRITE_TOKEN="..."
```

## Usage

You provide an RFC. Wisp generates tasks and runs `claude` until
done or blocked. Prompts for user input when needed. When done
produces a PR.

### Start a session

```bash
wisp start --repo org/repo --spec docs/my-rfc.md
```

With sibling repos:

```bash
wisp start --repo org/repo --spec docs/my-rfc.md --sibling-repos org/shared-lib
```

### Interactive mode

- `t` — toggle tail (stream output)
- `a` — attach (shell into Sprite)
- `d` — detach (return to summary)
- `k` — kill session
- `esc` — background

### Respond to questions

When the agent needs input, the TUI prompts for a response. Type and press Enter.

### Remote access

Monitor and interact with sessions from any device using the built-in web server:

```bash
wisp start --repo org/repo --spec docs/rfc.md --server
```

On first use, you'll be prompted to set a password. The server URL is printed
(default: `http://localhost:8374`). Expose via ngrok for mobile access:

```bash
ngrok http 8374
```

The web interface provides:
- Dashboard with active sessions
- Task list with completion status
- Live Claude output stream
- Input prompt for NEEDS_INPUT responses
- Browser notifications when input is needed

Additional server options:

```bash
wisp start ... --server --port 9000    # custom port
wisp start ... --server --password     # change password
wisp resume wisp/my-feature --server   # resume with server
```

### Complete and create PR

```bash
wisp done feat-auth
```

### Other commands

```bash
wisp status              # list sessions
wisp status feat-auth    # show session details
wisp stop feat-auth      # stop session
wisp resume feat-auth    # resume session
wisp attach feat-auth    # shell into Sprite
wisp tail feat-auth      # stream output
```

### Update RFC mid-implementation

```bash
wisp update --spec docs/my-rfc.md --branch feat-auth
```

### Address PR feedback

```bash
wisp review feedback.md --branch feat-auth
```

## Configuration

`.wisp/config.yaml` — limits:

```yaml
limits:
  max_iterations: 50
  max_budget_usd: 20.00
  max_duration_hours: 4
  no_progress_threshold: 3
```

`.wisp/settings.json` — Claude deny rules and MCP servers.

`.wisp/templates/default/` — prompt templates.

## Local development

```bash
go install ./cmd/wisp
```

Rebuilds to `$GOPATH/bin`. Run again after changes.

### Running tests

```bash
make test                            # unit tests
make test-integration                # integration tests (mocks)
make test-real-sprites               # real Sprite tests
make test-e2e                        # E2E CLI tests
```

See [Testing Guide](docs/testing.md) for credentials setup, debugging, and cleanup.

## Docs

- [RFC](docs/rfc.md) — implementation specification
- [PRD](docs/prd.md) — product requirements
- [Testing](docs/testing.md) — test tiers, setup, and debugging
