package server

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// deps extracts the EXTERNAL dependency graph straight from manifests — a deterministic fact,
// not a syntactic inference. It feeds the architecture-cartographer (which otherwise asks the
// LLM to guess a project's dependencies) and answers "what does this project pull in" without
// reading a line of source.

const (
	maxDepsManifests = 50 // bound the walk on sprawling monorepos
	maxMermaidDeps   = 30 // a dependency diagram past this stops being legible
)

type depsInput struct {
	Root   string `json:"root,omitempty" jsonschema:"repo root; defaults to the working directory"`
	Format string `json:"format,omitempty" jsonschema:"set to \"mermaid\" to also return a dependency flowchart; default returns structured data only"`
}

type dependency struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Dev     bool   `json:"dev,omitempty"` // development-only dependency
}

type manifestDeps struct {
	Manifest  string       `json:"manifest"`  // repo-relative path
	Ecosystem string       `json:"ecosystem"` // Go, JavaScript/TypeScript (npm), ...
	Module    string       `json:"module,omitempty"`
	Direct    []dependency `json:"direct"`
	Indirect  int          `json:"indirect,omitempty"` // count of indirect deps (go.mod)
}

type depsOutput struct {
	Manifests   []manifestDeps `json:"manifests"`
	TotalDirect int            `json:"total_direct"`
	Mermaid     string         `json:"mermaid,omitempty"`
	Truncated   bool           `json:"truncated,omitempty"`
	Note        string         `json:"note,omitempty"`
}

func depsExtract(_ context.Context, in depsInput) (depsOutput, error) {
	out := depsOutput{}
	root, err := resolveRoot(in.Root)
	if err != nil {
		return out, err
	}

	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && shouldSkipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if len(out.Manifests) >= maxDepsManifests {
			out.Truncated = true
			return fs.SkipDir
		}
		md, ok := parseManifest(root, p, d.Name())
		if ok {
			out.Manifests = append(out.Manifests, md)
		}
		return nil
	})

	sort.Slice(out.Manifests, func(i, j int) bool { return out.Manifests[i].Manifest < out.Manifests[j].Manifest })
	for _, m := range out.Manifests {
		out.TotalDirect += len(m.Direct)
	}

	if len(out.Manifests) == 0 {
		out.Note = "No recognized dependency manifests found (go.mod, package.json, requirements.txt, Cargo.toml)."
		return out, nil
	}
	if in.Format == "mermaid" {
		out.Mermaid, out.Truncated = renderDepsMermaid(out.Manifests)
	}
	out.Note = "Direct dependencies parsed from manifests (facts, not inferred). Versions reflect the manifest's declared constraint, not the resolved lockfile version."
	return out, nil
}

// Per-ecosystem manifest parsing (parseManifest, parseGoMod, parsePackageJSON,
// parseRequirements, parseCargoToml, and helpers) lives in manifests.go.

// renderDepsMermaid draws a flowchart of each manifest's module pointing at its direct
// dependencies, capping total dependency nodes so the diagram stays legible.
func renderDepsMermaid(manifests []manifestDeps) (string, bool) {
	var b strings.Builder
	b.WriteString("flowchart LR\n")
	shown, truncated := 0, false
	for mi, m := range manifests {
		rootID := fmt.Sprintf("m%d", mi)
		label := m.Module
		if label == "" {
			label = m.Manifest
		}
		b.WriteString(fmt.Sprintf("  %s[%q]\n", rootID, label))
		for di, dep := range m.Direct {
			if shown >= maxMermaidDeps {
				truncated = true
				break
			}
			depLabel := dep.Name
			if dep.Version != "" {
				depLabel += "@" + dep.Version
			}
			b.WriteString(fmt.Sprintf("  %s --> %s_%d[%q]\n", rootID, rootID, di, depLabel))
			shown++
		}
	}
	return b.String(), truncated
}

func registerDepsTool(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "deps",
		Description: "Extract the external dependency graph from a repo's manifests (go.mod, package.json, requirements.txt, Cargo.toml) — direct dependencies with declared versions per manifest, optionally as a Mermaid flowchart. Facts read from manifests, not inferred. Use to ground a dependency diagram or answer 'what does this project depend on'.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in depsInput) (*mcp.CallToolResult, depsOutput, error) {
		out, err := depsExtract(ctx, in)
		return nil, out, err
	})
}
