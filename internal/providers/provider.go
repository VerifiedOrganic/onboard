// Package providers builds a code graph (symbols + call edges) for a repository.
//
// Two implementations sit behind one interface:
//
//   - Builtin: a pure-Go tree-sitter engine (gotreesitter) that extracts symbol
//     definitions and call references for ~200 languages, then links them by
//     name + lexical scope. Tags are SYNTACTIC, so the resulting call graph is a
//     name-resolution heuristic, not a type-checked one — same-named symbols
//     across scopes, dynamic dispatch, and higher-order calls are its accuracy
//     ceiling. Callers should present results as "likely", not "proven".
//   - Null: a regex fallback that lists definitions only (no call edges). Used
//     when the Builtin engine cannot index a tree at all.
package providers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/VerifiedOrganic/onboard/internal/ignore"
)

// Symbol is a definition discovered in the source.
type Symbol struct {
	QName string `json:"qname"` // file-relative qualified name, e.g. "internal/x/y.go::Foo"
	Name  string `json:"name"`
	Kind  string `json:"kind"` // function, method, class, ...
	File  string `json:"file"` // repo-relative path
	Line  int    `json:"line"` // 1-based
	Lang  string `json:"lang"`
	// Recv is the receiver type for a method, e.g. "HTMLRenderer" for
	// func (h *HTMLRenderer) Render(). Empty for non-methods. Name stays the BARE
	// identifier (so it agrees with the type checker's fn.Name() during Go precise
	// enrichment); Recv is additive metadata used only to qualify display output.
	Recv string `json:"recv,omitempty"`
}

// Display returns the human-facing name: a method is qualified by its receiver type
// (HTMLRenderer.Render) so same-named methods on different types are legible, while a
// plain function returns its bare name.
func (s *Symbol) Display() string {
	if s.Recv != "" {
		return s.Recv + "." + s.Name
	}
	return s.Name
}

// Graph is the indexed code graph for a repository.
type Graph struct {
	Provider   string              `json:"provider"`
	Defs       map[string]*Symbol  `json:"defs"`
	Forward    map[string][]string `json:"-"` // caller QName -> callee QNames
	Reverse    map[string][]string `json:"-"` // callee QName -> caller QNames
	Files      int                 `json:"files"`
	Langs      []string            `json:"langs"`
	Unresolved int                 `json:"unresolved_calls"` // refs we could not link
	Note       string              `json:"note,omitempty"`

	// Precise is true when a precision layer (e.g. the Go type-checked callgraph) enriched
	// this graph. ProvenEdges holds the specific caller->callee edges that layer confirmed,
	// keyed by edgeKey — those edges are proven, not merely the syntactic "likely" the base
	// graph provides. ProvenEdges is never serialized; it backs the honesty note only.
	Precise     bool            `json:"precise,omitempty"`
	ProvenEdges map[string]bool `json:"-"`

	// Incremental-indexing stats: how many files were reused from the on-disk cache
	// vs. re-parsed. Unexported, so never serialized over MCP; observed only in tests.
	reused, retagged int
}

// edgeKey identifies a directed caller->callee edge in ProvenEdges.
func edgeKey(caller, callee string) string { return caller + "\x00" + callee }

// IsProven reports whether the caller->callee edge was confirmed by a precision layer
// (type-checked), as opposed to inferred syntactically.
func (g *Graph) IsProven(caller, callee string) bool {
	return g.ProvenEdges[edgeKey(caller, callee)]
}

// Provider indexes a repository into a Graph.
type Provider interface {
	Name() string
	Index(ctx context.Context, root string) (*Graph, error)
}

// Callees returns the direct callees of qname.
func (g *Graph) Callees(qname string) []string { return dedupeSort(g.Forward[qname]) }

// Callers returns the direct callers of qname.
func (g *Graph) Callers(qname string) []string { return dedupeSort(g.Reverse[qname]) }

// Impact returns every transitive caller of qname — the blast radius if it changes.
func (g *Graph) Impact(qname string) []string {
	seen := map[string]bool{}
	stack := append([]string{}, g.Reverse[qname]...)
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[n] {
			continue
		}
		seen[n] = true
		stack = append(stack, g.Reverse[n]...)
	}
	return sortedKeys(seen)
}

// FindSymbols returns definitions matching query by exact name, exact QName, or
// QName substring (in that order of preference, de-duplicated).
func (g *Graph) FindSymbols(query string) []*Symbol {
	var exact, sub []*Symbol
	for _, s := range g.Defs {
		switch {
		case s.Name == query || s.QName == query:
			exact = append(exact, s)
		case strings.Contains(s.QName, query):
			sub = append(sub, s)
		}
	}
	out := append(exact, sub...)
	sort.Slice(out, func(i, j int) bool { return out[i].QName < out[j].QName })
	return out
}

// PageRank-based ranking (PageRank, expandSeeds) lives in pagerank.go.

func dedupeSort(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	for _, s := range in {
		seen[s] = true
	}
	return sortedKeys(seen)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func uniqueQName(defs map[string]*Symbol, rel, name string, line int) string {
	qn := rel + "::" + name
	if _, exists := defs[qn]; !exists {
		return qn
	}
	base := fmt.Sprintf("%s::%s#%d", rel, name, line)
	qn = base
	for n := 2; ; n++ {
		if _, exists := defs[qn]; !exists {
			return qn
		}
		qn = fmt.Sprintf("%s.%d", base, n)
	}
}

func normalizeRoot(root string) (string, error) {
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("root %q is not a directory", abs)
	}
	return abs, nil
}

func skipDir(name string) bool {
	return ignore.Dir(name) || strings.HasPrefix(name, ".")
}
