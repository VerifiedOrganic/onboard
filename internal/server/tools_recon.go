package server

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/VerifiedOrganic/onboard/internal/git"
	"github.com/VerifiedOrganic/onboard/internal/ignore"
)

// reconHotspotCommits bounds the history recon scans for its quick hotspot summary; the
// dedicated history tool exposes the full, configurable view.
const reconHotspotCommits = 1000
const maxReconRiskHints = 20

type reconInput struct {
	Root string `json:"root,omitempty" jsonschema:"absolute path to the repository root to analyze; defaults to the server's working directory"`
}

type reconOutput struct {
	Root        string   `json:"root"`
	Stack       []string `json:"stack"`        // ecosystems inferred from manifests
	Manifests   []string `json:"manifests"`    // manifest files found (relative paths)
	Frameworks  []string `json:"frameworks"`   // framework fingerprints
	EntryPoints []string `json:"entry_points"` // likely program entry points
	TestLayout  []string `json:"test_layout"`  // directories that contain test files
	Tooling     []string `json:"tooling"`      // docker, CI, linters, build tooling
	RustTargets []string `json:"rust_targets,omitempty"`
	RiskHints   []string `json:"risk_hints,omitempty"` // capped static hints for risky patterns
	DirTree     []string `json:"dir_tree"`             // top two directory levels, pruned
	Hotspots    []string `json:"hotspots,omitempty"`   // highest-churn files (git only); look here first
	FileCount   int      `json:"file_count"`
	Note        string   `json:"note,omitempty"`
}

// manifest filename -> ecosystem label
var manifests = map[string]string{
	"package.json":     "JavaScript/TypeScript (npm)",
	"go.mod":           "Go",
	"Cargo.toml":       "Rust",
	"pyproject.toml":   "Python",
	"requirements.txt": "Python",
	"setup.py":         "Python",
	"pom.xml":          "Java (Maven)",
	"build.gradle":     "Java/Kotlin (Gradle)",
	"Gemfile":          "Ruby",
	"composer.json":    "PHP",
	"mix.exs":          "Elixir",
	"pubspec.yaml":     "Dart/Flutter",
	"Package.swift":    "Swift",
}

func registerReconTool(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "recon",
		Description: "Phase-1 reconnaissance: detect stack, frameworks, entry points, test layout, tooling, a pruned directory tree, and (in a git repo) the highest-churn hotspot files for a repository. A fast structural scan — reads no source beyond manifest filenames.",
	}, recon)
}

func recon(ctx context.Context, _ *mcp.CallToolRequest, in reconInput) (*mcp.CallToolResult, reconOutput, error) {
	root, err := resolveRoot(in.Root)
	if err != nil {
		return nil, reconOutput{}, err
	}
	out := reconOutput{Root: root}

	stackSet := map[string]bool{}
	fwSet := map[string]bool{}

	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // tolerate unreadable entries instead of aborting
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && shouldSkipDir(name) {
				return fs.SkipDir
			}
			return nil
		}
		out.FileCount++
		rel, _ := filepath.Rel(root, p)

		if eco, ok := manifests[name]; ok {
			stackSet[eco] = true
			out.Manifests = append(out.Manifests, rel)
		}

		switch {
		case strings.HasPrefix(name, "next.config."):
			fwSet["Next.js"] = true
		case strings.HasPrefix(name, "vite.config."):
			fwSet["Vite"] = true
		case strings.HasPrefix(name, "nuxt.config."):
			fwSet["Nuxt"] = true
		case name == "angular.json":
			fwSet["Angular"] = true
		case strings.HasPrefix(name, "svelte.config."):
			fwSet["Svelte"] = true
		case name == "manage.py":
			fwSet["Django"] = true
		case strings.HasPrefix(name, "tailwind.config."):
			fwSet["Tailwind"] = true
		}

		switch strings.TrimSuffix(name, filepath.Ext(name)) {
		case "main", "index", "app", "server":
			if !strings.Contains(rel, "test") {
				out.EntryPoints = append(out.EntryPoints, rel)
			}
		}
		if filepath.ToSlash(rel) == "src/lib.rs" {
			out.EntryPoints = append(out.EntryPoints, rel)
		}

		if strings.HasSuffix(name, "_test.go") ||
			strings.Contains(name, ".spec.") ||
			strings.Contains(name, ".test.") ||
			strings.HasPrefix(name, "test_") ||
			isRustTestPath(rel) {
			out.TestLayout = addUnique(out.TestLayout, filepath.Dir(rel))
		}
		if strings.HasSuffix(name, ".rs") {
			hasTests, risks := scanRustFileSignals(p, rel, maxReconRiskHints-len(out.RiskHints))
			if hasTests {
				out.TestLayout = addUnique(out.TestLayout, filepath.Dir(rel))
			}
			out.RiskHints = append(out.RiskHints, risks...)
		}

		switch {
		case name == "Dockerfile" || strings.HasPrefix(name, "docker-compose"):
			out.Tooling = addUnique(out.Tooling, "Docker")
		case name == ".env.example":
			out.Tooling = addUnique(out.Tooling, "env config (.env.example)")
		case strings.HasPrefix(name, ".eslintrc"):
			out.Tooling = addUnique(out.Tooling, "ESLint")
		case name == ".golangci.yml" || name == ".golangci.yaml":
			out.Tooling = addUnique(out.Tooling, "golangci-lint")
		case name == "Makefile":
			out.Tooling = addUnique(out.Tooling, "Make")
		}
		return nil
	})
	if err != nil {
		return nil, out, err
	}

	if entries, e := os.ReadDir(filepath.Join(root, ".github", "workflows")); e == nil && len(entries) > 0 {
		out.Tooling = addUnique(out.Tooling, "GitHub Actions")
	}
	if stackSet["Rust"] {
		if cargo, ok := loadCargoMetadata(ctx, root); ok {
			out.RustTargets = cargoTargetSummaries(cargo)
			for _, md := range cargo {
				for _, target := range md.Targets {
					for _, kind := range target.Kind {
						if kind == "bin" || kind == "lib" {
							out.EntryPoints = append(out.EntryPoints, target.SrcPath)
							break
						}
					}
				}
			}
		}
	}

	out.Stack = keys(stackSet)
	out.Frameworks = keys(fwSet)
	out.DirTree = dirTree(root, 2)
	sort.Strings(out.Manifests)
	sort.Strings(out.EntryPoints)
	sort.Strings(out.TestLayout)
	sort.Strings(out.Tooling)
	sort.Strings(out.RustTargets)
	sort.Strings(out.RiskHints)
	out.EntryPoints = dedupeStrings(out.EntryPoints)

	// Git churn hotspots: the files that change most are where understanding and risk
	// concentrate, so point an onboarding reader at them first. Degrades silently outside
	// a git work tree — the dedicated history tool gives the full view.
	if git.Available(root) {
		if hist, herr := git.History(root, reconHotspotCommits); herr == nil {
			out.Hotspots = topHotspots(hist, 8)
		}
	}

	if len(out.TestLayout) == 0 {
		out.Note = "No test files detected — the Phase-2 behavioral map will be thin; lean harder on Phase-4 end-to-end traces."
	}
	return nil, out, nil
}

func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	sort.Strings(in)
	out := in[:0]
	var prev string
	for i, v := range in {
		if i == 0 || v != prev {
			out = append(out, v)
			prev = v
		}
	}
	return out
}

// topHotspots formats the n highest-churn files (git.History is already sorted by churn)
// into compact one-line summaries an onboarding reader can scan.
func topHotspots(hist []git.FileStat, n int) []string {
	if len(hist) > n {
		hist = hist[:n]
	}
	out := make([]string, 0, len(hist))
	for _, fs := range hist {
		out = append(out, fmt.Sprintf("%s — %d commits, %d authors, last %s",
			fs.Path, fs.Commits, fs.Authors, fs.LastDate))
	}
	return out
}

// shouldSkipDir prunes the shared dependency/build directories plus dotdirs, but keeps
// .github (recon detects CI workflows there).
func shouldSkipDir(name string) bool {
	if ignore.Dir(name) {
		return true
	}
	return strings.HasPrefix(name, ".") && name != ".github"
}

func dirTree(root string, maxDepth int) []string {
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "." {
			return nil
		}
		if shouldSkipDir(d.Name()) {
			return fs.SkipDir
		}
		if len(strings.Split(rel, string(filepath.Separator))) > maxDepth {
			return fs.SkipDir
		}
		out = append(out, rel+"/")
		return nil
	})
	sort.Strings(out)
	return out
}

func addUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
