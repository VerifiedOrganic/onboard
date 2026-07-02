// Package providers defines the code graph model (symbols, edges, PageRank) and
// language-specific taggers. Syntactic indexing lives in [indexer]; semantic
// enrichment lives in [precision].
package providers

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
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
	// vs. re-parsed. Never serialized over MCP; observed only in tests.
	Reused, Retagged int `json:"-"`
}

// EdgeKey identifies a directed caller->callee edge in ProvenEdges.
func EdgeKey(caller, callee string) string { return caller + "\x00" + callee }

func edgeKey(caller, callee string) string { return EdgeKey(caller, callee) }

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
	slices.SortFunc(exact, func(a, b *Symbol) int {
		return cmp.Compare(a.QName, b.QName)
	})
	slices.SortFunc(sub, func(a, b *Symbol) int {
		return cmp.Compare(a.QName, b.QName)
	})
	return append(exact, sub...)
}

// PageRank-based ranking (PageRank, expandSeeds) lives in pagerank.go.

// GraphHasLang reports whether g lists lang among its indexed languages.
func GraphHasLang(g *Graph, lang string) bool {
	for _, l := range g.Langs {
		if strings.EqualFold(l, lang) {
			return true
		}
	}
	return false
}

// PosKey builds a lookup key for (file, line, name) symbol resolution.
func PosKey(slashFile string, line int, name string) string {
	return slashFile + "\x00" + strconv.Itoa(line) + "\x00" + name
}

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

// SortedKeys returns the keys of m in sorted order.
func SortedKeys(m map[string]bool) []string {
	return sortedKeys(m)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// UniqueQName returns a file-unique qualified name for a symbol definition.
func UniqueQName(defs map[string]*Symbol, rel, name string, line int) string {
	return uniqueQName(defs, rel, name, line)
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

// GraphEdgeSet tracks directed edges while deduplicating additions.
type GraphEdgeSet struct {
	forward map[string]map[string]bool
	reverse map[string]map[string]bool
}

// NewGraphEdgeSet returns an empty edge set.
func NewGraphEdgeSet() *GraphEdgeSet {
	return newGraphEdgeSet()
}

func newGraphEdgeSet() *GraphEdgeSet {
	return &GraphEdgeSet{
		forward: map[string]map[string]bool{},
		reverse: map[string]map[string]bool{},
	}
}

// EdgeSetFromGraph seeds a set from an existing graph's forward edges.
func EdgeSetFromGraph(g *Graph) *GraphEdgeSet {
	return edgeSetFromGraph(g)
}

func edgeSetFromGraph(g *Graph) *GraphEdgeSet {
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

// Add records caller->callee on g when not already present.
func (s *GraphEdgeSet) Add(g *Graph, caller, callee string) bool {
	return s.add(g, caller, callee)
}

func (s *GraphEdgeSet) add(g *Graph, caller, callee string) bool {
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

func (s *GraphEdgeSet) has(caller, callee string) bool {
	return s.forward[caller] != nil && s.forward[caller][callee]
}

func (s *GraphEdgeSet) mark(caller, callee string) {
	if s.forward[caller] == nil {
		s.forward[caller] = map[string]bool{}
	}
	if s.reverse[callee] == nil {
		s.reverse[callee] = map[string]bool{}
	}
	s.forward[caller][callee] = true
	s.reverse[callee][caller] = true
}

// NormalizeRoot resolves root to an absolute path.
func NormalizeRoot(root string) (string, error) {
	return normalizeRoot(root)
}

func normalizeRoot(root string) (string, error) {
	if root == "" {
		root = "."
	}
	return pathutil.ResolveRoot(root)
}

// SkipDir reports whether a directory name should be excluded from indexing.
func SkipDir(name string) bool {
	return skipDir(name)
}

func skipDir(name string) bool {
	return ignore.Dir(name) || strings.HasPrefix(name, ".")
}
