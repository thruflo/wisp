# Wisp: Product Requirements Document

> A minimal, opinionated Ralph Wiggum orchestrator for RFC-driven development on Sprites.

## Purpose

Wisp automates RFC implementation using the Ralph Wiggum pattern: autonomous Claude Code loops running in isolated Sprites, with structured human intervention when needed.

A developer points wisp at an RFC; wisp creates a sandboxed environment, generates a task list, runs Claude until completion or blockage, and produces a PR. The result is indistinguishable from manual Claude Code work in git history and artifacts.

## Goals

1. **hands-off RFC implementation**: developer starts a session and returns to a PR
2. **safe autonomy**: code runs in isolated Sprites with credential protection
3. **structured intervention**: clear signals when human input needed, easy attach/respond/detach
4. **minimal surface area**: CLI-only, single-purpose, no configuration sprawl

## Success criteria

- developer can go from RFC to PR with a single command plus occasional intervention
- session state is always recoverable (attach, investigate, continue or kill)
- git history shows clean, per-task commits with no orchestrator artifacts
- RFC divergence is captured locally for post-implementation documentation updates

---

## User workflow

```
1. wisp init                      # one-time: setup .wisp/ templates in repo
2. wisp start docs/my-rfc.md      # new session: Sprite created, tasks generated, loop begins
3. [watch summary view]           # see progress, respond to NEEDS_INPUT inline
4. [t/a/d/k/esc as needed]        # toggle tail, attach, detach, kill, background
5. wisp done <branch>             # confirm completion: PR pushed, Sprite torn down
```

**If RFC changes mid-implementation:**
```
1. [edit RFC locally]
2. wisp update docs/my-rfc.md --branch <branch>   # diff, reconcile tasks, resume
```

**Manual intervention:**
```
1. wisp stop <branch>             # stop session
2. wisp attach <branch>           # edit task list or other files directly
3. wisp resume <branch>           # resume
```

---

## CLI interface

### Commands

| Command | Description |
|---------|-------------|
| `wisp init [--template name]` | setup `.wisp/` directory with prompt templates |
| `wisp start <rfc-path> [--branch <name>]` | new session: fresh Sprite, generate task list, begin loop |
| `wisp resume <branch>` | resume existing session |
| `wisp update <rfc-path> --branch <name>` | diff RFC, reconcile task list, resume session |
| `wisp status [<branch>]` | list sessions or show specific session status |
| `wisp tail <branch>` | stream session output (read-only) |
| `wisp attach <branch>` | interactive session access (can edit files when stopped) |
| `wisp stop <branch>` | gracefully stop session |
| `wisp done <branch>` | confirm completion, push PR, teardown Sprite |

### Interactive mode

`wisp start`, `wisp resume`, and `wisp update` enter an interactive TUI with modes:

**Summary (default)**: high-level status, surfaces NEEDS_INPUT prompts for inline response

**Shortcuts** (when not answering a prompt):
- `t` — toggle tail (full output)
- `a` — attach (interactive Claude session)
- `d` — detach (return to summary)
- `k` — kill session
- `esc` — background (exit wisp, session continues)

---

## Core concepts

### Sessions

- **one Sprite per session** (fresh by default, reusable via `wisp resume`)
- **branch name = session ID**: `wisp/<auto-slug>` or explicit `--branch`
- **multiple concurrent sessions supported** from same repo

### Task list

Generated from RFC at session start. JSON format:

```json
[
  {
    "category": "setup|feature|bugfix|refactor|test|docs",
    "description": "Human-readable task description",
    "steps": ["Step 1", "Step 2"],
    "passes": false
  }
]
```

Agent marks `passes: true` on completion. Commits per task.

### Task list reconciliation

When RFC changes mid-implementation (`wisp update`):

1. wisp copies updated RFC to Sprite
2. wisp diffs old RFC vs new RFC
3. wisp passes diff + current task list to agent (dedicated prompt)
4. agent updates task list: adds "update X to match revised RFC" tasks for invalidated completed work, adds new tasks for new requirements
5. session resumes with updated task list

### Agent state

Agent emits structured state after each iteration:

```json
{
  "status": "CONTINUE | DONE | NEEDS_INPUT | BLOCKED",
  "summary": "What just happened",
  "question": "If NEEDS_INPUT, the question",
  "error": "If BLOCKED, the error"
}
```

Orchestrator reads state file to determine next action.

### Failure handling

All failures → stop and notify. Human attaches to investigate. No automatic recovery.

- agent stuck (no progress): stop, notify
- Sprite dies: stop, notify
- out of credits: stop, notify

---

## Artifacts

### In Sprite (committed or discarded)

- code changes (committed per task to feature branch)
- task list with pass/fail status
- agent state files
- divergence log (changes from original RFC)

### Local (`.wisp/`)

- `templates/` — prompt templates (created by `wisp init`)
- `<slug>-changes.md` — divergence summary (synced on completion)

Developer can use divergence summary to update RFC: "read `.wisp/<slug>-changes.md` and update the RFC".

---

## Infrastructure

### Sprites

- **hardcoded dependency** — no abstraction for other backends
- **fresh Sprite per session** by default
- **base checkpoint** used, polluted through run, discarded on completion
- **fresh Claude context per iteration** (bash loop pattern)

### Security

- `--dangerously-skip-permissions` inside Sprite (safe sandbox)
- **unrestricted network egress**
- **credential file protection**: block read access to `.ssh/`, `.env`, and configured sensitive paths

### Claude Code configuration

- model: Claude Code defaults
- system prompt: from `.wisp/templates/` (copied to Sprite)
- MCP: Playwright required; extensible for additional servers

---

## Non-goals (v1)

- multi-agent coordination
- web UI / dashboard
- team-wide session sharing
- automatic RFC generation
- non-Claude agent support
- budget limits / kill switches
- automatic failure recovery
- per-task system checkpointing

---

## Open questions

1. **credential protection implementation**: exact mechanism for blocking sensitive file reads
2. **MCP extensibility**: configuration format for adding MCP servers beyond Playwright
3. **template structure**: exact prompt template format and required files

---

## Appendix: state machine

```
                    ┌─────────────┐
                    │   START     │
                    └──────┬──────┘
                           │
                           ▼
                   ┌─────────────┐
         ┌─────────│  RUNNING    │◄─────────┐
         │         └──────┬──────┘          │
         │                │                 │
         │    ┌───────────┼───────────┐     │
         │    ▼           ▼           ▼     │
         │ ┌──────┐  ┌─────────┐  ┌───────┐ │
         │ │ DONE │  │ NEEDS   │  │BLOCKED│ │
         │ └──┬───┘  │ INPUT   │  └──┬────┘ │
         │    │      └────┬────┘     │      │
         │    │           │          │      │
         │    ▼           ▼          ▼      │
         │ [human     [human      [human    │
         │  confirms]  responds]   investigates]
         │    │           │          │      │
         │    ▼           └──────────┴──────┘
         │ ┌──────┐
         │ │  PR  │
         │ │PUSHED│
         │ └──┬───┘
         │    │
         ▼    ▼
      ┌─────────────┐
      │  TEARDOWN   │
      └─────────────┘
```
