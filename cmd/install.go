package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/VerifiedOrganic/onboard/internal/agents"
)

var (
	installAgent  string
	installAll    bool
	installDryRun bool
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install onboard into your coding agents (skill files + MCP config)",
	Long: `Registers this binary as an MCP server in your agents' config, and for agents
with a native skill system also installs the embedded skill files.

Examples:
  onboard install --agent claude
  onboard install --agent codex
  onboard install --all
  onboard install --all --dry-run`,
	RunE: func(_ *cobra.Command, _ []string) error {
		bin, err := os.Executable()
		if err != nil {
			return err
		}

		targets, err := resolveTargets(installAgent, installAll, "onboard install --help")
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
			if installDryRun {
				res, err = agents.PreviewInstall(a, bin)
			} else {
				res, err = agents.Install(a, bin)
			}
			if err != nil {
				fmt.Printf("  ✗ %-9s %v\n", a.Name, err)
				failures++
				continue
			}
			printInstallResult(res, installDryRun)
		}
		if installDryRun {
			fmt.Println("\nDry run only; no files were changed.")
		} else {
			fmt.Println("\nRestart your agent(s) to pick up the onboard MCP server.")
		}
		if failures > 0 {
			return fmt.Errorf("%d install(s) failed", failures)
		}
		return nil
	},
}

func printInstallResult(res agents.Result, dryRun bool) {
	action := res.ConfigAction
	if dryRun {
		action = dryRunAction(action)
	}
	fmt.Printf("  ✓ %-9s config: %-18s skills: %d file(s)%s%s%s\n",
		res.Agent, action, res.SkillFiles, cleanupSuffix(res.SkillDirsCleaned), backupSuffix(res.BackupPath), pathSuffix(res))
}

func dryRunAction(action string) string {
	switch action {
	case "merged":
		return "would-merge"
	case "appended":
		return "would-append"
	case "refreshed":
		return "would-refresh"
	case "removed":
		return "would-remove"
	default:
		return "would-" + action
	}
}

func cleanupSuffix(n int) string {
	if n == 0 {
		return ""
	}
	if n == 1 {
		return " (cleaned 1 legacy dir)"
	}
	return fmt.Sprintf(" (cleaned %d legacy dirs)", n)
}

func backupSuffix(path string) string {
	if path == "" {
		return ""
	}
	return " backup: " + path
}

func pathSuffix(res agents.Result) string {
	if res.ConfigPath == "" && res.SkillsDir == "" {
		return ""
	}
	return fmt.Sprintf(" (config: %s, skills: %s)", res.ConfigPath, res.SkillsDir)
}

func resolveTargets(agent string, allFlag bool, helpCmd string) ([]agents.Agent, error) {
	if allFlag && agent != "" {
		return nil, fmt.Errorf("use either --agent <name> or --all, not both (run %q)", helpCmd)
	}
	if allFlag {
		all, err := agents.Registry()
		if err != nil {
			return nil, err
		}
		var detected []agents.Agent
		for _, a := range all {
			if agents.Detected(a) {
				detected = append(detected, a)
			}
		}
		return detected, nil
	}
	if agent != "" {
		a, err := agents.Find(agent)
		if err != nil {
			return nil, err
		}
		return []agents.Agent{a}, nil
	}
	return nil, fmt.Errorf("specify --agent <name> or --all (run %q)", helpCmd)
}

func init() {
	installCmd.Flags().StringVar(&installAgent, "agent", "", "agent to install into (claude|grok|codex|kimi|opencode|cursor|copilot|junie)")
	installCmd.Flags().BoolVar(&installAll, "all", false, "install into all detected agents")
	installCmd.Flags().BoolVar(&installDryRun, "dry-run", false, "show planned config and skill changes without writing files")
	rootCmd.AddCommand(installCmd)
}
