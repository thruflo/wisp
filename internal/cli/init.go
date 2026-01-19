package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/thruflo/wisp/internal/config"
)

var initTemplate string
var initForce bool

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize .wisp/ directory structure",
	Long: `Creates the .wisp/ directory with default configuration files and templates.

This command sets up:
  - config.yaml with operational limits
  - settings.json with Claude Code permission deny rules and MCP servers
  - templates/default/ with prompt files for task generation and iteration`,
	RunE: runInit,
}

func init() {
	initCmd.Flags().StringVarP(&initTemplate, "template", "t", "default", "template name to create")
	initCmd.Flags().BoolVarP(&initForce, "force", "f", false, "overwrite existing template")
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	wispDir := filepath.Join(cwd, ".wisp")
	templateDir := filepath.Join(wispDir, "templates", initTemplate)

	wispExists := dirExists(wispDir)
	templateExists := dirExists(templateDir)

	// Handle three cases:
	// 1. .wisp/ doesn't exist -> full initialization
	// 2. .wisp/ exists, template doesn't exist -> create only template
	// 3. .wisp/ exists, template exists -> error unless --force

	if wispExists && templateExists && !initForce {
		return fmt.Errorf("template %q already exists (use --force to overwrite)", initTemplate)
	}

	if !wispExists {
		// Full initialization
		dirs := []string{
			wispDir,
			filepath.Join(wispDir, "sessions"),
			templateDir,
		}

		for _, dir := range dirs {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", dir, err)
			}
		}

		// Write config.yaml
		if err := writeConfigYAML(wispDir); err != nil {
			return err
		}

		// Write settings.json
		if err := writeSettingsJSON(wispDir); err != nil {
			return err
		}

		// Write .sprite.env placeholder
		if err := writeSpriteEnv(wispDir); err != nil {
			return err
		}

		// Write .gitignore
		if err := writeGitignore(wispDir); err != nil {
			return err
		}

		// Write template files
		if err := writeTemplateFiles(wispDir, initTemplate); err != nil {
			return err
		}

		fmt.Printf("Initialized .wisp/ directory with %q template\n", initTemplate)
	} else {
		// Template-only creation (wisp already initialized)
		if err := os.MkdirAll(templateDir, 0755); err != nil {
			return fmt.Errorf("failed to create template directory %s: %w", templateDir, err)
		}

		if err := writeTemplateFiles(wispDir, initTemplate); err != nil {
			return err
		}

		if templateExists && initForce {
			fmt.Printf("Overwrote template %q\n", initTemplate)
		} else {
			fmt.Printf("Created template %q\n", initTemplate)
		}
	}

	return nil
}

// dirExists checks if a directory exists
func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func writeConfigYAML(wispDir string) error {
	content := `# Wisp configuration
# See https://github.com/thruflo/wisp for documentation

limits:
  # Maximum number of Claude iterations before stopping
  max_iterations: 50

  # Maximum budget in USD per session
  max_budget_usd: 20.00

  # Maximum wall-clock time in hours
  max_duration_hours: 4

  # Number of iterations without task progress before marking BLOCKED
  no_progress_threshold: 3
`
	return os.WriteFile(filepath.Join(wispDir, "config.yaml"), []byte(content), 0644)
}

func writeSettingsJSON(wispDir string) error {
	settings := config.Settings{
		Permissions: config.Permissions{
			Deny: []string{
				"Read(~/.ssh/**)", "Edit(~/.ssh/**)",
				"Read(~/.aws/**)", "Edit(~/.aws/**)",
				"Read(~/.config/gh/**)", "Edit(~/.config/gh/**)",
				"Read(~/.config/claude/**)", "Edit(~/.config/claude/**)",
				"Read(**/.env)", "Edit(**/.env)",
				"Read(**/.env.*)", "Edit(**/.env.*)",
			},
		},
		MCPServers: map[string]config.MCPServer{
			"playwright": {
				Command: "npx",
				Args:    []string{"@anthropic/mcp-playwright"},
			},
		},
	}

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal settings: %w", err)
	}

	return os.WriteFile(filepath.Join(wispDir, "settings.json"), data, 0644)
}

func writeSpriteEnv(wispDir string) error {
	content := `# Sprite environment variables (gitignored)
# These are injected into the Sprite environment on setup
#
# Note: Claude authentication uses your local ~/.config/claude/ credentials
# (from 'claude login') which are automatically copied to the Sprite.

GITHUB_TOKEN="..."
SPRITE_TOKEN="..."
`
	return os.WriteFile(filepath.Join(wispDir, ".sprite.env"), []byte(content), 0644)
}

func writeGitignore(wispDir string) error {
	content := `# Credentials
.sprite.env
`
	return os.WriteFile(filepath.Join(wispDir, ".gitignore"), []byte(content), 0644)
}

func writeTemplateFiles(wispDir, templateName string) error {
	templateDir := filepath.Join(wispDir, "templates", templateName)

	templates := map[string]string{
		"context.md":      contextMDContent,
		"create-tasks.md": createTasksMDContent,
		"update-tasks.md": updateTasksMDContent,
		"review-tasks.md": reviewTasksMDContent,
		"iterate.md":      iterateMDContent,
		"generate-pr.md":  generatePRMDContent,
	}

	for name, content := range templates {
		path := filepath.Join(templateDir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Errorf("failed to write template %s: %w", name, err)
		}
	}

	return nil
}

const contextMDContent = `# Wisp Agent Context

You are an autonomous coding agent working on implementing an RFC specification.
Your work is orchestrated by wisp, which manages your iteration loop.

## Quality Standards

- Write clean, idiomatic code following the project's conventions
- Include appropriate error handling
- Write tests where the project supports them
- Keep commits focused and atomic (one task per commit)
- Document significant decisions or deviations

## Verification

Verify your work before marking tasks complete:
- Run tests if the project has a test suite
- Run type checking if applicable (tsc, mypy, etc.)
- Run the build if applicable
- For UI changes, verify visually if tools allow

## Project Context

Read AGENTS.md in the repo root (if present) for project-specific conventions,
build commands, and testing instructions.

## State Files

Wisp manages state through files in .wisp/:
- tasks.json: The task list you're working through
- state.json: Your current status (you write this)
- history.json: Rolling iteration history
- response.json: Human responses to your questions (read and delete)
- divergence.md: Log of RFC deviations (append to this)

## Task Schema

` + "```json" + `
{
  "category": "setup|feature|bugfix|refactor|test|docs",
  "description": "Human-readable task description",
  "steps": ["Step 1", "Step 2"],
  "passes": false
}
` + "```" + `

## State Schema

` + "```json" + `
{
  "status": "CONTINUE|DONE|NEEDS_INPUT|BLOCKED",
  "summary": "What you just accomplished",
  "question": "If NEEDS_INPUT, your question here",
  "error": "If BLOCKED, what's blocking you",
  "verification": {
    "method": "tests|typecheck|build|manual|none",
    "passed": true,
    "details": "Verification output"
  }
}
` + "```" + `

## Status Values

- CONTINUE: Work in progress, more tasks remain
- DONE: All tasks complete, ready for PR
- NEEDS_INPUT: You need a human answer to proceed
- BLOCKED: Cannot continue (missing deps, unclear requirements, etc.)
`

const createTasksMDContent = `# Create Tasks from RFC

Read the RFC specification and generate a task list.

## Instructions

1. Read the RFC content provided below
2. Break down the implementation into discrete, testable tasks
3. Order tasks by dependency (setup first, then features, then tests)
4. Each task should be completable in one commit
5. Output tasks.json to .wisp/tasks.json

## Task Guidelines

- Keep tasks focused and atomic
- Include setup tasks (dependencies, configuration)
- Include test tasks where appropriate
- Mark all tasks with "passes": false initially

## Output Format

Write a valid JSON array to .wisp/tasks.json with this structure:

` + "```json" + `
[
  {
    "category": "setup",
    "description": "Task description",
    "steps": ["Step 1", "Step 2"],
    "passes": false
  }
]
` + "```" + `

## RFC Content

Read the RFC from the path specified in your session configuration.
`

const updateTasksMDContent = `# Update Tasks from RFC Changes

The RFC has been updated. Reconcile the task list with the changes.

## Instructions

1. Read the RFC diff provided below
2. Read the current tasks.json from .wisp/tasks.json
3. Identify which tasks need to be:
   - Modified (requirements changed)
   - Added (new features)
   - Removed (features cut)
   - Marked incomplete (needs rework)
4. Output updated tasks.json

## Guidelines

- Preserve completed tasks where possible
- Mark tasks as passes: false if they need rework
- Add new tasks for new requirements
- Remove tasks for cut features
- Maintain dependency ordering

## Output

Write updated tasks.json to .wisp/tasks.json.
`

const reviewTasksMDContent = `# Address PR Feedback

Generate tasks to address PR review feedback.

## Instructions

1. Read the feedback content provided below
2. Read the current tasks.json from .wisp/tasks.json
3. Generate new tasks to address each piece of feedback
4. Append new tasks to the task list
5. Output updated tasks.json

## Guidelines

- Create focused tasks for each feedback item
- Use category "bugfix" for corrections
- Use category "refactor" for code quality feedback
- Use category "docs" for documentation requests
- Keep existing completed tasks intact

## Output

Write updated tasks.json to .wisp/tasks.json.
`

const iterateMDContent = `# Iteration Instructions

Complete the next incomplete task in the task list.

## Steps

1. Read .wisp/tasks.json and find the first task where passes is false
2. Read .wisp/state.json for context on previous iteration
3. Check .wisp/response.json for human response (delete after reading)
4. Implement the task following its steps
5. Verify your work (tests, typecheck, build as appropriate)
6. Commit your changes with a descriptive message
7. Update .wisp/tasks.json to mark the task as passes: true
8. Write .wisp/state.json with your status

## State Updates

Always write state.json after completing work:

` + "```json" + `
{
  "status": "CONTINUE",
  "summary": "Implemented feature X with tests",
  "verification": {
    "method": "tests",
    "passed": true,
    "details": "12 tests passed"
  }
}
` + "```" + `

## When Blocked

If you cannot proceed:
- Need human input: status "NEEDS_INPUT" with "question"
- Cannot continue: status "BLOCKED" with "error"

## RFC Divergence

If your implementation differs from the RFC (e.g., better approach discovered),
append a note to .wisp/divergence.md explaining the deviation.

## Completion

When all tasks have passes: true, set status to "DONE".
`

const generatePRMDContent = `# Generate PR Title and Description

Generate a concise, informative pull request title and description based on the specification and completed tasks.

## Instructions

1. Read the spec from /var/local/wisp/session/spec.md
2. Read the completed tasks from /var/local/wisp/session/tasks.json
3. Generate a PR title and description that:
   - Explains the **purpose** and **value** of the changes (not just what was done)
   - Uses conventional commit format for the title (feat:, fix:, etc.)
   - Summarizes the key capabilities added
   - Is written for a human reviewer, not a task tracker

## Guidelines

**Title:**
- Use conventional commit prefix: ` + "`feat:`" + ` for features, ` + "`fix:`" + ` for bugs, ` + "`refactor:`" + ` for refactoring
- Focus on the user-facing outcome, not implementation details
- Keep under 72 characters
- Example: "feat: add remote web UI for monitoring wisp sessions"

**Description:**
- Start with a ` + "`## Summary`" + ` section with 1-3 sentences explaining the purpose and value
- Add a ` + "`### Key Changes`" + ` section with bullet points summarizing the main capabilities (grouped conceptually, not just listing every task)
- End with the full task list as a checklist (with ` + "`- [x]`" + ` for completed tasks) in a collapsed ` + "`<details>`" + ` block
- Only include the task list ONCE (in the details block)
- End with a wisp attribution line

## Output Format

Write a valid JSON object to /var/local/wisp/session/pr.json with this structure:

` + "```json" + `
{
  "title": "feat: concise description of the change",
  "body": "## Summary\n\nPurpose and value of this PR in 1-3 sentences.\n\n### Key Changes\n\n- Capability 1\n- Capability 2\n- Capability 3\n\n<details>\n<summary>Tasks completed (N/N)</summary>\n\n- [x] Task 1\n- [x] Task 2\n- [x] Task 3\n\n</details>\n\n---\n🤖 Generated with [wisp](https://github.com/thruflo/wisp)"
}
` + "```" + `

Read the spec and tasks, then generate the PR content.
`
