package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/VerifiedOrganic/onboard/internal/agents"
)

var (
	installAgent string
	installAll   bool
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install onboard into your coding agents (skill files + MCP config)",
	Long: `Registers this binary as an MCP server in your agents' config, and for agents
with a native skill system also installs the embedded skill files.

Examples:
  onboard install --agent claude
  onboard install --agent codex
  onboard install --all`,
	RunE: func(_ *cobra.Command, _ []string) error {
		bin, err := os.Executable()
		if err != nil {
			return err
		}

		targets, err := resolveTargets()
		if err != nil {
			return err
		}
		if len(targets) == 0 {
			fmt.Println("No target agents. Use --agent <name> or --all.")
			return nil
		}

		failures := 0
		for _, a := range targets {
			res, err := agents.Install(a, bin)
			if err != nil {
				fmt.Printf("  ✗ %-9s %v\n", a.Name, err)
				failures++
				continue
			}
			fmt.Printf("  ✓ %-9s config: %-15s skills: %d file(s)%s\n",
				res.Agent, res.ConfigAction, res.SkillFiles, cleanupSuffix(res.SkillDirsCleaned))
		}
		fmt.Println("\nRestart your agent(s) to pick up the onboard MCP server.")
		if failures > 0 {
			return fmt.Errorf("%d install(s) failed", failures)
		}
		return nil
	},
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

func resolveTargets() ([]agents.Agent, error) {
	if installAll && installAgent != "" {
		return nil, fmt.Errorf("use either --agent <name> or --all, not both")
	}
	if installAll {
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
	if installAgent != "" {
		a, err := agents.Find(installAgent)
		if err != nil {
			return nil, err
		}
		return []agents.Agent{a}, nil
	}
	return nil, fmt.Errorf("specify --agent <name> or --all")
}

func init() {
	installCmd.Flags().StringVar(&installAgent, "agent", "", "agent to install into (claude|grok|codex|opencode|cursor|copilot|junie)")
	installCmd.Flags().BoolVar(&installAll, "all", false, "install into all detected agents")
	rootCmd.AddCommand(installCmd)
}
