package cli

import (
	"github.com/spf13/cobra"
)

// Version is set at build time via ldflags.
var Version = "dev"

var rootCmd = &cobra.Command{
	Use:   "wisp",
	Short: "Ralph Wiggum orchestrator for RFC-driven development on Sprites",
	Long: `Wisp automates RFC implementation using autonomous Claude Code loops
in isolated Sprites. Developer provides an RFC, wisp generates tasks,
runs Claude until completion or blockage, produces a PR.`,
}

func init() {
	rootCmd.Version = Version
	rootCmd.SetVersionTemplate("wisp version {{.Version}}\n")
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}
