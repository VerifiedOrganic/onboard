package server

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/VerifiedOrganic/onboard/internal/pathutil"
	"github.com/VerifiedOrganic/onboard/internal/providers"
)

// context_pack assembles a ranked, token-budgeted bundle of source snippets most relevant
// to a seed symbol or file — retrieval-augmented context WITHOUT an embedding model. It is
// the offline, pure-Go answer to "given this, hand me the code I need to understand it":
// relevance is call-graph proximity to the seed, refined by centrality and git churn.
//
// The three relevance axes, all already computed elsewhere:
//   - call-distance from the seed (BFS over callers AND callees) — the dominant signal,
//   - PageRank centrality — importance, to prefer the load-bearing neighbor, and
//   - git churn — recency/volatility, to prefer the code that actually keeps changing.

const (
	defaultPackTokens   = 4000 // snippets are bigger than repo_map's outline, so a larger default
	defaultPackDistance = 2    // seed, its neighbors, and their neighbors
	maxPackNodes        = 200  // bound the neighborhood BFS on hub-heavy graphs
	maxSnippetLines     = 40   // hard cap per snippet before the next-definition boundary
	packDecay           = 0.5  // each call-graph hop halves a symbol's relevance
	packCentralityW     = 1.0  // weight of normalized centrality in the per-node boost
	packChurnW          = 0.5  // weight of normalized churn (secondary to centrality)
)

type contextPackInput struct {
	Root        string `json:"root,omitempty" jsonschema:"repo root; defaults to the working directory"`
	Seed        string `json:"seed" jsonschema:"what to gather context around: a symbol name, a repo-relative file path, or a file::name qualified name"`
	MaxTokens   int    `json:"max_tokens,omitempty" jsonschema:"approximate token budget for the bundled snippets (default 4000)"`
	MaxDistance int    `json:"max_distance,omitempty" jsonschema:"how many call-graph hops out from the seed to gather (default 2)"`
	Precise     bool   `json:"precise,omitempty" jsonschema:"for Go modules, enrich with type-checked edges; for Rust Cargo projects, enrich with rust-analyzer call hierarchy when available; slower, requires the language toolchain"`
	Refresh     bool   `json:"refresh,omitempty" jsonschema:"re-index the repo instead of using the cached graph"`
}

type contextItem struct {
	QName    string  `json:"qname"`
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
	File     string  `json:"file"`
	Line     int     `json:"line"`
	EndLine  int     `json:"end_line"`
	Distance int     `json:"distance"` // call-graph hops from the nearest seed (0 = a seed)
	Callers  int     `json:"callers"`
	Churn    int     `json:"churn"`
	Score    float64 `json:"score"`
	Snippet  string  `json:"snippet"`
}

type contextPackOutput struct {
	Seed            string        `json:"seed"`
	Matched         []string      `json:"matched,omitempty"` // resolved seed QNames
	Pack            string        `json:"pack"`              // rendered bundle, ready to paste into context
	Items           []contextItem `json:"items"`
	TotalCandidates int           `json:"total_candidates"` // definitions in the neighborhood before the budget cut
	Included        int           `json:"included"`
	Provider        string        `json:"provider"`
	Truncated       bool          `json:"truncated"`
	Note            string        `json:"note,omitempty"`
}

func contextPack(ctx context.Context, in contextPackInput) (contextPackOutput, error) {
	out := contextPackOutput{Seed: in.Seed}
	if strings.TrimSpace(in.Seed) == "" {
		out.Note = "Provide a seed: a symbol name, a repo-relative file path, or a file::name qualified name."
		return out, nil
	}
	maxTokens := in.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultPackTokens
	}
	maxDist := in.MaxDistance
	if maxDist <= 0 {
		maxDist = defaultPackDistance
	}
	root, err := resolveRoot(in.Root)
	if err != nil {
		return out, err
	}
	g, err := indexGraph(ctx, root, in.Refresh, in.Precise)
	if err != nil {
		return out, err
	}
	out.Provider = g.Provider

	seeds := resolveSeeds(g, in.Seed)
	if len(seeds) == 0 {
		out.Note = "No symbol or file matched the seed. Provide a symbol name, a repo-relative file path, or a file::name qualified name."
		return out, nil
	}
	seedQNames := make([]string, 0, len(seeds))
	for _, s := range seeds {
		seedQNames = append(seedQNames, s.QName)
		out.Matched = append(out.Matched, s.QName)
	}

	dist := neighborhood(g, seedQNames, maxDist)

	pr := g.PageRank(nil)
	var maxPR float64
	for _, v := range pr {
		if v > maxPR {
			maxPR = v
		}
	}
	churn := fileChurn(root, in.Refresh)
	var maxLogChurn float64
	for _, c := range churn {
		if l := math.Log1p(float64(c)); l > maxLogChurn {
			maxLogChurn = l
		}
	}

	// Score every reachable node that is an actual definition (we can only snippet defs).
	items := make([]contextItem, 0, len(dist))
	for q, d := range dist {
		sym := g.Defs[q]
		if sym == nil {
			continue
		}
		commits := churn[filepath.ToSlash(sym.File)]
		items = append(items, contextItem{
			QName:    q,
			Name:     sym.Name,
			Kind:     sym.Kind,
			File:     sym.File,
			Line:     sym.Line,
			Distance: d,
			Callers:  len(g.Callers(q)),
			Churn:    commits,
			Score:    packScore(d, pr[q], maxPR, commits, maxLogChurn),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		if items[i].Distance != items[j].Distance {
			return items[i].Distance < items[j].Distance
		}
		return items[i].QName < items[j].QName
	})
	out.TotalCandidates = len(items)

	// Greedily attach snippets in relevance order until the budget is spent, always keeping
	// at least the top item so a tiny budget still returns the seed's own code.
	defLines := defLinesByFile(g)
	lineCache := map[string][]string{}
	var b strings.Builder
	tokens := 0
	for i := range items {
		it := &items[i]
		snippet, end := extractSnippet(root, it.File, it.Line, defLines[it.File], lineCache)
		it.Snippet, it.EndLine = snippet, end
		block := renderPackBlock(*it)
		cost := estTokens(block)
		if len(out.Items) > 0 && tokens+cost > maxTokens {
			out.Truncated = true
			break
		}
		tokens += cost
		b.WriteString(block)
		out.Items = append(out.Items, *it)
	}
	out.Included = len(out.Items)
	out.Pack = b.String()

	switch {
	case g.Provider == "null":
		out.Note = "Definitions-only provider: no call graph, so the pack contains only the seed's own definitions (no neighbors). Snippets are heuristic windows bounded by the next definition."
	case g.Precise:
		out.Note = "Relevance = call-graph proximity to the seed, refined by centrality and git churn. " + edgeCaveat(g) + " Snippets are heuristic windows bounded by the next definition, so a body may over- or under-shoot."
	case in.Precise:
		out.Note = "Relevance = call-graph proximity to the seed, refined by centrality and git churn. " + semanticPrecisionUnavailableNote() + edgeCaveat(g) + " Snippets are heuristic windows bounded by the next definition, so a body may over- or under-shoot."
	default:
		out.Note = "Relevance = call-graph proximity to the seed, refined by centrality and git churn. Edges are syntactic (likely, not proven); snippets are heuristic windows bounded by the next definition, so a body may over- or under-shoot."
	}
	return out, nil
}

// resolveSeeds turns a seed string into the set of definitions it names. An exact name or
// QName match wins outright; otherwise the seed is treated as a path/substring, so a file
// path expands to every symbol defined in that file.
func resolveSeeds(g *providers.Graph, seed string) []*providers.Symbol {
	var exact []*providers.Symbol
	for _, s := range g.Defs {
		if s == nil {
			continue
		}
		if s.Name == seed || s.QName == seed {
			exact = append(exact, s)
		}
	}
	if len(exact) > 0 {
		sortSyms(exact)
		return exact
	}
	slashed := filepath.ToSlash(seed)
	var sub []*providers.Symbol
	for _, s := range g.Defs {
		if s == nil {
			continue
		}
		if strings.Contains(filepath.ToSlash(s.File), slashed) || strings.Contains(s.QName, seed) {
			sub = append(sub, s)
		}
	}
	sortSyms(sub)
	return sub
}

func sortSyms(s []*providers.Symbol) {
	sort.Slice(s, func(i, j int) bool { return s[i].QName < s[j].QName })
}

// neighborhood does a breadth-first walk outward from the seeds over BOTH directions
// (callees = what the seed depends on, callers = who depends on the seed), recording each
// node's minimum hop distance. Distance is order-independent (BFS gives min hops), so the
// result is deterministic; the node cap bounds blow-up on hub-heavy graphs.
func neighborhood(g *providers.Graph, seeds []string, maxDist int) map[string]int {
	dist := map[string]int{}
	type item struct {
		q string
		d int
	}
	var queue []item
	for _, s := range seeds {
		if _, ok := dist[s]; !ok {
			dist[s] = 0
			queue = append(queue, item{s, 0})
		}
	}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		if it.d >= maxDist {
			continue
		}
		neighbors := append(g.Callees(it.q), g.Callers(it.q)...)
		for _, nb := range neighbors {
			if _, ok := dist[nb]; !ok {
				dist[nb] = it.d + 1
				queue = append(queue, item{nb, it.d + 1})
			}
		}
		if len(dist) >= maxPackNodes {
			break
		}
	}
	return dist
}

func packScore(d int, pr, maxPR float64, commits int, maxLogChurn float64) float64 {
	decay := math.Pow(packDecay, float64(d))
	var cen, ch float64
	if maxPR > 0 {
		cen = pr / maxPR
	}
	if maxLogChurn > 0 {
		ch = math.Log1p(float64(commits)) / maxLogChurn
	}
	return decay * (1 + packCentralityW*cen + packChurnW*ch)
}

// defLinesByFile maps each file to its definition start-lines, sorted ascending — the input
// to the snippet boundary heuristic.
func defLinesByFile(g *providers.Graph) map[string][]int {
	m := map[string][]int{}
	for _, s := range g.Defs {
		if s == nil {
			continue
		}
		m[s.File] = append(m[s.File], s.Line)
	}
	for f := range m {
		sort.Ints(m[f])
	}
	return m
}

// extractSnippet returns the source for a definition starting at startLine (1-based) and the
// last line it included. Because the syntactic graph records only a start line, the end is a
// heuristic: the smaller of startLine+maxSnippetLines and the line just before the next
// definition in the same file (so a snippet never bleeds into the following symbol). Missing
// or unreadable files degrade to an empty snippet rather than an error.
func extractSnippet(root, file string, startLine int, fileDefLines []int, cache map[string][]string) (string, int) {
	lines, ok := cache[file]
	if !ok {
		path, err := pathutil.JoinUnderRoot(root, file)
		if err != nil {
			cache[file] = nil
			return "", startLine
		}
		data, err := os.ReadFile(path)
		if err != nil {
			cache[file] = nil
			return "", startLine
		}
		lines = strings.Split(string(data), "\n")
		cache[file] = lines
	}
	if startLine < 1 || startLine > len(lines) {
		return "", startLine
	}
	end := startLine + maxSnippetLines - 1
	if next := nextDefLine(fileDefLines, startLine); next > startLine && next-1 < end {
		end = next - 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if end < startLine {
		end = startLine
	}
	return strings.Join(lines[startLine-1:end], "\n"), end
}

// nextDefLine returns the smallest definition line strictly greater than line, or 0 if none.
func nextDefLine(sorted []int, line int) int {
	i := sort.SearchInts(sorted, line+1)
	if i < len(sorted) {
		return sorted[i]
	}
	return 0
}

func renderPackBlock(it contextItem) string {
	tags := []string{fmt.Sprintf("distance %d", it.Distance)}
	if it.Callers > 0 {
		tags = append(tags, fmt.Sprintf("callers %d", it.Callers))
	}
	if it.Churn > 0 {
		tags = append(tags, fmt.Sprintf("churn %d", it.Churn))
	}
	header := fmt.Sprintf("// %s:%d  %s  (%s)\n", it.File, it.Line, it.QName, strings.Join(tags, ", "))
	return header + it.Snippet + "\n\n"
}

func registerContextPackTool(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "context_pack",
		Description: "Assemble a ranked, token-budgeted bundle of the source most relevant to a seed symbol or file — retrieval-augmented context with no embedding model. Relevance is call-graph proximity to the seed (callers and callees), refined by centrality and git churn. Use to load 'everything I need to understand or change X' in one shot.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in contextPackInput) (*mcp.CallToolResult, contextPackOutput, error) {
		out, err := contextPack(ctx, in)
		return nil, out, err
	})
}
