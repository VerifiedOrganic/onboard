package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/VerifiedOrganic/onboard/internal/agents"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Detect installed agents and wire onboard into each",
	Long:  "Scans for installed agents (Claude Code, Grok, Codex, opencode, Cursor) and installs onboard into every one it finds. A convenience wrapper over `install --all`.",
	RunE: func(_ *cobra.Command, _ []string) error {
		bin, err := os.Executable()
		if err != nil {
			return err
		}
		all, err := agents.Registry()
		if err != nil {
			return err
		}

		fmt.Println("Scanning for installed agents...")
		found := 0
		failures := 0
		for _, a := range all {
			if !agents.Detected(a) {
				fmt.Printf("  – %-9s not detected, skipping\n", a.Name)
				continue
			}
			found++
			res, err := agents.Install(a, bin)
			if err != nil {
				fmt.Printf("  ✗ %-9s %v\n", a.Name, err)
				failures++
				continue
			}
			fmt.Printf("  ✓ %-9s config: %-15s skills: %d file(s)\n",
				res.Agent, res.ConfigAction, res.SkillFiles)
		}
		if found == 0 {
			fmt.Println("\nNo agents detected. Install one, or use `onboard install --agent <name>` to force a target.")
			return nil
		}
		fmt.Println("\nDone. Restart your agent(s) to pick up the onboard MCP server.")
		if failures > 0 {
			return fmt.Errorf("%d install(s) failed", failures)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
