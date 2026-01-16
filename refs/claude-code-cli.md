# Claude Code CLI Reference for Wisp Orchestration

> Reference document for invoking Claude Code programmatically in the Wisp orchestrator.

---

## Non-Interactive Execution

### Print Mode (`-p` / `--print`)

The `-p` flag enables non-interactive (headless) execution. Claude processes the prompt and exits upon completion.

```bash
claude -p "Your task here"
```

This is **required** for automation. Without `-p`, Claude enters interactive REPL mode.

### Autonomous Mode (`--dangerously-skip-permissions`)

Bypasses ALL permission prompts, enabling fully unattended execution:

```bash
claude -p --dangerously-skip-permissions "Implement the auth module"
```

**What it enables:**
- File creation, editing, and deletion without confirmation
- Bash command execution without approval
- Full tool usage without human oversight

**Safety considerations:**
- Use ONLY in isolated environments (Sprites)
- Claude may modify files outside intended scope ("scope creep")
- No opportunity to review/reject individual operations

**Alternative: Granular tool approval** with `--allowedTools`:
```bash
claude -p --allowedTools "Bash(git:*),Read,Edit" "Fix the tests"
```

---

## Output Formats

### Text (default)
```bash
claude -p "Summarize README.md"
# Returns: plain text response
```

### JSON
```bash
claude -p "Summarize README.md" --output-format json
```

Response structure:
```json
{
  "result": "...",
  "session_id": "...",
  "usage": {...},
  "metadata": {...}
}
```

### Streaming JSON (for real-time monitoring)
```bash
claude -p "Implement feature X" --output-format stream-json
```

Emits **newline-delimited JSON** (JSONL). Each line is a complete JSON object.

---

## Stream-JSON Event Types

When using `--output-format stream-json`, Claude emits these event types:

### 1. Init Event (session start)
```json
{"type":"system","subtype":"init","session_id":"...","tools":[...]}
```

### 2. Assistant Message
```json
{"type":"assistant","message":{"content":[{"type":"text","text":"..."}]}}
```

Or with tool use:
```json
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"...","name":"Bash","input":{"command":"..."}}]}}
```

### 3. User Message (tool results)
```json
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"...","content":"..."}]}}
```

### 4. Result Event (completion)
```json
{"type":"result","subtype":"success","session_id":"...","cost_usd":2.90,"num_turns":15}
```

**Detecting completion:** Wait for the `{"type":"result",...}` event. This signals Claude has finished.

**Known issue:** In some versions, the final `result` event may be missing. Implement a timeout fallback if stdout closes without a result event.

---

## System Prompt Configuration

### CLAUDE.md Files (filesystem-based)

Claude automatically reads CLAUDE.md files at session start:

| Location | Scope |
|----------|-------|
| `~/.claude/CLAUDE.md` | Global (all projects) |
| `./CLAUDE.md` | Project root |
| `./subdir/CLAUDE.md` | Subdirectory (loaded when working there) |

**For Wisp:** Copy `.wisp/templates/CLAUDE.md` to Sprite's working directory before invoking Claude.

### CLI Flags

**Append to default prompt (recommended):**
```bash
claude -p --append-system-prompt "You are implementing an RFC. Output structured state after each task." "..."
```

**Replace entire prompt:**
```bash
claude -p --system-prompt "Custom prompt replacing all defaults" "..."
```

**Load from file:**
```bash
claude -p --append-system-prompt-file ./prompt.txt "..."
claude -p --system-prompt-file ./prompt.txt "..."
```

---

## Model Selection

```bash
claude -p --model claude-sonnet-4-5-20250929 "..."
claude -p --model opus "..."   # alias
claude -p --model sonnet "..." # alias
```

**Fallback model** (if primary overloaded):
```bash
claude -p --model opus --fallback-model sonnet "..."
```

---

## Turn and Budget Limits

**Limit agentic turns** (prevents runaway loops):
```bash
claude -p --max-turns 10 "..."
```

**Limit spend:**
```bash
claude -p --max-budget-usd 5.00 "..."
```

---

## Session Management

### Session IDs

Each conversation has a UUID session ID. Use for resumption:

```bash
# Capture session ID
session_id=$(claude -p "Start task" --output-format json | jq -r '.session_id')

# Resume later
claude -p --resume "$session_id" "Continue where you left off"
```

### Continue Most Recent
```bash
claude -p --continue "Continue the previous task"
```

### Disable Persistence
```bash
claude -p --no-session-persistence "One-shot task"
```

---

## Environment Variables

### Authentication
| Variable | Purpose |
|----------|---------|
| `ANTHROPIC_API_KEY` | Primary API key (pay-as-you-go billing) |
| `ANTHROPIC_AUTH_TOKEN` | Alternative auth method |

### Model Configuration
| Variable | Purpose |
|----------|---------|
| `ANTHROPIC_MODEL` | Default model for complex tasks |
| `ANTHROPIC_SMALL_FAST_MODEL` | Model for simple operations |
| `ANTHROPIC_BASE_URL` | Custom API endpoint |

### Cloud Providers
| Variable | Purpose |
|----------|---------|
| `CLAUDE_CODE_USE_BEDROCK=1` | Use AWS Bedrock |
| `CLAUDE_CODE_USE_VERTEX=1` | Use Google Vertex AI |
| `AWS_REGION`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` | Bedrock credentials |
| `ANTHROPIC_VERTEX_PROJECT_ID` | GCP project for Vertex |

### Behavioral Controls
| Variable | Purpose |
|----------|---------|
| `CLAUDE_CODE_MAX_OUTPUT_TOKENS` | Response token limit |
| `BASH_MAX_TIMEOUT_MS` | Command execution timeout |
| `BASH_DEFAULT_TIMEOUT_MS` | Default bash timeout |

### Debugging
| Variable | Purpose |
|----------|---------|
| `DEBUG=1` | Enable verbose logging |
| `CLAUDE_CODE_ENABLE_TELEMETRY=0` | Disable telemetry |

---

## Tool Control

### Restrict Available Tools
```bash
claude -p --tools "Bash,Edit,Read" "..."
```

### Auto-Approve Specific Tools
```bash
claude -p --allowedTools "Bash,Read,Edit" "..."
```

### Granular Tool Permissions
```bash
claude -p --allowedTools "Bash(git:*)" "Bash(npm:*)" "Read" "Edit" "..."
```

### Disallow Tools
```bash
claude -p --disallowedTools "WebSearch" "..."
```

---

## Structured Output

Force JSON output conforming to a schema:

```bash
claude -p --output-format json \
  --json-schema '{"type":"object","properties":{"status":{"type":"string"},"summary":{"type":"string"}},"required":["status","summary"]}' \
  "Analyze the codebase"
```

Response includes `structured_output` field with schema-conforming data.

---

## Complete Example for Wisp

```bash
# Single iteration in the Ralph loop
claude -p \
  --dangerously-skip-permissions \
  --output-format stream-json \
  --max-turns 50 \
  --max-budget-usd 10.00 \
  --append-system-prompt-file /path/to/wisp-prompt.md \
  "Read the task list at .wisp/tasks.json. Complete the next pending task. After completion, update the task status and emit your state to .wisp/state.json with format: {status: CONTINUE|DONE|NEEDS_INPUT|BLOCKED, summary: string, question?: string, error?: string}"
```

**Orchestrator behavior:**
1. Parse JSONL stream for progress monitoring
2. Wait for `{"type":"result",...}` event (or timeout)
3. Read `.wisp/state.json` to determine next action
4. If `CONTINUE`: invoke next iteration
5. If `DONE`: proceed to PR creation
6. If `NEEDS_INPUT`: surface question to user
7. If `BLOCKED`: stop and notify

---

## Additional Flags Reference

| Flag | Purpose |
|------|---------|
| `--add-dir ../other` | Add additional working directories |
| `--verbose` | Show full turn-by-turn output |
| `--debug "api,mcp"` | Enable debug logging by category |
| `--mcp-config ./mcp.json` | Load MCP server configuration |
| `--version` | Show version |

---

## Sources

- [Official CLI Reference](https://code.claude.com/docs/en/cli-reference)
- [Headless/Programmatic Usage](https://code.claude.com/docs/en/headless)
- [Claude Code GitHub Repository](https://github.com/anthropics/claude-code)
- [CLAUDE.md Configuration](https://claude.com/blog/using-claude-md-files)
- [Best Practices for Agentic Coding](https://www.anthropic.com/engineering/claude-code-best-practices)
- [Agent SDK Documentation](https://platform.claude.com/docs/en/agent-sdk/overview)
