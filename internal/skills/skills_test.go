package skills

import (
	"strings"
	"testing"
)

func TestListFindsEmbeddedSkill(t *testing.T) {
	all, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("expected at least one embedded skill")
	}
	var found bool
	for _, s := range all {
		if s.Name == "onboard-codebase-walkthrough" {
			found = true
			if s.Description == "" {
				t.Error("onboard-codebase-walkthrough has empty description (frontmatter not parsed)")
			}
		}
	}
	if !found {
		t.Errorf("onboard-codebase-walkthrough not in %v", names(all))
	}
}

func TestGet(t *testing.T) {
	s, err := Get("onboard-codebase-walkthrough")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if s.Name != "onboard-codebase-walkthrough" {
		t.Errorf("name = %q", s.Name)
	}
	if _, err := Get("does-not-exist"); err == nil {
		t.Error("expected error for unknown skill")
	}
}

func TestGetAcceptsLegacyUnprefixedNames(t *testing.T) {
	s, err := Get("codebase-walkthrough")
	if err != nil {
		t.Fatalf("Get legacy alias: %v", err)
	}
	if s.Name != "onboard-codebase-walkthrough" {
		t.Errorf("legacy alias resolved to %q, want onboard-codebase-walkthrough", s.Name)
	}
}

func TestCatalogCoversEmbeddedSkills(t *testing.T) {
	all, err := List()
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) != len(all) {
		t.Fatalf("catalog has %d entries, want %d", len(catalog), len(all))
	}
	for i, entry := range catalog {
		if !strings.HasPrefix(entry.Name, "onboard-") {
			t.Errorf("catalog entry %q is not namespaced", entry.Name)
		}
		if entry.Summary == "" {
			t.Errorf("catalog entry %q has no summary", entry.Name)
		}
		if i == 0 && entry.Name != "onboard-codebase-walkthrough" {
			t.Errorf("first catalog entry = %q, want onboard-codebase-walkthrough", entry.Name)
		}
	}
	md, err := CatalogMarkdown()
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"/onboard", "/onboard-skills", "onboard-dependency-impact-analyzer"} {
		if !strings.Contains(md, marker) {
			t.Errorf("catalog markdown missing %q", marker)
		}
	}
}

func TestRenderIncludesReferences(t *testing.T) {
	s, err := Get("onboard-codebase-walkthrough")
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out, "# Codebase Walkthrough") {
		t.Error("rendered skill missing SKILL.md body")
	}
	if !strings.Contains(out, "# reference:") {
		t.Error("rendered skill missing reference sections")
	}
	// The walkthrough ships four references; expect each delimiter.
	if n := strings.Count(out, "# reference:"); n < 4 {
		t.Errorf("expected >=4 reference sections, got %d", n)
	}
}

func TestFiles(t *testing.T) {
	s, err := Get("onboard-codebase-walkthrough")
	if err != nil {
		t.Fatal(err)
	}
	files, err := s.Files()
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if _, ok := files["SKILL.md"]; !ok {
		t.Errorf("Files missing SKILL.md; got keys %v", keysOf(files))
	}
	for rel := range files {
		if strings.HasPrefix(rel, "/") || strings.Contains(rel, "assets/") {
			t.Errorf("file path %q not relative to bundle root", rel)
		}
	}
}

func TestAllEmbeddedSkillsValid(t *testing.T) {
	all, err := List()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"onboard-architecture-cartographer", "onboard-codebase-walkthrough",
		"onboard-dependency-impact-analyzer", "onboard-guide-maintainer", "onboard-test-gap-and-risk-auditor",
	}
	have := map[string]bool{}
	for _, s := range all {
		have[s.Name] = true

		// Frontmatter contract enforced by the loader + agent runtimes.
		if s.Description == "" {
			t.Errorf("%s: empty description", s.Name)
		}
		if len(s.Description) > 1024 {
			t.Errorf("%s: description %d chars, exceeds 1024", s.Name, len(s.Description))
		}
		if strings.Contains(s.Description, "\n") {
			t.Errorf("%s: description must be a single line", s.Name)
		}
		// Every skill must render (SKILL.md present, references readable).
		out, err := s.Render()
		if err != nil {
			t.Errorf("%s: Render: %v", s.Name, err)
		}
		if len(out) < 200 {
			t.Errorf("%s: rendered skill suspiciously short (%d chars)", s.Name, len(out))
		}
		if !strings.Contains(out, "# ") {
			t.Errorf("%s: rendered skill has no markdown heading", s.Name)
		}
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("expected embedded skill %q", w)
		}
	}
}

// TestDescriptionsAreStrictYAMLSafe guards a class of frontmatter bug that onboard's
// own naive line parser (parseFrontmatter) does NOT catch but strict YAML parsers in
// other agent runtimes (e.g. Codex) reject: inside an unquoted plain scalar a colon-space
// ": " is read as a mapping separator, yielding "mapping values are not allowed in this
// context" and silently skipping the skill at load time. Because parseFrontmatter takes
// the raw remainder of the line, these fields must stay plain single-line scalars with no
// embedded ": ", no trailing ":", and no " #" comment marker. Reword (an em dash reads
// well) instead of quoting -- quoting would satisfy strict YAML but leak quote/escape
// characters into the description our naive parser hands to every agent.
func TestDescriptionsAreStrictYAMLSafe(t *testing.T) {
	all, err := List()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range all {
		if strings.Contains(s.Description, ": ") {
			t.Errorf("%s: description contains a colon-space \": \", which strict YAML reads as a "+
				"mapping; reword (e.g. use an em dash) so the value stays a valid plain scalar", s.Name)
		}
		if strings.HasSuffix(s.Description, ":") {
			t.Errorf("%s: description ends with ':', which strict YAML reads as a mapping key", s.Name)
		}
		if strings.Contains(s.Description, " #") {
			t.Errorf("%s: description contains \" #\", which strict YAML reads as a comment", s.Name)
		}
	}
}

// TestNewToolsReferencedBySkills guards the "orphaned tool" regression: a tool can be fully
// wired at the MCP layer (registered, tested, documented) yet be invisible in practice,
// because what actually drives an agent's tool calls is the SKILL prose, not the tool list.
// Every tool added after the original skill set must be referenced by at least one skill's
// workflow, or agents following the skills will never invoke it. Checks the onboard:-prefixed
// form so a bare common word ("schema", "routes") in prose does not satisfy the assertion.
func TestNewToolsReferencedBySkills(t *testing.T) {
	all, err := List()
	if err != nil {
		t.Fatal(err)
	}
	var corpus strings.Builder
	for _, s := range all {
		out, err := s.Render()
		if err != nil {
			t.Fatalf("%s: Render: %v", s.Name, err)
		}
		corpus.WriteString(out)
	}
	text := corpus.String()
	for _, tool := range []string{"repo_map", "history", "context_pack", "deps", "schema", "routes"} {
		if !strings.Contains(text, "onboard:"+tool) {
			t.Errorf("no skill references onboard:%s — a newly-added tool that no skill drives is "+
				"orphaned: agents following the skills will never call it. Wire it into a skill workflow.", tool)
		}
	}
}

func TestParseFrontmatter(t *testing.T) {
	md := "---\nname: demo\ndescription: a short thing\n---\n# Body\n"
	name, desc := parseFrontmatter(md)
	if name != "demo" {
		t.Errorf("name = %q", name)
	}
	if desc != "a short thing" {
		t.Errorf("desc = %q", desc)
	}

	if n, d := parseFrontmatter("# no frontmatter\n"); n != "" || d != "" {
		t.Errorf("expected empty for no frontmatter, got %q/%q", n, d)
	}
}

func names(s []Skill) []string {
	out := make([]string, len(s))
	for i, x := range s {
		out[i] = x.Name
	}
	return out
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
