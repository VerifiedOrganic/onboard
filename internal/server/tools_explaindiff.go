package server

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"sort"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/VerifiedOrganic/onboard/internal/git"
	"github.com/VerifiedOrganic/onboard/internal/providers"
)

// explain_diff scopes onboarding to a change set: what a branch/PR touched, the symbols
// inside those touched lines, and the blast radius of each (transitive callers + at-risk
// tests). It turns onboard from a one-time orientation tool into a per-change review
// companion. Changed lines come from git; symbol attribution and impact come from the code
// graph the other tools share.

type explainDiffInput struct {
	Root    string `json:"root,omitempty" jsonschema:"repo root; defaults to the working directory"`
	Base    string `json:"base,omitempty" jsonschema:"git ref to compare against (branch, tag, or SHA); defaults to the merge-base with the default branch (origin/main, main, master)"`
	Limit   int    `json:"limit,omitempty" jsonschema:"max changed symbols to detail, by blast radius (default 50)"`
	Precise bool   `json:"precise,omitempty" jsonschema:"for Go modules, use type-checked edges; for Rust Cargo projects, use rust-analyzer call hierarchy when available; slower, requires the language toolchain"`
	Refresh bool   `json:"refresh,omitempty" jsonschema:"re-index the repo instead of using the cached graph"`
}

type changedFile struct {
	Path   string `json:"path"`
	Status string `json:"status"` // A, M, D, R
	Hunks  int    `json:"hunks"`
}

type changedSymbol struct {
	QName         string `json:"qname"`
	Symbol        string `json:"symbol"` // display name (receiver-qualified for methods)
	File          string `json:"file"`
	Line          int    `json:"line"`
	Kind          string `json:"kind"`
	DirectCallers int    `json:"direct_callers"`
	ImpactedCount int    `json:"impacted_count"` // transitive callers of this symbol
}

type explainDiffOutput struct {
	Base           string          `json:"base,omitempty"`
	ChangedFiles   []changedFile   `json:"changed_files"`
	ChangedSymbols []changedSymbol `json:"changed_symbols"`
	AtRiskTests    []string        `json:"at_risk_tests"`
	ImpactedCount  int             `json:"impacted_count"` // size of the union of transitive callers
	Truncated      bool            `json:"truncated"`
	Provider       string          `json:"provider,omitempty"`
	Note           string          `json:"note,omitempty"`
}

// reviewableKinds are the definition kinds worth reporting as "changed symbols" — the ones
// whose blast radius is meaningful. Bare variables/constants/imports are skipped as noise.
var reviewableKinds = map[string]bool{
	"function": true, "method": true, "type": true,
	"class": true, "interface": true, "constructor": true,
}

func explainDiff(ctx context.Context, in explainDiffInput) (explainDiffOutput, error) {
	out := explainDiffOutput{}
	root, err := resolveRoot(in.Root)
	if err != nil {
		return out, err
	}
	if !git.Available(root) {
		out.Note = "Not a git repository — nothing to diff."
		return out, nil
	}

	base := in.Base
	if base == "" {
		if base = git.DefaultBase(root); base == "" {
			out.Note = "Could not detect a base branch (origin/main, main, master). Pass `base` (a branch, tag, or SHA) explicitly."
			return out, nil
		}
	}
	out.Base = base
	if err := git.ValidateRef(root, base); err != nil {
		out.Note = err.Error()
		return out, nil
	}

	diffs, err := git.Diff(root, base)
	if err != nil {
		return out, err
	}
	for _, d := range diffs {
		out.ChangedFiles = append(out.ChangedFiles, changedFile{Path: d.Path, Status: d.Status, Hunks: len(d.Hunks)})
	}
	if len(diffs) == 0 {
		out.Note = "No changes between " + base + " and the working tree."
		return out, nil
	}

	g, err := indexGraph(ctx, root, in.Refresh, in.Precise)
	if err != nil {
		return out, err
	}
	out.Provider = g.Provider
	deletions := false
	for _, d := range diffs {
		if d.Status == "D" {
			deletions = true
			break
		}
	}
	var baseGraph *providers.Graph
	if deletions {
		baseGraph, err = diffBaseGraph(ctx, root, base, in.Precise)
		if err != nil {
			return out, err
		}
	}

	// Group definitions by file, sorted by line, so each symbol's body extent can be
	// approximated as [line, nextSymbolLine) for attributing changed lines.
	defsByFile := map[string][]*providers.Symbol{}
	for _, s := range g.Defs {
		if s == nil {
			continue
		}
		f := filepath.ToSlash(s.File)
		defsByFile[f] = append(defsByFile[f], s)
	}
	for _, defs := range defsByFile {
		sort.Slice(defs, func(i, j int) bool { return defs[i].Line < defs[j].Line })
	}

	impactedUnion := map[string]bool{}
	atRiskTests := map[string]bool{}
	for _, d := range diffs {
		if d.Status == "D" {
			if baseGraph == nil {
				continue
			}
			for _, sym := range deletedFileSymbols(baseGraph, d.Path) {
				if !reviewableKinds[sym.Kind] {
					continue
				}
				addChangedSymbol(&out, baseGraph, sym, impactedUnion, atRiskTests)
			}
			continue
		}
		if len(d.Hunks) == 0 {
			continue
		}
		for _, sym := range touchedSymbols(defsByFile[filepath.ToSlash(d.Path)], d.Hunks) {
			if !reviewableKinds[sym.Kind] {
				continue
			}
			addChangedSymbol(&out, g, sym, impactedUnion, atRiskTests)
		}
	}

	// Widest blast radius first — that's what a reviewer should look at hardest.
	sort.Slice(out.ChangedSymbols, func(i, j int) bool {
		a, b := out.ChangedSymbols[i], out.ChangedSymbols[j]
		if a.ImpactedCount != b.ImpactedCount {
			return a.ImpactedCount > b.ImpactedCount
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})

	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	if len(out.ChangedSymbols) > limit {
		out.ChangedSymbols = out.ChangedSymbols[:limit]
		out.Truncated = true
	}

	out.AtRiskTests = sortedSet(atRiskTests)
	out.ImpactedCount = len(impactedUnion)
	deletionNote := ""
	if deletions {
		deletionNote = " Deleted files are analyzed against the base tree, because their symbols no longer exist in the working tree."
	}
	out.Note = "Changed symbols are attributed by line range (a symbol spans from its declaration to the next one), so a change in a file's preamble may not map to any symbol." + deletionNote + " Blast radius via the call graph: " +
		edgeCaveat(g) + goPrecisionHint(g, in.Precise)
	if in.Precise && !g.Precise {
		out.Note = "Changed symbols are attributed by line range (a symbol spans from its declaration to the next one), so a change in a file's preamble may not map to any symbol." + deletionNote + " Blast radius via the call graph: " +
			semanticPrecisionUnavailableNote() + edgeCaveat(g)
	}
	return out, nil
}

func diffBaseGraph(ctx context.Context, root, base string, precise bool) (*providers.Graph, error) {
	tmp, err := os.MkdirTemp("", "onboard-base-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := git.ArchiveTree(ctx, root, base, tmp); err != nil {
		return nil, err
	}
	g, err := (providers.Builtin{}).Index(ctx, tmp)
	if err != nil {
		return nil, err
	}
	if g.Files == 0 {
		if ng, nerr := (providers.Null{}).Index(ctx, tmp); nerr == nil && len(ng.Defs) > 0 {
			g = ng
		}
	}
	if precise {
		_, _ = providers.EnrichGo(ctx, tmp, g)
		_, _ = providers.EnrichRust(ctx, tmp, g)
	}
	return g, nil
}

func deletedFileSymbols(g *providers.Graph, path string) []*providers.Symbol {
	slashed := filepath.ToSlash(path)
	var out []*providers.Symbol
	for _, sym := range g.Defs {
		if sym == nil {
			continue
		}
		if filepath.ToSlash(sym.File) == slashed {
			out = append(out, sym)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out
}

func addChangedSymbol(out *explainDiffOutput, g *providers.Graph, sym *providers.Symbol, impactedUnion, atRiskTests map[string]bool) {
	trans := g.Impact(sym.QName)
	for _, q := range trans {
		impactedUnion[q] = true
		if isTestQName(q, g) {
			atRiskTests[q] = true
		}
	}
	out.ChangedSymbols = append(out.ChangedSymbols, changedSymbol{
		QName: sym.QName, Symbol: sym.Display(), File: sym.File, Line: sym.Line,
		Kind: sym.Kind, DirectCallers: len(g.Callers(sym.QName)), ImpactedCount: len(trans),
	})
}

// touchedSymbols returns the definitions in fileDefs (sorted by line) whose extent overlaps
// any changed hunk. A symbol's extent is approximated as [its line, the next symbol's line),
// which is the best available bound without storing full body spans.
func touchedSymbols(fileDefs []*providers.Symbol, hunks []git.Hunk) []*providers.Symbol {
	var out []*providers.Symbol
	for i, s := range fileDefs {
		start := s.Line
		end := math.MaxInt
		if i+1 < len(fileDefs) {
			end = fileDefs[i+1].Line - 1
		}
		if end < start {
			end = start
		}
		for _, h := range hunks {
			if h.Start <= end && h.End >= start { // ranges overlap
				out = append(out, s)
				break
			}
		}
	}
	return out
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func registerExplainDiffTool(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "explain_diff",
		Description: "Explain a branch/PR: the files it changed, the symbols inside the changed lines, and each one's blast radius (transitive callers + at-risk tests). Scopes onboarding to a change set so it can run on every PR, not just once. Defaults the base to the merge-base with the default branch; pass `base` to override. Blast radius is syntactic (pass precise:true for type-checked Go).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in explainDiffInput) (*mcp.CallToolResult, explainDiffOutput, error) {
		out, err := explainDiff(ctx, in)
		return nil, out, err
	})
}
