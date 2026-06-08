// Package cmd implements the onboard command-line interface. The same binary is
// both an MCP server (onboard serve) and an installer/CLI (onboard install, init,
// skills, ...).
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Build metadata, stamped via -ldflags at release time, e.g.:
//
//	-X github.com/VerifiedOrganic/onboard/cmd.version=v1.2.3
//	-X github.com/VerifiedOrganic/onboard/cmd.commit=$(git rev-parse HEAD)
//	-X github.com/VerifiedOrganic/onboard/cmd.date=$(date -u +%FT%TZ)
//
// version also identifies the MCP server to connecting clients.
var (
	version = "0.1.0"
	commit  = ""
	date    = ""
)

// buildVersion composes the human-facing version string for `onboard -v`.
func buildVersion() string {
	v := version
	if commit != "" {
		short := commit
		if len(short) > 7 {
			short = short[:7]
		}
		v += " (" + short + ")"
	}
	if date != "" {
		v += " " + date
	}
	return v
}

var rootCmd = &cobra.Command{
	Use:   "onboard",
	Short: "Cross-agent codebase onboarding: an MCP server with embedded skills",
	Long: `onboard is a single static binary that walks developers through a codebase —
architecture, data flow, end-to-end traces, and the risky negative space.

It embeds namespaced onboard-* skills and serves them to any MCP-capable agent
(Claude Code, Grok, Codex, opencode, Cursor, Copilot CLI, Junie CLI) and to headless/CI use.

  onboard serve              run the MCP server over stdio
  onboard install --all      wire it into every detected agent
  onboard skills             list the embedded skills`,
	Version:       buildVersion(),
	SilenceErrors: true,
	SilenceUsage:  true,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
