# Wisp

Minimal Ralph Wiggum orchestrator for RFC-driven development on Sprites.

Point wisp at an RFC. Get a PR back.

## Requirements

- Go 1.21+
- Claude code (locally authenticated)
- [Sprites CLI](https://sprites.dev) and `SPRITE_TOKEN`
- `GITHUB_TOKEN` (for PR creation)

## Install

```bash
go install github.com/thruflo/wisp@latest
```

### Local development

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

Add project context to `AGENTS.md` in your repo root.

## Usage

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

## Docs

- [RFC](docs/rfc.md) — implementation specification
- [PRD](docs/prd.md) — product requirements
- [Testing](docs/testing.md) — test tiers, setup, and debugging
