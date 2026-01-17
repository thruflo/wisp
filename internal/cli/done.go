package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thruflo/wisp/internal/config"
	"github.com/thruflo/wisp/internal/sprite"
	"github.com/thruflo/wisp/internal/state"
)

var doneCmd = &cobra.Command{
	Use:   "done [branch]",
	Short: "Complete a wisp session and create a PR",
	Long: `Complete a wisp session, verify all tasks pass, and create a pull request.

This command:
1. Verifies all tasks have passes: true
2. Copies divergence.md from Sprite to local storage
3. Pushes the branch to remote
4. Prompts for PR creation mode:
   [p] push PR (auto-generated title/body)
   [m] push branch (manual PR via browser)
   [c] cancel
5. Tears down the Sprite
6. Updates session status to completed

If no branch is specified and there's only one session, that session is used.

Example:
  wisp done
  wisp done wisp/my-feature`,
	Args: cobra.MaximumNArgs(1),
	RunE: runDone,
}

func init() {
	rootCmd.AddCommand(doneCmd)
}

// PRMode represents the user's choice for PR creation.
type PRMode int

const (
	PRModeCancel PRMode = iota
	PRModeAuto
	PRModeManual
)

func runDone(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Initialize storage
	store := state.NewStore(cwd)

	// Determine branch name
	branch, err := resolveBranch(store, args)
	if err != nil {
		return err
	}

	// Load session
	session, err := store.GetSession(branch)
	if err != nil {
		return fmt.Errorf("failed to load session: %w", err)
	}

	fmt.Printf("Completing wisp session...\n")
	fmt.Printf("  Branch: %s\n", session.Branch)
	fmt.Printf("  Repo: %s\n", session.Repo)

	// Load and verify tasks
	tasks, err := store.LoadTasks(branch)
	if err != nil {
		return fmt.Errorf("failed to load tasks: %w", err)
	}

	if tasks == nil || len(tasks) == 0 {
		return fmt.Errorf("no tasks found for session")
	}

	// Verify all tasks pass
	incomplete := countIncompleteTasks(tasks)
	if incomplete > 0 {
		return fmt.Errorf("%d task(s) are not complete (passes: false)", incomplete)
	}

	fmt.Printf("  Tasks: %d/%d complete ✓\n", len(tasks), len(tasks))

	// Get sprite token
	env, _ := config.LoadEnvFile(cwd)
	spriteToken := env["SPRITE_TOKEN"]
	if spriteToken == "" {
		spriteToken = os.Getenv("SPRITE_TOKEN")
	}

	var client sprite.Client
	var repoPath string
	var spriteExists bool

	if spriteToken != "" {
		client = sprite.NewSDKClient(spriteToken)

		// Check if Sprite exists
		exists, err := client.Exists(ctx, session.SpriteName)
		if err != nil {
			fmt.Printf("Warning: failed to check Sprite status: %v\n", err)
		}
		spriteExists = exists

		if spriteExists {
			// Calculate repo path
			parts := strings.Split(session.Repo, "/")
			if len(parts) == 2 {
				repoPath = filepath.Join("/home/sprite", parts[0], parts[1])

				// Sync divergence.md from Sprite
				if err := syncDivergenceFromSprite(ctx, client, session.SpriteName, repoPath, store, branch); err != nil {
					fmt.Printf("Warning: failed to sync divergence.md: %v\n", err)
				}

				// Push branch to remote (on Sprite)
				fmt.Printf("Pushing branch to remote...\n")
				if err := pushBranchOnSprite(ctx, client, session.SpriteName, repoPath, session.Branch); err != nil {
					return fmt.Errorf("failed to push branch: %w", err)
				}
				fmt.Printf("Branch pushed successfully.\n")
			}
		} else {
			fmt.Printf("Note: Sprite not found. Branch may already be pushed.\n")
		}
	} else {
		fmt.Printf("Warning: SPRITE_TOKEN not found, skipping Sprite operations.\n")
	}

	// Prompt user for PR mode
	mode := promptPRMode()

	switch mode {
	case PRModeCancel:
		fmt.Printf("Cancelled. Session left in current state.\n")
		return nil

	case PRModeAuto:
		fmt.Printf("Creating PR automatically...\n")
		if spriteExists && client != nil && repoPath != "" {
			prURL, err := createPROnSprite(ctx, client, session.SpriteName, repoPath, session.Branch, tasks, env)
			if err != nil {
				return fmt.Errorf("failed to create PR: %w", err)
			}
			fmt.Printf("PR created: %s\n", prURL)
		} else {
			return fmt.Errorf("cannot create PR automatically: Sprite not available")
		}

	case PRModeManual:
		// Open GitHub compare URL
		compareURL := buildCompareURL(session.Repo, session.Branch)
		fmt.Printf("Opening browser for manual PR creation...\n")
		fmt.Printf("URL: %s\n", compareURL)
		if err := openBrowser(compareURL); err != nil {
			fmt.Printf("Failed to open browser: %v\n", err)
			fmt.Printf("Please open the URL manually.\n")
		}
	}

	// Teardown Sprite if it exists
	if spriteExists && client != nil {
		fmt.Printf("Tearing down Sprite...\n")
		if err := client.Delete(ctx, session.SpriteName); err != nil {
			fmt.Printf("Warning: failed to teardown Sprite: %v\n", err)
		} else {
			fmt.Printf("Sprite teardown complete.\n")
		}
	}

	// Update session status to completed
	if err := store.UpdateSession(branch, func(s *config.Session) {
		s.Status = config.SessionStatusCompleted
	}); err != nil {
		return fmt.Errorf("failed to update session status: %w", err)
	}

	fmt.Printf("\nSession completed successfully!\n")
	return nil
}

// resolveBranch determines the branch to use based on args or available sessions.
func resolveBranch(store *state.Store, args []string) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}

	// No branch specified, check for single session
	sessions, err := store.ListSessions()
	if err != nil {
		return "", fmt.Errorf("failed to list sessions: %w", err)
	}

	if len(sessions) == 0 {
		return "", fmt.Errorf("no sessions found. Specify a branch name")
	}

	if len(sessions) > 1 {
		fmt.Printf("Multiple sessions found:\n")
		for _, s := range sessions {
			fmt.Printf("  - %s (%s)\n", s.Branch, s.Status)
		}
		return "", fmt.Errorf("multiple sessions found. Specify a branch name")
	}

	return sessions[0].Branch, nil
}

// countIncompleteTasks returns the number of tasks with passes: false.
func countIncompleteTasks(tasks []state.Task) int {
	count := 0
	for _, t := range tasks {
		if !t.Passes {
			count++
		}
	}
	return count
}

// syncDivergenceFromSprite copies divergence.md from Sprite to local session directory.
func syncDivergenceFromSprite(ctx context.Context, client sprite.Client, spriteName, repoPath string, store *state.Store, branch string) error {
	divergencePath := filepath.Join(repoPath, ".wisp", "divergence.md")
	content, err := client.ReadFile(ctx, spriteName, divergencePath)
	if err != nil {
		// File may not exist if no divergence occurred
		return nil
	}

	if len(content) == 0 {
		return nil
	}

	// Write to local session directory
	localPath := filepath.Join(store.SessionDir(branch), "divergence.md")
	if err := os.WriteFile(localPath, content, 0o644); err != nil {
		return fmt.Errorf("failed to write divergence.md locally: %w", err)
	}

	fmt.Printf("  Divergence log synced.\n")
	return nil
}

// pushBranchOnSprite pushes the current branch to remote via the Sprite.
func pushBranchOnSprite(ctx context.Context, client sprite.Client, spriteName, repoPath, branch string) error {
	_, stderr, exitCode, err := client.ExecuteOutput(ctx, spriteName, repoPath, nil, "git", "push", "-u", "origin", branch)
	if err != nil {
		return fmt.Errorf("failed to execute push: %w", err)
	}
	if exitCode != 0 {
		return fmt.Errorf("push failed with exit code %d: %s", exitCode, string(stderr))
	}

	return nil
}

// promptPRMode displays the PR creation options and reads user choice.
func promptPRMode() PRMode {
	fmt.Printf("\nAll tasks complete. Create PR?\n")
	fmt.Printf("[p] push PR (auto-generated)  [m] push branch (manual PR)  [c] cancel\n")
	fmt.Printf("> ")

	reader := bufio.NewReader(os.Stdin)
	for {
		input, err := reader.ReadString('\n')
		if err != nil {
			return PRModeCancel
		}

		input = strings.TrimSpace(strings.ToLower(input))
		switch input {
		case "p":
			return PRModeAuto
		case "m":
			return PRModeManual
		case "c", "":
			return PRModeCancel
		default:
			fmt.Printf("Invalid choice. Enter [p], [m], or [c]: ")
		}
	}
}

// createPROnSprite runs gh pr create on the Sprite to create a PR.
func createPROnSprite(ctx context.Context, client sprite.Client, spriteName, repoPath, branch string, tasks []state.Task, env map[string]string) (string, error) {
	// Build PR title and body from task summaries
	title := buildPRTitle(tasks)
	body := buildPRBody(tasks)

	// Build environment with GITHUB_TOKEN
	cmdEnv := []string{}
	if token := env["GITHUB_TOKEN"]; token != "" {
		cmdEnv = append(cmdEnv, "GITHUB_TOKEN="+token)
	}
	if token := os.Getenv("GITHUB_TOKEN"); token != "" && len(cmdEnv) == 0 {
		cmdEnv = append(cmdEnv, "GITHUB_TOKEN="+token)
	}

	// Run gh pr create
	stdout, stderr, exitCode, err := client.ExecuteOutput(ctx, spriteName, repoPath, cmdEnv,
		"gh", "pr", "create",
		"--title", title,
		"--body", body,
	)
	if err != nil {
		return "", fmt.Errorf("failed to execute gh pr create: %w", err)
	}
	if exitCode != 0 {
		return "", fmt.Errorf("gh pr create failed with exit code %d: %s", exitCode, string(stderr))
	}

	prURL := strings.TrimSpace(string(stdout))
	return prURL, nil
}

// buildPRTitle generates a PR title from tasks.
func buildPRTitle(tasks []state.Task) string {
	// Use the first feature task's description, or fallback
	for _, t := range tasks {
		if t.Category == "feature" && t.Description != "" {
			// Truncate if too long
			title := t.Description
			if len(title) > 72 {
				title = title[:69] + "..."
			}
			return title
		}
	}

	// Fallback: count tasks by category
	categories := make(map[string]int)
	for _, t := range tasks {
		categories[t.Category]++
	}

	parts := []string{}
	for cat, count := range categories {
		if count == 1 {
			parts = append(parts, cat)
		} else {
			parts = append(parts, fmt.Sprintf("%d %s tasks", count, cat))
		}
	}

	if len(parts) > 0 {
		return "Implement " + strings.Join(parts, ", ")
	}
	return "Implementation complete"
}

// buildPRBody generates a PR body from tasks.
func buildPRBody(tasks []state.Task) string {
	var body strings.Builder

	body.WriteString("## Summary\n\n")
	body.WriteString("This PR implements the following tasks:\n\n")

	for _, t := range tasks {
		body.WriteString(fmt.Sprintf("- **[%s]** %s\n", t.Category, t.Description))
	}

	body.WriteString("\n## Tasks\n\n")
	completed := 0
	for _, t := range tasks {
		if t.Passes {
			body.WriteString(fmt.Sprintf("- [x] %s\n", t.Description))
			completed++
		} else {
			body.WriteString(fmt.Sprintf("- [ ] %s\n", t.Description))
		}
	}

	body.WriteString(fmt.Sprintf("\n**%d/%d tasks completed**\n", completed, len(tasks)))
	body.WriteString("\n---\n🤖 Generated with [wisp](https://github.com/thruflo/wisp)\n")

	return body.String()
}

// buildCompareURL creates a GitHub compare URL for manual PR creation.
func buildCompareURL(repo, branch string) string {
	// Handle branch names with slashes
	encodedBranch := strings.ReplaceAll(branch, "/", "%2F")
	return fmt.Sprintf("https://github.com/%s/compare/%s?expand=1", repo, encodedBranch)
}

// openBrowser opens a URL in the default browser.
func openBrowser(url string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	return cmd.Start()
}
