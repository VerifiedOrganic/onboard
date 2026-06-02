package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/VerifiedOrganic/onboard/internal/agents"
)

var doctorAgent string

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Verify onboard is correctly installed in each detected agent",
	Long: `Inspects each agent's MCP config and skills directory and reports whether onboard
is registered, the configured binary still exists, and the skill files landed.

It is read-only — it changes nothing. Exits non-zero if a detected agent has a
problem, so it is safe to use as a post-install or CI check.`,
	RunE: func(_ *cobra.Command, _ []string) error {
		all, err := agents.Registry()
		if err != nil {
			return err
		}
		if doctorAgent != "" {
			a, err := agents.Find(doctorAgent)
			if err != nil {
				return err
			}
			all = []agents.Agent{a}
		}

		problems, shown := 0, 0
		for _, a := range all {
			h := agents.Inspect(a)
			if !h.Detected && doctorAgent == "" {
				fmt.Printf("  – %-9s not installed\n", a.Name)
				continue
			}
			shown++
			mark := "✓"
			if !h.OK() {
				mark = "✗"
				problems++
			}
			fmt.Printf("  %s %-9s registered=%-5t bin=%-5t skills=%d/%d\n",
				mark, a.Name, h.Registered, h.BinExists, h.SkillsPresent, h.SkillsExpected)
			if h.ConfiguredBin != "" {
				fmt.Printf("      bin: %s\n", h.ConfiguredBin)
			}
			for _, iss := range h.Issues {
				fmt.Printf("      ! %s\n", iss)
			}
		}

		if shown == 0 {
			fmt.Println("No agents detected. Install one, then run `onboard init`.")
			return nil
		}
		if problems > 0 {
			return fmt.Errorf("%d agent(s) have problems — see above", problems)
		}
		fmt.Println("\nAll detected agents look healthy.")
		return nil
	},
}

func init() {
	doctorCmd.Flags().StringVar(&doctorAgent, "agent", "", "check only this agent (claude, codex, grok, opencode, cursor)")
	rootCmd.AddCommand(doctorCmd)
}
