package server

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/VerifiedOrganic/onboard/internal/providers"
)

// Shared graph honesty helpers used by every graph-backed tool (trace_flow, impact,
// repo_map, context_pack, render_map). Indexing lives in internal/graph.

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
		if sym == nil {
			continue
		}
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
