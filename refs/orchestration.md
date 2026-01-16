# Orchestration architecture

> How wisp coordinates Claude Code loops in isolated Sprites.

## Core principle

> The loop must be dumb, and the protocol must be strict.

Control lives in the orchestrator, not the agent. The agent is a step function that reads files, does work, and writes files.

---

## Components

### Controller (wisp CLI)

External process that:
- starts and stops agent steps
- enforces limits (iterations, time)
- reads state artifacts to decide next action
- owns recovery, escalation, and human notification

The controller never infers state from logs alone.

### Agent (Claude Code)

Stateless executor that:
- reads task list and previous state
- does one task worth of work
- writes state artifact as final action
- assumes zero memory beyond files

### Sprite (execution environment)

Isolated sandbox that:
- provides fresh filesystem per session
- runs Claude Code with `--dangerously-skip-permissions`
- exposes attachable shell for debugging
- discards state on completion (code committed to git survives)

---

## State protocol

### Files written by agent

| file | purpose |
|------|---------|
| `tasks.json` | task list with pass/fail status |
| `state.json` | iteration result (status, summary, question, error) |

### Files read by controller

| file | action |
|------|--------|
| `state.json` | determine next action based on status |
| `tasks.json` | verify completion (all passes: true) |

### Status values

| status | meaning | controller action |
|--------|---------|-------------------|
| `CONTINUE` | work in progress | run next iteration |
| `DONE` | agent claims completion | verify tasks, then complete |
| `NEEDS_INPUT` | agent needs human answer | pause, surface question |
| `BLOCKED` | agent cannot proceed | stop, notify human |

---

## Loop execution

### Per-iteration flow

```
controller                              sprite
    │                                     │
    │─── restore checkpoint ─────────────►│
    │─── inject context ─────────────────►│
    │─── run claude step ────────────────►│
    │                                     │──► read tasks.json
    │                                     │──► do work
    │                                     │──► commit if task done
    │                                     │──► write state.json
    │◄── read state.json ─────────────────│
    │                                     │
    │─── check exit conditions            │
    │─── sleep, repeat                    │
```

### Context injection

Each iteration receives:
- loop number (e.g., "iteration 7 of max 50")
- incomplete task count
- previous summary (truncated to ~200 chars)
- human response (if resuming from NEEDS_INPUT)

### Exit conditions

Loop terminates when any condition met:
- `status == DONE` AND all tasks pass (success)
- `status == NEEDS_INPUT` (pause)
- `status == BLOCKED` (failure)
- max iterations reached (safety)
- no progress for N iterations (stuck)

---

## Human intervention

### NEEDS_INPUT flow

```
agent writes: state.json { status: "NEEDS_INPUT", question: "..." }
controller:   pauses loop, surfaces question
human:        responds via TUI or writes response.json
controller:   resumes, next iteration reads response
```

### Attach/detach

Human can:
- `wisp attach <branch>` — enter Sprite shell
- edit files, run commands, debug
- `wisp resume <branch>` — continue loop

Session state preserved across attach/detach.

### Manual override

Human can:
- edit tasks.json directly (add/remove/reorder tasks)
- edit state.json (force status change)
- `wisp stop` then `wisp resume` to apply changes

---

## Failure handling

All failures stop and notify. No automatic recovery.

| failure | detection | action |
|---------|-----------|--------|
| agent stuck | no task progress for 3+ iterations | stop, notify |
| repeated errors | same error 5+ times | stop, notify |
| Sprite dies | process exit | stop, notify |
| max iterations | counter | stop, notify |

Human investigates via `wisp attach`, fixes, resumes.

---

## Filesystem layout

See [ralph-patterns.md](./ralph-patterns.md#filesystem-layout) for detailed structure.

Summary:
```
.wisp/
  templates/:name/     # prompt templates
  sessions/:branch/    # per-session state
    session.json       # metadata
    state.json         # latest agent state
    tasks.json         # task list
    history.json       # rolling iteration history
```

---

## Differences from other Ralph implementations

| aspect | other implementations | wisp |
|--------|----------------------|------|
| task format | markdown checkboxes | JSON (richer metadata) |
| state files | multiple dotfiles | consolidated (state.json, tasks.json) |
| recovery | some auto-recovery | stop and notify only |
| human I/O | varies | file-based, async-friendly |
| environment | local or Docker | Sprites (mandatory) |

---

## References

- [ralph-patterns.md](./ralph-patterns.md) — implementation patterns
- [PRD](../docs/prd.md) — product requirements
