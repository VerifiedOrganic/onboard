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
// not a syntactic inference. It feeds the onboard-architecture-cartographer (which otherwise asks the
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
	Name      string `json:"name"`
	Version   string `json:"version,omitempty"`
	Kind      string `json:"kind,omitempty"`   // normal | dev | build | peer | optional
	Target    string `json:"target,omitempty"` // target cfg for platform-specific deps
	Optional  bool   `json:"optional,omitempty"`
	Dev       bool   `json:"dev,omitempty"`       // development-only dependency
	Workspace bool   `json:"workspace,omitempty"` // true if it is a local workspace package
}

type rustTarget struct {
	Name       string   `json:"name"`
	Kind       []string `json:"kind,omitempty"`
	CrateTypes []string `json:"crate_types,omitempty"`
	SrcPath    string   `json:"src_path,omitempty"`
	Edition    string   `json:"edition,omitempty"`
}

type manifestDeps struct {
	Manifest              string            `json:"manifest"`  // repo-relative path
	Ecosystem             string            `json:"ecosystem"` // Go, JavaScript/TypeScript (npm), ...
	Module                string            `json:"module,omitempty"`
	Direct                []dependency      `json:"direct"`
	Indirect              int               `json:"indirect,omitempty"` // count of indirect deps (go.mod)
	Targets               []rustTarget      `json:"targets,omitempty"`  // Cargo targets, when Cargo metadata is available
	WorkspaceDependencies []string          `json:"workspace_dependencies,omitempty"`
	PackageManager        string            `json:"package_manager,omitempty"`
	Workspaces            []string          `json:"workspaces,omitempty"`
	Scripts               map[string]string `json:"scripts,omitempty"`
	Engines               map[string]string `json:"engines,omitempty"`
	DetectedTools         []string          `json:"detected_tools,omitempty"`
}

type depsOutput struct {
	Manifests   []manifestDeps `json:"manifests"`
	TotalDirect int            `json:"total_direct"`
	Mermaid     string         `json:"mermaid,omitempty"`
	Truncated   bool           `json:"truncated,omitempty"`
	Note        string         `json:"note,omitempty"`
}

func depsExtract(ctx context.Context, in depsInput) (depsOutput, error) {
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

	if cargo, ok := loadCargoMetadata(ctx, root); ok {
		for i := range out.Manifests {
			if md, found := cargo[out.Manifests[i].Manifest]; found {
				out.Manifests[i] = md
			}
		}
	}

	// Build local workspace dependency graph
	pkgToManifest := map[string]string{}
	for _, m := range out.Manifests {
		if m.Module != "" {
			pkgToManifest[m.Module] = m.Manifest
		}
	}

	for i := range out.Manifests {
		m := &out.Manifests[i]
		var workspaceDeps []string
		for j := range m.Direct {
			dep := &m.Direct[j]
			if targetManifest, exists := pkgToManifest[dep.Name]; exists {
				dep.Workspace = true
				workspaceDeps = append(workspaceDeps, targetManifest)
			}
		}
		sort.Strings(workspaceDeps)
		var uniqueWorkspaceDeps []string
		for k, w := range workspaceDeps {
			if k == 0 || w != workspaceDeps[k-1] {
				uniqueWorkspaceDeps = append(uniqueWorkspaceDeps, w)
			}
		}
		m.WorkspaceDependencies = uniqueWorkspaceDeps
	}

	sort.Slice(out.Manifests, func(i, j int) bool { return out.Manifests[i].Manifest < out.Manifests[j].Manifest })
	for _, m := range out.Manifests {
		out.TotalDirect += len(m.Direct)
	}

	if len(out.Manifests) == 0 {
		out.Note = "No recognized dependency manifests found (go.mod, package.json, requirements.txt, Cargo.toml, versions.tf, .terraform.lock.hcl)."
		return out, nil
	}
	if in.Format == "mermaid" {
		out.Mermaid, out.Truncated = renderDepsMermaid(out.Manifests)
	}
	out.Note = "Direct dependencies parsed from manifests (facts, not inferred). Rust manifests are upgraded with `cargo metadata --no-deps` when available; versions reflect declared constraints, not resolved lockfile versions. Terraform/OpenTofu providers appear twice when a lock file is present: the declared constraint (kind: provider) and the pinned version (kind: locked)."
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
		Description: "Extract the external dependency graph from a repo's manifests (go.mod, package.json, requirements.txt, Cargo.toml, Terraform required_providers + lock files, external module sources) — direct dependencies with declared versions per manifest, optionally as a Mermaid flowchart. Facts read from manifests, not inferred. Use to ground a dependency diagram or answer 'what does this project depend on'.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in depsInput) (*mcp.CallToolResult, depsOutput, error) {
		out, err := depsExtract(ctx, in)
		return nil, out, err
	})
}
