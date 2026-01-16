# Ralph Wiggum & Sprites: Context Primer

> Briefing document for PRD development of a Ralph orchestrator system using Sprites.

---

## Table of Contents

1. [What is Ralph Wiggum?](#what-is-ralph-wiggum)
2. [What are Sprites?](#what-are-sprites)
3. [The Pure Bash Loop Pattern](#the-pure-bash-loop-pattern)
4. [Orchestrator Layer Functions](#orchestrator-layer-functions)
5. [Key Concepts & Terminology](#key-concepts--terminology)
6. [Implementation Patterns](#implementation-patterns)
7. [Reference Links](#reference-links)

---

## What is Ralph Wiggum?

Ralph Wiggum is a **technique** (not a tool) for running AI coding agents in continuous autonomous loops until task completion. Named after the Simpsons character who is perpetually confused but persistent, it embodies iterative improvement over single-shot perfection.

### Core Philosophy

- **Iteration beats perfection** — Don't aim for perfect on first try; let the loop refine the work
- **Deterministically bad in an undeterministic world** — Failures become predictable and informative when looped
- **Eventual consistency** — Faith that repeated attempts will converge on correct solutions
- **Failures are data** — Each failure teaches what guardrails to add

### Origin

Created by Geoffrey Huntley (ghuntley.com). In July 2025, Anthropic's Boris Cherny formalized the technique into an official Claude Code plugin. The technique works with any CLI agent (Claude Code, Codex, Gemini CLI, etc.).

### What Ralph Does

Ralph solves the "human-in-the-loop bottleneck" — where AI agents stop after every action waiting for human input. Instead:

1. Agent works on task
2. Agent tries to exit
3. Stop mechanism blocks exit
4. Same prompt fed back
5. Repeat until completion condition met

---

## What are Sprites?

Sprites are **stateful sandbox environments** from Fly.io — persistent Linux VMs with checkpoint/restore capabilities designed for running arbitrary code, including AI coding agents.

### Core Properties

| Property | Description |
|----------|-------------|
| **Instant creation** | ~1-2 seconds to create and shell into |
| **Persistent storage** | 100GB durable ext4 filesystem per Sprite |
| **Auto-hibernation** | Sleep after ~30s inactivity (no compute cost while dormant) |
| **Fast wake** | Wake instantly when needed (<1s cold start for HTTP) |
| **Checkpoint/Restore** | ~300ms checkpoints; full environment snapshots |
| **Hardware isolation** | Firecracker VMs with isolated networking |
| **HTTP access** | Each Sprite gets a unique URL (port 8080 → public) |

### Technical Architecture

- **Compute**: Firecracker micro-VMs with up to 8 vCPUs, 8GB RAM
- **Storage**: S3-compatible object storage as root, NVMe as read-through cache
- **Networking**: Layer 3 network policies, configurable egress rules
- **Billing**: Pay only for actual CPU cycles, resident memory, and written storage blocks

### Pre-installed Tools

Sprites come with Claude Code, Gemini CLI, Codex, Python 3.13, Node.js 22.x, and other common dev tools.

### Why Sprites for Ralph

- Safe sandbox for `--dangerously-skip-permissions` mode
- Persistent filesystem survives hibernation (agent resumes where it left off)
- Checkpoints enable rollback on failures
- Network isolation prevents prompt injection attacks from causing external harm
- Cost-effective for intermittent/burst workloads

---

## The Pure Bash Loop Pattern

The canonical Ralph implementation in its simplest form:

```bash
while :; do cat PROMPT.md | claude-code; done
```

### How It Works

1. **PROMPT.md** contains the task description and success criteria
2. **Agent executes** against the prompt
3. **Agent state persists** via git history and modified files
4. **Loop restarts** with fresh context but same filesystem
5. **Completion detected** when a "completion promise" is emitted

### Fresh Context Per Iteration

The bash loop approach starts a **fresh context window** for each iteration. This is architecturally different from the Claude Code plugin (which runs in a single context window). Fresh context:

- Prevents context window exhaustion
- Allows longer-running tasks
- Each iteration sees previous work via filesystem/git, not conversation history

### Stop Conditions

The loop terminates when:

- Max iterations reached
- Max runtime exceeded
- Completion promise detected (e.g., `<promise>COMPLETE</promise>`)
- Cost/token limits hit
- Too many consecutive errors

### Completion Promise Pattern

```markdown
When all tasks complete:
- Output: <promise>DONE</promise>
```

The orchestrator watches stdout for this exact string.

---

## Orchestrator Layer Functions

An orchestrator wraps the basic bash loop with management capabilities.

## Key Concepts & Terminology

### Ralph-Specific

| Term | Definition |
|------|------------|
| **Completion promise** | A specific string the agent outputs to signal task completion |
| **Stop hook** | Mechanism that intercepts agent exit attempts |
| **HITL (Human-in-the-Loop)** | Running Ralph with manual oversight per iteration |
| **AFK (Away From Keyboard)** | Running Ralph autonomously without supervision |
| **Backpressure** | Verification mechanisms that force agent to meet quality standards |
| **Tuning** | Iteratively refining prompts based on failures |

### Sprites-Specific

| Term | Definition |
|------|------------|
| **Checkpoint** | Snapshot of entire Sprite state (~300ms to create) |
| **Restore** | Roll back to a previous checkpoint |
| **Hibernation** | Sprite releases compute but retains storage |
| **Wake** | Sprite receives new compute, resumes with same filesystem |
| **Network policy** | L3 rules controlling which domains the Sprite can access |

### Orchestration Patterns

| Pattern | Description |
|---------|-------------|
| **3-Phase Ralph** | Define → Plan → Build workflow |
| **Two-Prompt System** | PLANNING prompt (gap analysis, TODO list) + BUILDING prompt (implement, test, commit) |
| **Task-picking** | Agent chooses next task from PRD rather than human specifying |
| **Pass/fail tasks** | JSON task format with `passes` field for verification |

---

## Implementation Patterns

### Project Structure (Common Pattern)

```
project/
├── PROMPT.md           # Main instructions for Ralph
├── @fix_plan.md        # Prioritized task list (@ = control file)
├── @AGENT.md           # Build/run instructions
├── specs/              # Requirements and specifications
├── .agent/             # Orchestrator workspace
│   ├── prompts/        # Prompt history
│   ├── checkpoints/    # Checkpoint markers
│   ├── metrics/        # Execution metrics
│   ├── plans/          # Planning documents
│   ├── memory/         # Agent memory
│   └── scratchpad.md   # Context persistence (ACP)
└── logs/               # Execution logs
```

### PROMPT.md Structure (Example)

```markdown
# Task: [Description]

## Context
@plan.md @activity.md

## Instructions
1. Read activity.md for recent work
2. Start local server
3. Pick highest-priority failing task from plan.md
4. Implement and verify
5. Update plan.md when complete

## Completion
Output <promise>COMPLETE</promise> when all tasks pass
```

### Task List Format (Anthropic-Recommended)

```json
{
  "category": "feature",
  "description": "User authentication",
  "steps": ["Write tests", "Implement JWT", "Add validation"],
  "passes": false
}
```

### Sprites + Ralph Integration Points

1. **Create Sprite** → Initialize project structure
2. **Checkpoint** → Before risky operations, at intervals
3. **Run Ralph loop** → Inside Sprite with `--dangerously-skip-permissions`
4. **Network policy** → Restrict to allowed domains (LLMs, package registries)
5. **Restore** → On catastrophic failure, roll back and adjust prompt
6. **HTTP access** → Expose running app for verification

---

## Reference Links

### Primary Sources

| Resource | URL |
|----------|-----|
| Ralph Wiggum (Origin) | https://ghuntley.com/ralph/ |
| Sprites Homepage | https://sprites.dev |
| Sprites Design & Implementation | https://fly.io/blog/design-and-implementation |

### Ralph Implementations

| Resource | URL |
|----------|-----|
| frankbria/ralph-claude-code | https://github.com/frankbria/ralph-claude-code |
| mikeyobrien/ralph-orchestrator | https://github.com/mikeyobrien/ralph-orchestrator |
| Anthropic Official Plugin | https://github.com/anthropics/claude-code/tree/main/plugins/ralph-wiggum |
| vercel-labs/ralph-loop-agent | https://github.com/vercel-labs/ralph-loop-agent |
| ghuntley/how-to-ralph-wiggum | https://github.com/ghuntley/how-to-ralph-wiggum |

### Guides & Documentation

| Resource | URL |
|----------|-----|
| JeredBlu Ralph Guide | https://github.com/JeredBlu/guides/blob/main/Ralph_Wiggum_Guide.md |
| AwesomeClaude Ralph Reference | https://awesomeclaude.ai/ralph-wiggum |
| AI Hero Tips for Ralph | https://www.aihero.dev/tips-for-ai-coding-with-ralph-wiggum |

### Sprites SDKs

| Language | URL |
|----------|-----|
| CLI | `curl https://sprites.dev/install.sh \| bash` |
| JavaScript | `@fly/sprites` (npm) |
| Go | Official SDK (see docs.sprites.dev) |
| Python | https://github.com/superfly/sprites-py |
| Rust | https://github.com/m-mohamed/sprites-rs |
| Elixir | Official SDK (see docs.sprites.dev) |

### Sprites API

| Endpoint | Purpose |
|----------|---------|
| `PUT /v1/sprites/{name}` | Create a Sprite |
| `POST /v1/sprites/{name}/exec` | Execute command |
| `WSS /v1/sprites/{name}/exec` | WebSocket exec (TTY support) |
| `POST /v1/sprites/{name}/checkpoints` | Create checkpoint |
| `POST /v1/sprites/{name}/restore` | Restore to checkpoint |

### Additional Context

| Resource | URL |
|----------|-----|
| VentureBeat Coverage | https://venturebeat.com/technology/how-ralph-wiggum-went-from-the-simpsons-to-the-biggest-name-in-ai-right-now |
| Simon Willison on Sprites | https://simonwillison.net/2026/Jan/9/sprites-dev/ |
| mikeyobrien/ralph-orchestrator docs | https://mikeyobrien.github.io/ralph-orchestrator/ |

---

## Quick Reference: Key Decisions for PRD

When designing a Ralph orchestrator with Sprites, consider:

### Loop Architecture
- Single context vs fresh context per iteration?
- Plugin-based (Claude Code native) vs bash loop (any agent)?

### State Management
- How frequently to checkpoint?
- When to auto-restore vs require human intervention?
- How to persist agent context across iterations?

### Exit Detection
- What completion signals to watch for?
- How to detect stuck/no-progress conditions?
- Max iteration/time/cost limits?

### Agent Configuration
- Which agents to support?
- Permission model (auto-approve, allowlist, etc.)?
- Network policy (which domains allowed)?

### Backpressure/Verification
- What tests/checks must pass?
- How to prevent agent "cheating" on completion?
- LLM-as-judge for subjective criteria?
