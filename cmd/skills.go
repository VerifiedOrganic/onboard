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
		all, err := skills.List()
		if err != nil {
			return err
		}
		if len(all) == 0 {
			fmt.Println("No embedded skills found.")
			return nil
		}
		for _, s := range all {
			fmt.Printf("• %s\n  %s\n", s.Name, truncate(s.Description, 110))
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
