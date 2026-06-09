package server

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/VerifiedOrganic/onboard/internal/git"
	"github.com/VerifiedOrganic/onboard/internal/providers"
)

// Shared code-graph indexing used by every graph-backed tool (trace_flow, impact, repo_map,
// context_pack, render_map): a per-root in-memory cache, per-key locking, the persistent
// on-disk index path, optional type-checked enrichment, and the small honesty/classification
// helpers the tools share. Kept here, not in any one tool's file, because it is cross-cutting.

// Indexing walks the whole repo, so cache the graph per root for the server's
// lifetime. Callers pass refresh=true to rebuild after the tree changes.
var (
	graphCacheMu sync.Mutex
	graphCache   = map[string]*providers.Graph{}
	graphLocks   = map[string]*sync.Mutex{}
)

func cachedGraph(root string) (*providers.Graph, bool) {
	graphCacheMu.Lock()
	defer graphCacheMu.Unlock()
	g, ok := graphCache[root]
	return g, ok
}

func rootLock(root string) *sync.Mutex {
	graphCacheMu.Lock()
	defer graphCacheMu.Unlock()
	l := graphLocks[root]
	if l == nil {
		l = &sync.Mutex{}
		graphLocks[root] = l
	}
	return l
}

func indexGraph(ctx context.Context, root string, refresh, precise bool) (*providers.Graph, error) {
	// The precise (type-checked) graph is a superset of the syntactic one, so cache it under
	// a distinct key — a non-precise caller must never get charged for, nor accidentally
	// receive, the heavier enriched graph, and vice versa.
	key := root
	if precise {
		key = root + "\x00precise"
	}
	if !refresh {
		if g, ok := cachedGraph(key); ok {
			return g, nil
		}
	}
	// Serialize indexing PER KEY so concurrent calls for the same repo don't
	// duplicate the walk — without the old global lock that blocked cache hits and
	// unrelated repos for the entire multi-second Index.
	l := rootLock(key)
	l.Lock()
	defer l.Unlock()
	if !refresh {
		if g, ok := cachedGraph(key); ok { // another goroutine may have built it
			return g, nil
		}
	}
	g, err := (providers.Builtin{}).IndexWithCache(ctx, root, graphCachePath(root))
	if err != nil {
		return nil, err
	}
	// If tree-sitter matched nothing, fall back to the definitions-only provider
	// so the user at least gets a symbol list rather than an empty result.
	if g.Files == 0 {
		if ng, nerr := (providers.Null{}).Index(ctx, root); nerr == nil && len(ng.Defs) > 0 {
			g = ng
		}
	}
	// Optional semantic enrichment. Safe to mutate g in place: it was just built here and
	// is not yet shared. Non-fatal — missing language toolchains leave g syntactic.
	if precise {
		_, _ = providers.EnrichGo(ctx, root, g)
		_, _ = providers.EnrichRust(ctx, root, g)
	}
	graphCacheMu.Lock()
	graphCache[key] = g
	graphCacheMu.Unlock()
	return g, nil
}

// graphCachePath returns where the persistent per-file index lives, or "" to disable
// persistence. In a git repo it sits inside the common git dir (alongside the guide
// cache) so it is never committed; outside a repo we skip persistence rather than
// litter an untracked working tree with a .onboard directory on every graph query.
func graphCachePath(root string) string {
	dir, err := git.CommonDir(root)
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "onboard-graph.json")
}

// edgeCaveat returns the honesty caveat for a graph's call edges, upgraded when a precision
// layer has proven some of them.
func edgeCaveat(g *providers.Graph) string {
	suffix := precisionNotes(g)
	capNotes := getLanguageCapabilityNotes(g)
	var capStr string
	if len(capNotes) > 0 {
		capStr = "\nCapabilities by stack:\n- " + strings.Join(capNotes, "\n- ")
	}

	if g.Precise {
		var parts []string
		if strings.Contains(g.Precision, "go") {
			parts = append(parts, "Go call edges are type-checked (proven, including interface dispatch)")
		}
		if strings.Contains(g.Precision, "rust-analyzer") {
			parts = append(parts, "Rust call edges were enriched through rust-analyzer call hierarchy")
		}
		if len(parts) == 0 {
			parts = append(parts, "Some call edges were enriched by a semantic backend")
		}
		return strings.Join(parts, "; ") + "; any unresolved edges remain syntactic (likely, not proven)." + suffix + capStr
	}
	return "Edges are syntactic (name + lexical scope), not type-checked: callers via dynamic dispatch or reflection may be missed, and same-named symbols may add noise. Treat as a strong lead, not a proof." + suffix + capStr
}

func getLanguageCapabilityNotes(g *providers.Graph) []string {
	var notes []string
	hasGo, hasRust, hasJS, hasSvelte, hasAngular := false, false, false, false, false
	for _, l := range g.Langs {
		switch strings.ToLower(l) {
		case "go":
			hasGo = true
		case "rust":
			hasRust = true
		case "javascript", "typescript", "tsx":
			hasJS = true
		case "svelte":
			hasSvelte = true
		}
	}
	for _, sym := range g.Defs {
		if strings.Contains(strings.ToLower(sym.File), ".component.ts") || strings.Contains(strings.ToLower(sym.File), ".service.ts") {
			hasAngular = true
			break
		}
	}

	if hasGo {
		if g.Precise && strings.Contains(g.Precision, "go") {
			notes = append(notes, "Go: Precise type-checked call graph enabled (proven edges resolved including interface dispatch).")
		} else {
			notes = append(notes, "Go: Call graph is syntactic-only. Interface dispatch and method callers may be missed; pass precise:true to enable type-checked resolution.")
		}
	}
	if hasRust {
		if g.Precise && strings.Contains(g.Precision, "rust-analyzer") {
			notes = append(notes, "Rust: Call graph enriched via rust-analyzer call hierarchy.")
		} else {
			notes = append(notes, "Rust: Call graph is syntactic-only; pass precise:true to enable rust-analyzer enrichment.")
		}
	}
	if hasJS {
		notes = append(notes, "JavaScript/TypeScript: Call graph is syntactic. Resolves ES imports, default/named exports, aliases, and JSX component usage.")
	}
	if hasSvelte {
		notes = append(notes, "Svelte/SvelteKit: Call graph is syntactic. Indexes <script> blocks, template expressions, and component tags; routes and endpoint lifecycles are recognized.")
	}
	if hasAngular {
		notes = append(notes, "Angular: Call graph is syntactic. Resolves component template event/property bindings, interpolations, and constructor dependency injection.")
	}
	return notes
}

func precisionNotes(g *providers.Graph) string {
	if g == nil || len(g.PrecisionNotes) == 0 {
		return ""
	}
	return " " + strings.Join(g.PrecisionNotes, " ")
}

// goPrecisionHint nudges a Go caller toward the type-checked path when the syntactic pass
// left calls unresolved and precision was not already requested. In Go, unresolved edges are
// overwhelmingly method and interface-dispatch calls (the syntactic resolver matches on name
// + scope and cannot see receiver types), which is exactly what EnrichGo resolves. Returns ""
// for non-Go graphs, an already-precise graph, a fully-resolved graph, or a precise request.
func goPrecisionHint(g *providers.Graph, requestedPrecise bool) string {
	if requestedPrecise || g.Precise || g.Unresolved == 0 {
		return ""
	}
	hasGo := false
	for _, l := range g.Langs {
		if strings.EqualFold(l, "go") {
			hasGo = true
			break
		}
	}
	if !hasGo {
		return ""
	}
	return fmt.Sprintf(" %d call(s) were left unresolved by the syntactic pass — in Go these are usually"+
		" method or interface-dispatch calls; pass precise:true (Go toolchain required) to resolve them"+
		" with type-checked edges.", g.Unresolved)
}

func semanticPrecisionUnavailableNote() string {
	return "Semantic enrichment was requested but no supported backend could run for this repo. "
}

// isTestFile reports whether a repo-relative path is a test file, by the conventions of the
// common ecosystems (Go, JS/TS, Python, Rust).
func isTestFile(path string) bool {
	base := filepath.Base(path)
	ext := strings.ToLower(filepath.Ext(path))
	if strings.HasSuffix(base, "_test.go") {
		return true
	}
	baseLower := strings.ToLower(base)
	if strings.Contains(baseLower, ".test.") ||
		strings.Contains(baseLower, ".spec.") ||
		strings.Contains(baseLower, ".cy.") ||
		strings.HasPrefix(baseLower, "test_") ||
		strings.HasSuffix(baseLower, "_test") ||
		strings.HasSuffix(baseLower, ".test"+ext) ||
		strings.HasSuffix(baseLower, ".spec"+ext) ||
		strings.HasSuffix(baseLower, ".cy"+ext) ||
		strings.HasSuffix(baseLower, ".tftest.hcl") ||
		strings.HasSuffix(baseLower, ".tofutest.hcl") {
		return true
	}
	slashed := "/" + filepath.ToSlash(strings.ToLower(path)) + "/"
	if strings.Contains(slashed, "/tests/") ||
		strings.Contains(slashed, "/__tests__/") ||
		strings.Contains(slashed, "/e2e/") ||
		strings.Contains(slashed, "/cypress/") ||
		strings.Contains(slashed, "/playwright/") {
		return true
	}
	if strings.HasSuffix(base, ".rs") {
		return strings.Contains(slashed, "/benches/")
	}
	return false
}

func isTestSymbol(sym *providers.Symbol) bool {
	if sym == nil {
		return false
	}
	return sym.Test || isTestFile(sym.File)
}

func isTestQName(q string, g *providers.Graph) bool {
	if isTestFile(qnameFile(q)) {
		return true
	}
	if g != nil {
		if sym := g.Defs[q]; isTestSymbol(sym) {
			return true
		}
	}
	return false
}

func qnameFile(q string) string {
	if idx := strings.Index(q, "::"); idx >= 0 {
		return q[:idx]
	}
	return q
}
