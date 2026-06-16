package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/VerifiedOrganic/onboard/internal/agents"
)

var (
	uninstallAgent  string
	uninstallAll    bool
	uninstallDryRun bool
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove onboard from your coding agents (skill files + MCP config)",
	Long: `Removes onboard's MCP server entry and embedded onboard-* skill directories
from one agent or every detected agent.

Examples:
  onboard uninstall --agent codex
  onboard uninstall --all
  onboard uninstall --all --dry-run`,
	RunE: func(_ *cobra.Command, _ []string) error {
		targets, err := resolveTargets(uninstallAgent, uninstallAll, "onboard uninstall --help")
		if err != nil {
			return err
		}
		if len(targets) == 0 {
			fmt.Println("No target agents. Use --agent <name> or --all.")
			return nil
		}

		failures := 0
		for _, a := range targets {
			var res agents.Result
			if uninstallDryRun {
				res, err = agents.PreviewUninstall(a)
			} else {
				res, err = agents.Uninstall(a)
			}
			if err != nil {
				fmt.Printf("  ✗ %-9s %v\n", a.Name, err)
				failures++
				continue
			}
			printUninstallResult(res, uninstallDryRun)
		}
		if uninstallDryRun {
			fmt.Println("\nDry run only; no files were changed.")
		}
		if failures > 0 {
			return fmt.Errorf("%d uninstall(s) failed", failures)
		}
		return nil
	},
}

func printUninstallResult(res agents.Result, dryRun bool) {
	action := res.ConfigAction
	if dryRun {
		action = dryRunAction(action)
	}
	fmt.Printf("  ✓ %-9s config: %-18s skills: %d dir(s)%s\n",
		res.Agent, action, res.SkillDirsRemoved, pathSuffix(res))
}

func init() {
	uninstallCmd.Flags().StringVar(&uninstallAgent, "agent", "", "agent to uninstall from (claude|grok|codex|kimi|opencode|cursor|copilot|junie)")
	uninstallCmd.Flags().BoolVar(&uninstallAll, "all", false, "uninstall from all detected agents")
	uninstallCmd.Flags().BoolVar(&uninstallDryRun, "dry-run", false, "show planned removals without writing files")
	rootCmd.AddCommand(uninstallCmd)
}
