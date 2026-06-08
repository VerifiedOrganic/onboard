// Package skills exposes the onboarding skill bundles that are compiled into the
// binary via //go:embed. The embedded assets directory is the single source of
// truth: the MCP resources, the get_skill tool, and the per-agent installer all
// read from it, so the skill content never forks.
package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Plain "assets" (not "all:assets") on purpose: Go's default embed excludes
// files and dirs whose names begin with "." or "_", so a stray .env, .DS_Store,
// or editor swap file in the tree can never be silently baked into the binary.
// Every real skill file is normally named, so this embeds the same content.
//
//go:embed assets
var assets embed.FS

// Skill is an embedded skill bundle: a SKILL.md plus optional reference files.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	dir         string // path within the embedded FS, e.g. "assets/onboard-codebase-walkthrough"
}

// List enumerates every embedded skill. Each subdirectory of assets/ that holds a
// SKILL.md is treated as one skill bundle.
func List() ([]Skill, error) {
	entries, err := fs.ReadDir(assets, "assets")
	if err != nil {
		return nil, err
	}
	var out []Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := path.Join("assets", e.Name())
		md, err := assets.ReadFile(path.Join(dir, "SKILL.md"))
		if err != nil {
			continue // not a skill bundle
		}
		name, desc := parseFrontmatter(string(md))
		if name == "" {
			name = e.Name()
		}
		out = append(out, Skill{Name: name, Description: desc, dir: dir})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Get returns a single embedded skill by name. Legacy unprefixed names are accepted for
// compatibility, but List advertises the namespaced onboard-* identifiers.
func Get(name string) (Skill, error) {
	name = CanonicalName(name)
	all, err := List()
	if err != nil {
		return Skill{}, err
	}
	for _, s := range all {
		if s.Name == name {
			return s, nil
		}
	}
	return Skill{}, fmt.Errorf("skill %q not found", name)
}

// Render returns the full skill payload: SKILL.md followed by every reference file,
// each delimited with a header. This is what get_skill hands to agents that cannot
// read MCP resources, so the workflow reaches every client through a tool call.
func (s Skill) Render() (string, error) {
	var b strings.Builder
	md, err := assets.ReadFile(path.Join(s.dir, "SKILL.md"))
	if err != nil {
		return "", err
	}
	b.Write(md)

	refs, _ := fs.ReadDir(assets, path.Join(s.dir, "references"))
	for _, r := range refs {
		if r.IsDir() {
			continue
		}
		content, err := assets.ReadFile(path.Join(s.dir, "references", r.Name()))
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "\n\n---\n\n# reference: %s\n\n%s", r.Name(), content)
	}
	return b.String(), nil
}

// Files returns relative-path -> content for the whole bundle. The installer writes
// these into an agent's native skills directory.
func (s Skill) Files() (map[string][]byte, error) {
	files := map[string][]byte{}
	err := fs.WalkDir(assets, s.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		content, err := assets.ReadFile(p)
		if err != nil {
			return err
		}
		files[strings.TrimPrefix(p, s.dir+"/")] = content
		return nil
	})
	return files, err
}

// parseFrontmatter extracts name and description from a SKILL.md YAML frontmatter
// block. It is line-based on purpose: it avoids a YAML dependency and the two fields
// we need are always single-line.
func parseFrontmatter(md string) (name, desc string) {
	lines := strings.Split(md, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", ""
	}
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) == "---" {
			break
		}
		switch {
		case strings.HasPrefix(l, "name:"):
			name = strings.TrimSpace(strings.TrimPrefix(l, "name:"))
		case strings.HasPrefix(l, "description:"):
			desc = strings.TrimSpace(strings.TrimPrefix(l, "description:"))
		}
	}
	return name, desc
}
