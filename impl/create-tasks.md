# Create implementation tasks

Read `docs/prd.md` and `docs/rfc.md`. Break down the RFC into implementation tasks.

## Output format

Write tasks to `impl/tasks.json`:

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

## Guidelines

- **medium scope** — each task fits in the "smart zone" of a coding agent's context. Not too big, not too small. A task should be completable in one focused session.
- **test-driven** — where applicable, include test writing as part of the task steps
- **logical ordering** — tasks should be ordered so dependencies come first
- **concrete steps** — each step should be actionable, not vague
- **categories**:
  - `setup` — project scaffolding, dependencies, CI
  - `feature` — new functionality
  - `test` — test-only tasks (integration tests, test utilities)
  - `refactor` — restructuring without behavior change
  - `docs` — documentation

## Task breakdown approach

1. Start with project setup (go module, dependencies, directory structure)
2. Core types and interfaces
3. Configuration loading
4. Sprite client wrapper
5. State management (local and remote)
6. Individual CLI commands (simplest first)
7. Loop mechanics
8. TUI
9. Integration tests
10. Documentation

Do not include tasks for features marked as non-goals in the PRD.
