package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/VerifiedOrganic/onboard/internal/skills"
)

var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "List the skills embedded in this binary",
	RunE: func(_ *cobra.Command, _ []string) error {
		catalog, err := skills.Catalog()
		if err != nil {
			return err
		}
		if len(catalog) == 0 {
			fmt.Println("No embedded skills found.")
			return nil
		}
		fmt.Println("Onboard skills")
		fmt.Println("Use `/onboard` for the guided tour, or `/onboard-skills` to show this catalog in an MCP client.")
		for _, s := range catalog {
			fmt.Printf("\n• %s\n  %s\n", s.Name, truncate(s.Summary, 110))
			if s.Try != "" {
				fmt.Printf("  Try: %s\n", s.Try)
			}
		}
		return nil
	},
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func init() {
	rootCmd.AddCommand(skillsCmd)
}
