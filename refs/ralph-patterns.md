# Ralph loop implementation patterns

> Reference patterns for wisp, synthesized from multiple Ralph implementations.

## Core principle

> The agent is stateless, the controller is stateful, all continuity lives in files.

This means:
- fresh Claude context per iteration (bash loop, not plugin)
- explicit state artifacts after every step
- disk as the handoff mechanism between iterations

## Task list format

Wisp uses JSON for task lists (richer than markdown checkboxes):

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

**Why JSON over markdown:**
- machine-parseable without regex
- richer metadata (category, steps array)
- consistent with PRD schema
- easier reconciliation on RFC updates

## State file protocol

Agent emits structured state after each iteration:

```json
{
  "status": "CONTINUE | DONE | NEEDS_INPUT | BLOCKED",
  "summary": "what just happened",
  "question": "if NEEDS_INPUT, what the agent needs answered",
  "error": "if BLOCKED, the error description",
  "files_modified": 3,
  "tests_run": 12,
  "tests_passed": 12
}
```

Controller reads this file to determine next action. Absence of file = failure.

## Filesystem layout

```
.wisp/
  templates/
    default/
      system.md           # system prompt for Claude
      create-tasks.md     # RFC-to-task conversion prompt
      update-tasks.md     # RFC reconciliation prompt
    :name/                # custom template sets
      ...
  sessions/
    :branch-name/
      session.json        # {session_id, branch, started_at, loop_count}
      state.json          # latest agent state (status, summary, etc.)
      tasks.json          # task list
      history.json        # rolling last N loop results
      divergence.md       # RFC drift log
```

**Key points:**
- one session directory per branch (concurrent sessions supported)
- template sets are swappable via `--template`
- state files are atomic outputs, not append logs

## Loop logic

### Basic flow

```
1. restore Sprite to checkpoint
2. load session state
3. inject context (loop count, task summary, previous state)
4. run one Claude step
5. read state artifact
6. check exit conditions
7. sleep, repeat
```

### Exit conditions

Loop terminates when:
- `status == DONE` (success)
- `status == NEEDS_INPUT` (pause for human)
- `status == BLOCKED` (failure, human investigates)
- max iterations reached (safety limit)
- idle timeout (no progress detection)

### Completion detection

**Dual-condition gate** (prevents premature exit):
1. agent explicitly signals `DONE` status
2. all tasks have `passes: true`

Both conditions required. Agent saying "done" while tasks remain incomplete = continue.

### Stuck loop detection

Track rolling window of last N iterations:
- 3+ iterations with no task progress → likely stuck
- 3+ iterations modifying same files → possible loop
- 5+ repeated similar errors → escalate to human

When stuck: set `status: BLOCKED`, stop, notify human.

## Prompt structure

### System prompt (templates/default/system.md)

- project context and architecture overview
- quality gates (tests must pass, lint clean)
- one task per iteration rule
- state file output requirement

### Per-iteration injection

Controller injects before each step:
- current loop number
- incomplete task count
- previous iteration summary (truncated)
- any human response (if resuming from NEEDS_INPUT)

### Completion promise

Agent must output when all tasks complete:

```
<promise>COMPLETE</promise>
```

Controller detects this string to confirm genuine completion (not just "I'm done with this task").

## Human-in-the-loop

### File-based I/O

When `status == NEEDS_INPUT`:
1. agent writes question to state file
2. controller pauses, surfaces question to human
3. human responds (via TUI or file)
4. response written to `response.json`
5. next iteration instructed to read response

### Attachable sessions

Every run happens in attachable shell (tmux/Sprite console):
- human can attach to debug
- can edit files directly when paused
- detach without breaking session

## Patterns to use

1. **fresh context per iteration** — prevents context bloat and hallucination drift
2. **structured state files** — machine-readable, not log scraping
3. **dual-condition exit** — prevents premature "done" claims
4. **rolling window tracking** — detect stuck loops without unbounded history
5. **file-based human I/O** — async-friendly, auditable
6. **one task per commit** — clean git history
7. **max iterations guard** — cost protection

## Patterns to avoid

1. **plugin/single-context loops** — context bloat causes drift
2. **markdown task checkboxes** — harder to parse, less metadata
3. **log scraping for state** — brittle, ambiguous
4. **automatic recovery** — hidden failures compound; stop and notify
5. **unbounded history** — memory grows forever; use rolling windows

## References

1. frankbria/ralph-claude-code — circuit breaker, dual-condition exit
2. mikeyobrien/ralph-orchestrator — hat system, config-driven guards
3. JeredBlu/Ralph_Wiggum_Guide — JSON tasks, bash loop pattern
4. KLIEBHAN/ralph-loop — early reference implementation
