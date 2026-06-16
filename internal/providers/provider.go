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
	"sort"
	"strings"

	"github.com/VerifiedOrganic/onboard/internal/ignore"
	"github.com/VerifiedOrganic/onboard/internal/pathutil"
)

// Symbol is a definition discovered in the source.
type Symbol struct {
	QName string `json:"qname"` // file-relative qualified name, e.g. "internal/x/y.go::Foo"
	Name  string `json:"name"`
	Kind  string `json:"kind"` // function, method, class, ...
	File  string `json:"file"` // repo-relative path
	Line  int    `json:"line"` // 1-based
	// Column is the zero-based byte column of the name identifier. It is omitted when a
	// provider cannot recover one, but tree-sitter-backed symbols set it so precision
	// layers can address LSP positions.
	Column int    `json:"column,omitempty"`
	Lang   string `json:"lang"`
	// Recv is the receiver/owner type for a method or associated item, e.g.
	// "HTMLRenderer" for func (h *HTMLRenderer) Render() or "Engine" for
	// impl Engine { fn run(...) }. Empty for unowned functions. Name stays the BARE
	// identifier (so it agrees with semantic backends); Recv is additive metadata used only
	// to qualify display output.
	Recv string `json:"recv,omitempty"`
	// Test marks test entry points even when the file path does not identify them, notably
	// Rust unit tests living inside src/*.rs behind #[test] / #[cfg(test)].
	Test bool `json:"test,omitempty"`
	// Public marks language-level public/exported symbols when capitalization is not the
	// export convention, notably Rust `pub fn` and `pub` associated functions.
	Public bool `json:"public,omitempty"`
}

// Display returns the human-facing name: a method is qualified by its receiver type
// (HTMLRenderer.Render) so same-named methods on different types are legible, while a
// plain function returns its bare name.
func (s *Symbol) Display() string {
	if s == nil {
		return ""
	}
	if s.Recv != "" {
		if strings.EqualFold(s.Lang, "rust") {
			return s.Recv + "::" + s.Name
		}
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
	Precision   string          `json:"precision,omitempty"` // comma-separated semantic backends, e.g. go,rust-analyzer
	ProvenEdges map[string]bool `json:"-"`
	// PrecisionNotes records semantic-backend degradation details: unavailable tools,
	// timeouts, zero returned edges, or capped enrichment. Tools append these to honesty
	// notes so precise:true failures are visible to users.
	PrecisionNotes []string `json:"precision_notes,omitempty"`

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

// MarkPrecision records that a semantic backend enriched the graph. It is idempotent and
// keeps a compact comma-separated list so API output can explain which language layer ran.
func (g *Graph) MarkPrecision(kind string) {
	if kind == "" {
		return
	}
	g.Precise = true
	for _, existing := range strings.Split(g.Precision, ",") {
		if existing == kind {
			return
		}
	}
	if g.Precision == "" {
		g.Precision = kind
		return
	}
	g.Precision += "," + kind
}

// AddPrecisionNote records a unique user-facing note about semantic precision degradation.
func (g *Graph) AddPrecisionNote(note string) {
	note = strings.TrimSpace(note)
	if note == "" {
		return
	}
	for _, existing := range g.PrecisionNotes {
		if existing == note {
			return
		}
	}
	g.PrecisionNotes = append(g.PrecisionNotes, note)
}

// Provider indexes a repository into a Graph.
type Provider interface {
	Name() string
	Index(ctx context.Context, root string) (*Graph, error)
}

// Indexer indexes a repository, optionally using a persistent per-file cache at cachePath.
type Indexer interface {
	IndexWithCache(ctx context.Context, root, cachePath string) (*Graph, error)
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

// FindSymbols returns definitions matching query by exact name, exact QName, exact
// Display name (receiver-qualified, e.g. "Engine::new"), or QName/Display substring
// (in that order of preference, de-duplicated).
func (g *Graph) FindSymbols(query string) []*Symbol {
	var exact, sub []*Symbol
	seen := map[string]bool{}
	for _, s := range g.Defs {
		if s == nil {
			continue
		}
		switch {
		case s.Name == query || s.QName == query || s.Display() == query:
			exact = append(exact, s)
			seen[s.QName] = true
		case strings.Contains(s.QName, query) || (s.Recv != "" && strings.Contains(s.Display(), query)):
			if !seen[s.QName] {
				sub = append(sub, s)
				seen[s.QName] = true
			}
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

type graphEdgeSet struct {
	forward map[string]map[string]bool
	reverse map[string]map[string]bool
}

func newGraphEdgeSet() *graphEdgeSet {
	return &graphEdgeSet{
		forward: map[string]map[string]bool{},
		reverse: map[string]map[string]bool{},
	}
}

func edgeSetFromGraph(g *Graph) *graphEdgeSet {
	set := newGraphEdgeSet()
	if g == nil {
		return set
	}
	for caller, callees := range g.Forward {
		for _, callee := range callees {
			set.mark(caller, callee)
		}
	}
	return set
}

func (s *graphEdgeSet) add(g *Graph, caller, callee string) bool {
	if caller == "" || callee == "" || caller == callee {
		return false
	}
	if s.has(caller, callee) {
		return false
	}
	s.mark(caller, callee)
	g.Forward[caller] = append(g.Forward[caller], callee)
	g.Reverse[callee] = append(g.Reverse[callee], caller)
	return true
}

func (s *graphEdgeSet) has(caller, callee string) bool {
	return s.forward[caller] != nil && s.forward[caller][callee]
}

func (s *graphEdgeSet) mark(caller, callee string) {
	if s.forward[caller] == nil {
		s.forward[caller] = map[string]bool{}
	}
	if s.reverse[callee] == nil {
		s.reverse[callee] = map[string]bool{}
	}
	s.forward[caller][callee] = true
	s.reverse[callee][caller] = true
}

func normalizeRoot(root string) (string, error) {
	if root == "" {
		root = "."
	}
	return pathutil.ResolveRoot(root)
}

func skipDir(name string) bool {
	return ignore.Dir(name) || strings.HasPrefix(name, ".")
}
