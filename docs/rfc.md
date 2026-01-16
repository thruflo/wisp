# Wisp RFC: Implementation specification

> Minimal Ralph Wiggum orchestrator for RFC-driven development on Sprites.

## Overview

Wisp automates RFC implementation using autonomous Claude Code loops in isolated Sprites. Developer provides an RFC, wisp generates tasks, runs Claude until completion or blockage, produces a PR.

**Core principle:** the loop is dumb, the protocol is strict. Control lives in wisp, not the agent. The agent reads files, does work, writes files.

## CLI interface

```
wisp init [--template <name>]
wisp start --repo <org/repo> --spec <path> [--sibling-repos <org/repo>...] [--branch <name>] [--template <name>] [--checkpoint <id>]
wisp resume <branch>
wisp update --spec <path> --branch <branch>
wisp review <feedback.md> --branch <branch>
wisp status [<branch>]
wisp tail <branch>
wisp attach <branch>
wisp stop <branch>
wisp done [<branch>]
```

### Command details

| Command | Description |
|---------|-------------|
| `init` | scaffold `.wisp/` directory with templates, config, settings |
| `start` | create Sprite, clone repos, generate tasks from RFC, begin loop |
| `resume` | create fresh Sprite, restore state from local `.wisp/`, continue loop |
| `update` | copy updated RFC to Sprite, reconcile tasks, continue loop |
| `review` | generate tasks from PR feedback, continue loop |
| `status` | list sessions or show specific session state (reads local `.wisp/`) |
| `tail` | stream Claude output (read-only) |
| `attach` | shell into Sprite via `sprite console` |
| `stop` | gracefully stop loop, sync state locally |
| `done` | verify completion, create PR, teardown Sprite |

### Interactive mode

`start`, `resume`, `update`, `review` enter TUI with:

**Summary view (default):** status, iteration count, task progress, last summary. Surfaces NEEDS_INPUT prompts for inline response.

**Shortcuts:**
- `t` — toggle tail (stream Claude output)
- `a` — attach (shell into Sprite)
- `d` — detach (return to summary)
- `k` — kill session
- `esc` — background (exit wisp, loop continues)

### Done modes

```
All tasks complete. Create PR?
[p] push PR (auto-generated)  [m] push branch (manual PR)  [c] cancel
```

- `p` — agent generates PR title/body from task summaries, `gh pr create`, teardown
- `m` — push branch, open GitHub compare URL in browser, teardown

## Architecture

```
┌─────────────┐         ┌─────────────┐         ┌─────────────┐
│    wisp     │────────▶│   Sprite    │────────▶│ Claude Code │
│ (controller)│◀────────│ (sandbox)   │◀────────│  (agent)    │
└─────────────┘         └─────────────┘         └─────────────┘
      │                        │
      │                        │
      ▼                        ▼
┌─────────────┐         ┌─────────────┐
│ local .wisp/│         │ git commits │
│ (durable)   │         │ (durable)   │
└─────────────┘         └─────────────┘
```

**Controller (wisp):** starts/stops agent, enforces limits, reads state files, owns recovery and notification.

**Agent (Claude Code):** stateless executor. Reads task list and state, does one task, writes state, commits. Zero memory beyond files.

**Sprite:** isolated sandbox. Runs Claude with `--dangerously-skip-permissions`. Ephemeral — teardown loses nothing important.

## State management

### Local `.wisp/` (durable)

```
.wisp/
  .sprite.env              # credentials (gitignored)
  config.yaml              # limits
  settings.json            # Claude deny rules, MCP config
  templates/
    default/
      context.md           # system prompt (static)
      create-tasks.md      # RFC → tasks
      update-tasks.md      # RFC diff → updated tasks
      review-tasks.md      # PR feedback → new tasks
      iterate.md           # per-iteration instructions
  sessions/
    <branch>/
      session.yaml         # metadata, repo config
      state.json           # last known agent state
      tasks.json           # task list
      history.json         # rolling iteration history
      divergence.md        # RFC drift log (synced on done)
```

### Sprite (ephemeral, source of truth during run)

```
/home/sprite/<org>/<repo>/.wisp/
  state.json
  tasks.json
  history.json
  divergence.md
  response.json            # human response (written by wisp, deleted by agent after reading)
```

### Sync protocol

1. After each task completion (agent commits), wisp copies state.json, tasks.json, history.json from Sprite to local `.wisp/sessions/<branch>/`
2. On `wisp resume`, wisp copies local state to fresh Sprite
3. On `wisp done`, wisp copies divergence.md locally before teardown

## Sprite lifecycle

### Naming

`wisp-<hash>` where hash is derived from repo + branch. Debuggable, collision-resistant.

### Directory layout

```
/home/sprite/
  <primary-org>/
    <primary-repo>/       # working directory
  <sibling-org>/
    <sibling-repo>/       # relative path ../../sibling-org/sibling-repo works
```

### Setup sequence (`wisp start`)

1. Create Sprite (from checkpoint if specified)
2. Clone primary repo, create/checkout branch
3. Clone sibling repos to adjacent directories
4. Copy `.wisp/settings.json` to `~/.claude/settings.json`
5. Copy `.wisp/templates/<name>/` to repo's `.wisp/`
6. Inject env vars from `.sprite.env`
7. Run create-tasks prompt with RFC content → writes tasks.json
8. Begin iteration loop

### Resume sequence (`wisp resume`)

1. Create fresh Sprite (from checkpoint if specified)
2. Clone primary repo, checkout branch (contains previous commits)
3. Clone sibling repos
4. Copy settings, templates
5. Copy local state files (state.json, tasks.json, history.json) to Sprite
6. Inject env vars
7. Continue iteration loop

## Templates

All prompts are static markdown. No templating or injection. Agent reads state from files.

### context.md

System prompt. Contains:
- quality standards
- verification approach: "verify your work, prefer automated tests where the project supports them"
- instruction to read `AGENTS.md` in repo root for project context
- task JSON schema
- state JSON schema

Auto-generated by `wisp init`.

### create-tasks.md

One-shot prompt for RFC → tasks.json conversion. Agent reads RFC, outputs task list.

### update-tasks.md

Prompt for RFC reconciliation. Agent reads RFC diff + current tasks, outputs updated task list.

### review-tasks.md

Prompt for PR feedback. Agent reads feedback.md + current tasks, outputs new tasks to address feedback.

### iterate.md

Per-iteration prompt. Instructs agent to:
1. Read `.wisp/tasks.json`, `.wisp/state.json`, `.wisp/history.json`
2. Read `.wisp/response.json` if present, delete after reading
3. Pick next incomplete task
4. Implement and verify
5. Commit with descriptive message
6. Update tasks.json (mark `passes: true`)
7. Write state.json
8. If implementation differs from RFC, append to divergence.md

## Configuration

### `.wisp/.sprite.env`

```bash
ANTHROPIC_API_KEY=sk-ant-...
GITHUB_TOKEN=ghp_...
```

Gitignored. Copied to Sprite env on setup.

### `.wisp/config.yaml`

```yaml
limits:
  max_iterations: 50
  max_budget_usd: 20.00
  max_duration_hours: 4
  no_progress_threshold: 3
```

### `.wisp/settings.json`

Claude Code settings. Copied to Sprite's `~/.claude/settings.json`.

```json
{
  "permissions": {
    "deny": [
      "Read(~/.ssh/**)", "Edit(~/.ssh/**)",
      "Read(~/.aws/**)", "Edit(~/.aws/**)",
      "Read(~/.config/gh/**)", "Edit(~/.config/gh/**)",
      "Read(**/.env)", "Edit(**/.env)",
      "Read(**/.env.*)", "Edit(**/.env.*)"
    ]
  },
  "mcpServers": {
    "playwright": {
      "command": "npx",
      "args": ["@anthropic/mcp-playwright"]
    }
  }
}
```

Deny rules take precedence over `--dangerously-skip-permissions`. User editable — can remove Playwright, add deny paths, add MCP servers.

### `.wisp/sessions/<branch>/session.yaml`

```yaml
repo: electric-sql/electric
spec: docs/rfc.md
siblings:
  - TanStack/db
checkpoint: null
branch: wisp/feat-auth
sprite_name: wisp-a1b2c3
started_at: 2026-01-16T10:00:00Z
status: running
```

Created on `wisp start`. Read on `wisp resume`.

## State protocol

### tasks.json

```json
[
  {
    "category": "setup|feature|bugfix|refactor|test|docs",
    "description": "human-readable task description",
    "steps": ["step 1", "step 2"],
    "passes": false
  }
]
```

Agent marks `passes: true` on completion. One commit per task.

### state.json

```json
{
  "status": "CONTINUE|DONE|NEEDS_INPUT|BLOCKED",
  "summary": "what just happened",
  "question": "if NEEDS_INPUT",
  "error": "if BLOCKED",
  "verification": {
    "method": "tests|typecheck|build|manual|none",
    "passed": true,
    "details": "12 tests passed"
  }
}
```

### history.json

Rolling window of last N iterations:

```json
[
  {"iteration": 5, "summary": "...", "tasks_completed": 2, "status": "CONTINUE"},
  {"iteration": 6, "summary": "...", "tasks_completed": 3, "status": "CONTINUE"}
]
```

Used for stuck detection.

### Status values

| Status | Meaning | Wisp action |
|--------|---------|-------------|
| `CONTINUE` | work in progress | run next iteration |
| `DONE` | agent claims completion | verify all tasks pass, then complete |
| `NEEDS_INPUT` | agent needs human answer | pause, surface question in TUI |
| `BLOCKED` | agent cannot proceed | stop, notify |

## Loop mechanics

### Claude invocation

```bash
claude -p "$(cat iterate.md)" \
  --append-system-prompt-file context.md \
  --dangerously-skip-permissions \
  --output-format stream-json \
  --max-turns 100 \
  --max-budget-usd <from config>
```

### Per-iteration flow

```
1. invoke Claude with static prompt
2. stream stdout for TUI display (tool calls, text)
3. wait for Claude exit
4. read state.json from Sprite (source of truth)
5. sync state files to local .wisp/
6. check exit conditions
7. if CONTINUE: goto 1
```

### Exit conditions

Loop terminates when:
- `status == DONE` AND all tasks have `passes: true`
- `status == NEEDS_INPUT` — pause for human
- `status == BLOCKED` — stop, notify
- max iterations reached
- max budget exceeded
- max duration exceeded
- no task progress for N iterations (stuck)
- Claude crashes without writing state.json → BLOCKED

### Stuck detection

Track `tasks_completed` count in history.json. If unchanged for `no_progress_threshold` iterations → set BLOCKED, stop, notify.

### NEEDS_INPUT flow

1. Agent writes state.json with `status: "NEEDS_INPUT"` and `question`
2. Wisp pauses loop, displays question in TUI
3. User types response
4. Wisp writes response to `.wisp/response.json` in Sprite
5. Wisp invokes next iteration
6. Agent reads response.json, deletes it, continues

## TUI implementation

Minimal approach using `golang.org/x/term` for raw input + ANSI escapes for display.

### Summary view

```
┌──────────────────────────────────────────┐
│ wisp: feat-auth | iteration 5/50         │
│ status: RUNNING | tasks: 3/7 complete    │
│ last: "Implemented JWT validation"       │
│                                          │
│ [t]ail [a]ttach [k]ill [esc]background   │
└──────────────────────────────────────────┘
```

Refreshes on state change (after each iteration).

### Tail view

Streams Claude's stdout. Press `t` or `d` to return to summary.

### NEEDS_INPUT view

Shows question, text input field. Enter submits response.

### Attach

Shells out to `sprite console`. Returns to summary on exit.

### Notifications

- **TUI active:** visual update + terminal bell (`\a`)
- **Backgrounded:** macOS notification (`osascript`) + update local session status

## Git workflow

### Branch naming

Default: `wisp/<slug>` derived from spec filename. Override with `--branch`.

### Commits

Agent commits after each task completion:
- Descriptive message
- One task = one commit
- Clean history, no orchestrator artifacts

### PR creation (`wisp done`)

1. Verify all tasks have `passes: true`
2. Push branch to remote
3. Mode `p`: agent generates title/body, `gh pr create`
4. Mode `m`: open `https://github.com/<org>/<repo>/compare/<branch>` in browser
5. Teardown Sprite

## Error handling

All failures stop and notify. No automatic recovery.

| Failure | Detection | Action |
|---------|-----------|--------|
| Agent stuck | no task progress for N iterations | BLOCKED, stop, notify |
| Claude crash | no state.json after exit | BLOCKED, stop, notify |
| Sprite dies | process exit | stop, notify |
| Max iterations | counter | stop, notify |
| Max budget | Claude reports | stop, notify |
| Max duration | wall clock | stop, notify |

Human investigates via `wisp attach`, fixes, `wisp resume`.

## Testing

### Unit tests

- Config loading and validation
- State file parsing (tasks.json, state.json, session.yaml)
- Session management logic
- Stuck detection logic
- Sprite name generation

### Integration tests

Run against real Sprites:
- Full `init` → `start` → `done` flow
- NEEDS_INPUT pause/resume cycle
- `update` with RFC changes
- `review` with feedback
- State sync on task completion
- Sprite failure handling
- Backgrounded notification

Use Go stdlib `testing`.

## Implementation notes

### Dependencies

- `github.com/superfly/sprites-go` — Sprite SDK
- `golang.org/x/term` — raw terminal mode

### Sprites SDK usage

```go
client := sprites.New(os.Getenv("SPRITE_TOKEN"))

// create
sprite, err := client.CreateSprite(ctx, name, nil)

// execute
cmd := sprite.Command("claude", "-p", "...")
cmd.Dir = "/home/sprite/org/repo"
cmd.Env = envVars
stdout, _ := cmd.StdoutPipe()
cmd.Start()
// stream stdout for TUI
cmd.Wait()

// file transfer
cmd := sprite.Command("bash", "-c", "cat > /path/to/file")
stdin, _ := cmd.StdinPipe()
cmd.Start()
stdin.Write(content)
stdin.Close()
cmd.Wait()

// read file
cmd := sprite.Command("cat", "/path/to/file")
output, _ := cmd.Output()

// attach
// shell out to: sprite console <name>

// teardown
client.DeleteSprite(ctx, name)
```

### File paths

Sprite working directory: `/home/sprite/<org>/<repo>/`
Wisp state in Sprite: `/home/sprite/<org>/<repo>/.wisp/`
Claude settings: `/home/sprite/.claude/settings.json`

## Open items

None. All PRD open questions resolved:
- Credential protection: Claude settings.json deny rules
- MCP extensibility: user-editable settings.json
- Template structure: context.md, create-tasks.md, update-tasks.md, review-tasks.md, iterate.md
