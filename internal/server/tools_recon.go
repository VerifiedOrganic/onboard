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
	"versions.tf":      "Terraform (HCL)",
	"terragrunt.hcl":   "Terragrunt",
	"root.hcl":         "Terragrunt",
}

// iacManifests are recorded in the manifests list but contribute a cleaner
// stack label than their filename would suggest.
var iacManifests = map[string]string{
	".terraform.lock.hcl": "Terraform (HCL)",
	".opentofu.lock.hcl":  "OpenTofu (HCL)",
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
	extCount := map[string]int{}

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
		ext := strings.ToLower(filepath.Ext(name))
		if ext != "" {
			extCount[ext]++
		}

		if eco, ok := manifests[name]; ok {
			stackSet[eco] = true
			out.Manifests = append(out.Manifests, rel)
		}
		if eco, ok := iacManifests[name]; ok {
			stackSet[eco] = true
			out.Manifests = append(out.Manifests, rel)
		}
		switch ext {
		case ".tf", ".tfvars":
			stackSet["Terraform (HCL)"] = true
		case ".tofu", ".tofuvars":
			stackSet["OpenTofu (HCL)"] = true
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
		case name == "terragrunt.hcl" || name == "root.hcl":
			fwSet["Terragrunt"] = true
		}

		switch strings.TrimSuffix(name, filepath.Ext(name)) {
		case "main", "index", "app", "server":
			if strings.Contains(rel, "test") {
				break
			}
			// A Terraform module's main.tf is internal plumbing, not an entry
			// point; only root modules (outside modules/) are deployable.
			if ext == ".tf" || ext == ".tofu" {
				if !strings.Contains(filepath.ToSlash(rel), "modules/") {
					out.EntryPoints = append(out.EntryPoints, rel)
				}
				break
			}
			out.EntryPoints = append(out.EntryPoints, rel)
		}
		if filepath.ToSlash(rel) == "src/lib.rs" {
			out.EntryPoints = append(out.EntryPoints, rel)
		}
		// Each terragrunt.hcl is a deployable unit — the IaC analogue of a main().
		if name == "terragrunt.hcl" {
			out.EntryPoints = append(out.EntryPoints, rel)
		}

		if strings.HasSuffix(name, "_test.go") ||
			strings.Contains(name, ".spec.") ||
			strings.Contains(name, ".test.") ||
			strings.HasPrefix(name, "test_") ||
			strings.HasSuffix(name, ".tftest.hcl") ||
			strings.HasSuffix(name, ".tofutest.hcl") ||
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
		case ext == ".rego":
			out.Tooling = addUnique(out.Tooling, "OPA/Conftest policies (Rego)")
		case name == ".tflint.hcl":
			out.Tooling = addUnique(out.Tooling, "TFLint")
		case name == "atlantis.yaml":
			out.Tooling = addUnique(out.Tooling, "Atlantis")
		case name == ".gitlab-ci.yml":
			out.Tooling = addUnique(out.Tooling, "GitLab CI")
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
		if hist, herr := git.History(ctx, root, reconHotspotCommits); herr == nil {
			out.Hotspots = topHotspots(hist, 8)
		}
	}

	var notes []string
	// An empty stack on a non-empty repo used to return silently, which read as
	// "nothing here" instead of "I don't speak this language". Name what was
	// actually seen so the gap is visible and actionable.
	if len(out.Stack) == 0 && out.FileCount > 0 {
		notes = append(notes, "No recognized manifests. Most common file extensions: "+
			topExtensions(extCount, 3)+
			" — if the repo's primary language is among these, stack and entry-point detection is degraded.")
	}
	if len(out.TestLayout) == 0 {
		notes = append(notes, "No test files detected — the Phase-2 behavioral map will be thin; lean harder on Phase-4 end-to-end traces.")
	}
	out.Note = strings.Join(notes, " ")
	return nil, out, nil
}

// topExtensions renders the n most common file extensions as ".tf (61), .hcl (12)".
func topExtensions(extCount map[string]int, n int) string {
	type ec struct {
		ext   string
		count int
	}
	ranked := make([]ec, 0, len(extCount))
	for e, c := range extCount {
		ranked = append(ranked, ec{e, c})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].count != ranked[j].count {
			return ranked[i].count > ranked[j].count
		}
		return ranked[i].ext < ranked[j].ext
	})
	if len(ranked) > n {
		ranked = ranked[:n]
	}
	parts := make([]string, 0, len(ranked))
	for _, r := range ranked {
		parts = append(parts, fmt.Sprintf("%s (%d)", r.ext, r.count))
	}
	if len(parts) == 0 {
		return "(none)"
	}
	return strings.Join(parts, ", ")
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
		if err != nil {
			return nil
		}
		if !d.IsDir() {
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
