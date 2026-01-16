# Implement next task

You are implementing wisp, a Ralph Wiggum orchestrator. Read the project context, then implement the next incomplete task.

## Context

- `docs/rfc.md` — implementation specification
- `AGENTS.md` — project conventions (if present)
- `impl/tasks.json` — task list with completion status

## Instructions

1. Read `impl/tasks.json`. Find the first task where `passes: false`.
2. Implement the task following its steps.
3. Write tests first where applicable. Run tests to verify.
4. Commit your changes with a descriptive message.
5. Update `impl/tasks.json` — set `passes: true` for the completed task.
6. Report what you did.

## Quality standards

- Tests must pass before marking a task complete
- Follow Go conventions (gofmt, effective go)
- Keep implementations minimal — no over-engineering
- One commit per task

## When blocked

If you cannot complete the task (missing dependency, unclear requirement, need human decision):
- Do not mark the task as complete
- Explain what's blocking you
- Stop and wait for guidance

## Output

After completing or getting blocked, report:
- What you implemented
- What tests you wrote/ran
- What's blocking (if anything)
- The commit hash (if you committed)
