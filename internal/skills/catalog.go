package skills

import (
	"fmt"
	"strings"
)

// CatalogEntry is the user-facing description of one shipped onboard workflow.
type CatalogEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Summary     string   `json:"summary"`
	Try         string   `json:"try"`
	Aliases     []string `json:"aliases,omitempty"`
}

var legacySkillAliases = map[string]string{
	"architecture-cartographer":  "onboard-architecture-cartographer",
	"codebase-walkthrough":       "onboard-codebase-walkthrough",
	"dependency-impact-analyzer": "onboard-dependency-impact-analyzer",
	"guide-maintainer":           "onboard-guide-maintainer",
	"test-gap-and-risk-auditor":  "onboard-test-gap-and-risk-auditor",
}

// LegacyAliases returns old unprefixed skill names mapped to their current namespaced
// identifiers. Callers must treat the returned map as read-only.
func LegacyAliases() map[string]string {
	out := make(map[string]string, len(legacySkillAliases))
	for alias, canonical := range legacySkillAliases {
		out[alias] = canonical
	}
	return out
}

var catalogOrder = []string{
	"onboard-codebase-walkthrough",
	"onboard-infra-walkthrough",
	"onboard-dependency-impact-analyzer",
	"onboard-test-gap-and-risk-auditor",
	"onboard-architecture-cartographer",
	"onboard-guide-maintainer",
}

var catalogMetadata = map[string]struct {
	Summary string
	Try     string
}{
	"onboard-codebase-walkthrough": {
		Summary: "Guided repo tour, first-time cached guide, or interactive map.",
		Try:     `/onboard` + ` or "walk me through this repo"`,
	},
	"onboard-infra-walkthrough": {
		Summary: "Guided tour of a Terraform/Terragrunt/OpenTofu repo — stacks, module graph, state layout, IaC risk.",
		Try:     `"walk me through this Terraform repo"`,
	},
	"onboard-dependency-impact-analyzer": {
		Summary: "Blast radius for one proposed function, file, endpoint, schema, or field change.",
		Try:     `"what breaks if I change X?"`,
	},
	"onboard-test-gap-and-risk-auditor": {
		Summary: "Whole-repo risk register covering untested paths, fragile seams, and silent assumptions.",
		Try:     `"audit this codebase for risk and test gaps"`,
	},
	"onboard-architecture-cartographer": {
		Summary: "Committable Mermaid diagrams for architecture, flows, dependencies, and schemas.",
		Try:     `"draw the architecture as Mermaid"`,
	},
	"onboard-guide-maintainer": {
		Summary: "Delta-update an existing git-SHA-tagged codebase guide after code changes.",
		Try:     `"refresh the onboard guide"`,
	},
}

// CanonicalName maps old unprefixed skill names to the current onboard-* namespace.
func CanonicalName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if canonical, ok := legacySkillAliases[name]; ok {
		return canonical
	}
	return name
}

// Catalog returns a stable, user-facing catalog of the embedded onboard workflows.
func Catalog() ([]CatalogEntry, error) {
	all, err := List()
	if err != nil {
		return nil, err
	}
	byName := map[string]Skill{}
	for _, sk := range all {
		byName[sk.Name] = sk
	}

	aliasesByName := map[string][]string{}
	for alias, canonical := range legacySkillAliases {
		aliasesByName[canonical] = append(aliasesByName[canonical], alias)
	}

	seen := map[string]bool{}
	out := make([]CatalogEntry, 0, len(all))
	for _, name := range catalogOrder {
		sk, ok := byName[name]
		if !ok {
			continue
		}
		out = append(out, catalogEntry(sk, aliasesByName[name]))
		seen[name] = true
	}
	for _, sk := range all {
		if seen[sk.Name] {
			continue
		}
		out = append(out, catalogEntry(sk, aliasesByName[sk.Name]))
	}
	return out, nil
}

func catalogEntry(sk Skill, aliases []string) CatalogEntry {
	meta := catalogMetadata[sk.Name]
	summary := meta.Summary
	if summary == "" {
		summary = sk.Description
	}
	return CatalogEntry{
		Name:        sk.Name,
		Description: sk.Description,
		Summary:     summary,
		Try:         meta.Try,
		Aliases:     aliases,
	}
}

// CatalogMarkdown renders a compact catalog suitable for prompts, docs, and CLI output.
func CatalogMarkdown() (string, error) {
	catalog, err := Catalog()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("# Onboard Skill Catalog\n\n")
	b.WriteString("Use `/onboard` for the guided codebase tour. Use `/onboard-skills` to show this catalog again.\n\n")
	b.WriteString("The native skill identifiers are namespaced with `onboard-` so they group together in agent skill lists and avoid collisions with user-installed skills. Legacy unprefixed names still work with `get_skill`.\n\n")
	for _, entry := range catalog {
		fmt.Fprintf(&b, "- `%s` — %s\n", entry.Name, entry.Summary)
		if entry.Try != "" {
			fmt.Fprintf(&b, "  Try: %s\n", entry.Try)
		}
	}
	return b.String(), nil
}
