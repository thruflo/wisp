package cli

import (
	"github.com/spf13/cobra"
)

var (
	version = "dev"
)

var rootCmd = &cobra.Command{
	Use:   "wisp",
	Short: "Ralph Wiggum orchestrator for RFC-driven development on Sprites",
	Long: `Wisp automates RFC implementation using autonomous Claude Code loops
in isolated Sprites. Developer provides an RFC, wisp generates tasks,
runs Claude until completion or blockage, produces a PR.`,
}

func init() {
	rootCmd.PersistentFlags().BoolP("version", "v", false, "print version information")
}

func Execute() error {
	return rootCmd.Execute()
}
