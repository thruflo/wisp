# Ralph in Practice: A Primer on the Converged Orchestration Pattern

## Overview

“Ralph” (the bash-loop autonomous coding agent pattern) works reliably only when treated as a **controlled system**, not a naive infinite loop. Over the last months, practitioners have independently converged on the same architecture: an **external controller**, **explicit agent state files**, **file-based human I/O**, **snapshot-based execution reset**, and **attachable shells for reality**. This document explains *why* this pattern exists, *what* it consists of, and *how* it is typically implemented.

---

## Why Ralph Needs a Controller

A naive loop (`while true; do claude ...; done`) fails quickly due to:

* runaway loops,
* false “DONE” claims,
* inability to detect when human input is required,
* environment drift across long runs,
* lack of observability and intervention points.

The key insight is that **the loop must be dumb, and the protocol must be strict**. Control moves out of the agent and into an external orchestrator that treats the agent as a step function.

---

## Core Principle

> Ralph succeeds when the agent is **stateless**, the controller is **stateful**, and all continuity is made explicit in files.

---

## 1. External Ralph Controller

**What**
A process outside the agent that:

* starts and stops agent steps,
* enforces limits (steps, time, cost),
* decides whether to continue, pause, or terminate,
* owns recovery, retries, and escalation.

**Why**
LLMs cannot reliably self-govern termination or safety. External control prevents infinite loops and allows deterministic behavior.

**How**
The controller runs a loop roughly equivalent to:

```
RESET ENV
LOAD STATE
RUN ONE AGENT STEP
READ STATE ARTIFACT
→ DONE | CONTINUE | NEEDS_INPUT | BLOCKED
```

The controller never infers state from logs alone.

---

## 2. Explicit Agent State Files (The Breakthrough)

**What**
After *every* step, the agent must emit a machine-readable state file, e.g.:

```json
{
  "status": "CONTINUE | DONE | NEEDS_INPUT | BLOCKED",
  "summary": "...",
  "question": "...",
  "evidence": [...]
}
```

**Why**

* Log scraping is brittle.
* LLM prose is ambiguous.
* Explicit state makes the system resumable, debuggable, and automatable.

**How**
The agent prompt *requires* writing this file as the final action of each step. Absence of the file is treated as failure.

---

## 3. File-Based Human I/O (Human as Event Source)

**What**
Human interaction is handled via files, not chat:

* Agent writes `outbox/request.json`
* Human (or UI) writes `inbox/response.json`
* Controller resumes next step

**Why**

* Works without TTY
* Auditable and replayable
* Async-friendly
* Avoids unreliable “did the agent ask a question?” heuristics

**How**
When `status == NEEDS_INPUT`, the controller pauses and waits for a response artifact. The next agent step is instructed to read and consume it.

---

## 4. Snapshot-Based Execution Reset (Clean Reality)

**What**
Each task or major phase runs in a freshly restored execution environment:

* filesystem reset,
* processes restarted,
* no hidden state.

**Why**
“Clean context” is meaningless without clean execution. Environment drift causes nondeterminism, hidden coupling, and unreproducible failures.

**How**
Use fast snapshot/restore systems (e.g. Sprites.dev, Firecracker, Lima):

* restore baseline,
* clone repo / mount state,
* run one step,
* discard or restore again.

This makes “fresh VM per task” practical and cheap.

---

## 5. Attachable Shells for Reality (Escape Hatch)

**What**
Every run happens inside an attachable shell/session (tmux, sprite console, etc.).

**Why**
Some failures are irreducibly interactive:

* blocked CLI prompts,
* unexpected tool behavior,
* debugging when the model goes off-script.

Pure automation without an escape hatch is fragile.

**How**
The controller launches the agent step in a detachable session. Humans can attach, intervene, and detach without breaking the run.

---

## 6. Where “Context” Actually Lives

* **Not** in chat history.
* **Not** in the agent’s memory.
* **Yes** in:

  * repo files,
  * explicit state artifacts,
  * run-scoped ledgers.

Agents are instructed to assume zero memory beyond files. This makes fresh sessions viable and reliable.

---

## What This Enables

* Deterministic long-running work
* Human-in-the-loop only when needed
* Parallel agent runs without chaos
* Reproducibility and auditability
* Independence from specific LLM vendors or CLIs

---

## What This Is Not (Yet)

* Not a single polished product
* Not “fully automatic” without protocols
* Not safe without isolation

It is a **converged pattern**, discovered independently by practitioners who run Ralph at scale.

---

## References

1. frankbria/ralph-claude-code — [https://github.com/frankbria/ralph-claude-code](https://github.com/frankbria/ralph-claude-code)
2. mikeyobrien/ralph-orchestrator — [https://github.com/mikeyobrien/ralph-orchestrator](https://github.com/mikeyobrien/ralph-orchestrator)
3. KLIEBHAN/ralph-loop — [https://github.com/KLIEBHAN/ralph-loop](https://github.com/KLIEBHAN/ralph-loop)
4. Sprites.dev (checkpoint/restore) — [https://sprites.dev](https://sprites.dev)
