package server

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/VerifiedOrganic/onboard/internal/providers"
)

// dead_code surfaces callable definitions (functions and methods) that nothing in the
// indexed graph calls — a strong lead for code an autonomous build wrote but never wired
// in. It is a *lead*, not a verdict: the syntactic graph cannot see calls made via
// interface dispatch, reflection, framework/DI registration, build-tagged files, or
// external importers, so results are ranked by how likely they are to be truly dead and
// the note spells out what could hide a caller.

type deadCodeInput struct {
	Root    string `json:"root,omitempty" jsonschema:"repo root; defaults to the working directory"`
	Limit   int    `json:"limit,omitempty" jsonschema:"max orphans to return, highest-confidence first (default 50)"`
	Precise bool   `json:"precise,omitempty" jsonschema:"for Go modules, resolve interface-dispatch callers; for Rust Cargo projects, enrich with rust-analyzer call hierarchy when available; slower, requires the language toolchain"`
	Refresh bool   `json:"refresh,omitempty" jsonschema:"re-index the repo instead of using the cached graph"`
}

type orphan struct {
	QName      string `json:"qname"`
	Symbol     string `json:"symbol"` // display name (receiver-qualified for methods)
	File       string `json:"file"`
	Line       int    `json:"line"`
	Kind       string `json:"kind"` // function | method
	Exported   bool   `json:"exported"`
	Confidence string `json:"confidence"` // high | medium | low
	Reason     string `json:"reason"`
}

type deadCodeOutput struct {
	Orphans    []orphan `json:"orphans"`
	Scanned    int      `json:"scanned_callables"` // functions + methods considered
	TotalFound int      `json:"total_found"`
	Truncated  bool     `json:"truncated"`
	Provider   string   `json:"provider"`
	Note       string   `json:"note,omitempty"`
}

func deadCode(ctx context.Context, in deadCodeInput) (deadCodeOutput, error) {
	out := deadCodeOutput{}
	root, err := resolveRoot(in.Root)
	if err != nil {
		return out, err
	}
	g, err := indexGraph(ctx, root, in.Refresh, in.Precise)
	if err != nil {
		return out, err
	}
	out.Provider = g.Provider
	if g.Provider == "null" {
		out.Note = "Definitions-only provider: no call graph, so callers cannot be determined and dead code cannot be inferred."
		return out, nil
	}

	var orphans []orphan
	for q, sym := range g.Defs {
		if sym.Kind != "function" && sym.Kind != "method" {
			continue // only callables can be "uncalled"
		}
		out.Scanned++
		if isTestSymbol(sym) || isEntryName(sym.Name) || isFrameworkOrEntrySymbol(sym) {
			continue // toolchain/runtime-invoked or framework entry point: never dead
		}
		if len(g.Callers(q)) > 0 {
			continue
		}
		exported := symbolExported(sym)
		conf, reason := orphanConfidence(symbolCallableKind(sym), exported, g.Precise)
		if isReactComponent(sym) {
			conf = "low"
			reason = "React component or class — framework managed lifecycle"
		} else if isAngularFile(sym.File) {
			conf = "low"
			reason = "Angular component/service method — may be called from HTML template or dependency injection"
		} else if isPublicEntrypoint(sym.File) {
			conf = "low"
			reason = "Symbol in public entry point — likely part of package exports"
		}
		orphans = append(orphans, orphan{
			QName: q, Symbol: sym.Display(), File: sym.File, Line: sym.Line,
			Kind: sym.Kind, Exported: exported, Confidence: conf, Reason: reason,
		})
	}
	out.TotalFound = len(orphans)

	// Highest confidence first, then file/line for a stable, reviewable order.
	rank := map[string]int{"high": 0, "medium": 1, "low": 2}
	sort.Slice(orphans, func(i, j int) bool {
		if rank[orphans[i].Confidence] != rank[orphans[j].Confidence] {
			return rank[orphans[i].Confidence] < rank[orphans[j].Confidence]
		}
		if orphans[i].File != orphans[j].File {
			return orphans[i].File < orphans[j].File
		}
		return orphans[i].Line < orphans[j].Line
	})

	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	if len(orphans) > limit {
		orphans = orphans[:limit]
		out.Truncated = true
	}
	out.Orphans = orphans
	out.Note = deadCodeNote(g, in.Precise)
	return out, nil
}

func isSvelteKitEntry(sym *providers.Symbol) bool {
	if sym == nil {
		return false
	}
	base := filepath.Base(sym.File)
	if strings.HasSuffix(sym.File, ".svelte") {
		if strings.Contains(base, "+page") || strings.Contains(base, "+layout") {
			return true
		}
	}
	if strings.HasPrefix(base, "+page.") || strings.HasPrefix(base, "+layout.") || strings.HasPrefix(base, "+server.") {
		name := sym.Name
		if name == "load" || name == "actions" || name == "default" {
			return true
		}
		switch name {
		case "GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS", "HEAD":
			return true
		}
	}
	return false
}

func isNextEntry(sym *providers.Symbol) bool {
	if sym == nil {
		return false
	}
	base := filepath.Base(sym.File)
	slashed := "/" + filepath.ToSlash(sym.File) + "/"
	if strings.Contains(slashed, "/app/") || strings.Contains(slashed, "/pages/") {
		name := sym.Name
		if base == "page.tsx" || base == "page.ts" || base == "page.jsx" || base == "page.js" ||
			base == "layout.tsx" || base == "layout.js" || base == "layout.jsx" || base == "layout.ts" ||
			base == "error.tsx" || base == "loading.tsx" || base == "not-found.tsx" {
			if name == "default" || (len(name) > 0 && unicode.IsUpper(rune(name[0]))) {
				return true
			}
		}
		if base == "route.ts" || base == "route.js" {
			switch name {
			case "GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS", "HEAD":
				return true
			}
		}
		if strings.Contains(slashed, "/pages/") {
			if name == "default" || (len(name) > 0 && unicode.IsUpper(rune(name[0]))) || name == "getServerSideProps" || name == "getStaticProps" || name == "getStaticPaths" {
				return true
			}
		}
	}
	return false
}

func isAngularFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	return strings.Contains(base, ".component.") || strings.Contains(base, ".service.") || strings.Contains(base, ".module.") || strings.Contains(base, ".directive.") || strings.Contains(base, ".pipe.")
}

func isAngularLifecycleHook(name string) bool {
	switch name {
	case "ngOnInit", "ngOnChanges", "ngDoCheck", "ngAfterContentInit", "ngAfterContentChecked", "ngAfterViewInit", "ngAfterViewChecked", "ngOnDestroy":
		return true
	}
	return false
}

func isStorybookFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	return strings.Contains(base, ".stories.") || strings.Contains(base, ".story.")
}

func isGeneratedFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	return strings.Contains(base, ".pb.go") ||
		strings.Contains(base, ".gen.") ||
		strings.Contains(base, "generated") ||
		strings.HasPrefix(base, "mock_")
}

func isPublicEntrypoint(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	return base == "index.ts" || base == "index.js" || base == "index.tsx" || base == "index.jsx" || base == "lib.go"
}

func isReactComponent(sym *providers.Symbol) bool {
	if sym == nil {
		return false
	}
	if sym.Lang != "javascript" && sym.Lang != "typescript" && sym.Lang != "tsx" && sym.Lang != "svelte" {
		return false
	}
	if len(sym.Name) > 0 && unicode.IsUpper(rune(sym.Name[0])) {
		return true
	}
	return false
}

func isRemixOrReactRouterEntry(sym *providers.Symbol) bool {
	if sym == nil {
		return false
	}
	if sym.Lang != "javascript" && sym.Lang != "typescript" && sym.Lang != "tsx" {
		return false
	}
	name := sym.Name
	if name == "loader" || name == "action" || name == "headers" || name == "meta" || name == "default" {
		return true
	}
	slashed := "/" + filepath.ToSlash(sym.File)
	if strings.Contains(slashed, "/routes/") {
		if len(name) > 0 && unicode.IsUpper(rune(name[0])) {
			return true
		}
	}
	return false
}

func isFrameworkOrEntrySymbol(sym *providers.Symbol) bool {
	if sym == nil {
		return false
	}
	if isSvelteKitEntry(sym) {
		return true
	}
	if isNextEntry(sym) {
		return true
	}
	if isRemixOrReactRouterEntry(sym) {
		return true
	}
	if isAngularLifecycleHook(sym.Name) {
		return true
	}
	if isStorybookFile(sym.File) {
		return true
	}
	if isGeneratedFile(sym.File) {
		return true
	}
	return false
}

// isEntryName matches names invoked by a runtime/toolchain rather than by an in-repo
// caller, so they must never be reported as dead even with zero callers.
func isEntryName(name string) bool {
	switch name {
	case "main", "init":
		return true
	}
	// Go test/benchmark/fuzz/example entry points: also guarded by isTestFile, but a
	// stray one outside a _test.go file is still toolchain-invoked.
	for _, p := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// isExported applies the common visibility conventions: a leading uppercase rune (Go and
// other export-by-capitalization languages) means exported; a leading underscore (Python,
// JS) means private. A heuristic — the note flags it as such.
func isExported(name string) bool {
	if name == "" {
		return false
	}
	r := []rune(name)[0]
	if r == '_' {
		return false
	}
	return unicode.IsUpper(r)
}

func symbolExported(sym *providers.Symbol) bool {
	if sym == nil {
		return false
	}
	if strings.EqualFold(sym.Lang, "rust") {
		return sym.Public
	}
	return isExported(sym.Name)
}

func symbolCallableKind(sym *providers.Symbol) string {
	if sym == nil {
		return ""
	}
	if sym.Kind == "method" || sym.Recv != "" {
		return "method"
	}
	return sym.Kind
}

// orphanConfidence ranks how likely an uncalled callable is *truly* dead, given what the
// graph can and cannot see. Methods are the weakest case without type-checked dispatch;
// exported functions may serve external importers; unexported functions are the strongest
// signal because nothing outside the repo can reach them.
func orphanConfidence(kind string, exported, precise bool) (confidence, reason string) {
	switch {
	case kind == "method" && !precise:
		return "low", "method with no syntactic caller — may be reached via dispatch the syntactic graph cannot resolve; pass precise:true to confirm when a semantic backend is available"
	case kind == "method":
		return "medium", "method with no caller even after available semantic dispatch resolution"
	case exported:
		return "medium", "exported function with no in-repo caller — may be public API or used by an external importer"
	default:
		return "high", "unexported function with no caller — unreachable within this repo"
	}
}

func deadCodeNote(g *providers.Graph, requestedPrecise bool) string {
	base := "Leads, not verdicts: the syntactic graph cannot see calls via reflection, code generation, " +
		"framework/DI registration, build-tagged files, or external importers — verify before deleting. " +
		"Entry points, framework-managed lifecycles (React, SvelteKit, Next.js, Angular, Storybook), " +
		"generated files, and test functions are already excluded or marked low-confidence."
	if requestedPrecise && !g.Precise {
		return base + " " + semanticPrecisionUnavailableNote() + edgeCaveat(g)
	}
	return base + goPrecisionHint(g, requestedPrecise) + precisionNotes(g)
}

func registerDeadCodeTool(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "dead_code",
		Description: "Find callable definitions (functions and methods) that nothing in the repo calls — a lead for code that was written but never wired in (common in fast/AI builds). Ranked by confidence; excludes entry points and tests. Leads, not proof: reflection, codegen, framework registration, and external importers can hide callers (pass precise:true for Go or Rust semantic enrichment when available).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in deadCodeInput) (*mcp.CallToolResult, deadCodeOutput, error) {
		out, err := deadCode(ctx, in)
		return nil, out, err
	})
}
