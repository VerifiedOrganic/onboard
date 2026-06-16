package server

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/VerifiedOrganic/onboard/internal/git"
)

// repo_map ranks the codebase by call-graph centrality (PageRank) and returns a
// compact, token-budgeted outline of the most important symbols — the orientation
// view an agent should load first. Inspired by aider's repo map.
//
// When the repo is a git work tree, the ranking is fused with git churn (commit
// count per file): centrality answers "what does everything depend on?", churn
// answers "what keeps changing?", and the symbols that score high on both — load-
// bearing AND volatile — are the prime onboarding targets. See blendScore.

const (
	// defaultChurnWeight is churn's share of the blended ranking score when git history
	// is available and the caller did not specify a weight. Centrality stays dominant.
	defaultChurnWeight = 0.3
	// churnScanCommits bounds how much history the fusion reads (matches the history tool
	// default), so the blend stays cheap on repos with very deep histories.
	churnScanCommits = 1000
)

type repoMapInput struct {
	Root        string   `json:"root,omitempty" jsonschema:"repo root; defaults to the working directory"`
	Focus       []string `json:"focus,omitempty" jsonschema:"optional symbols, repo-relative files, or qualified names to bias the ranking toward (personalized PageRank)"`
	MaxTokens   int      `json:"max_tokens,omitempty" jsonschema:"approximate token budget for the rendered map (default 1000)"`
	ChurnWeight *float64 `json:"churn_weight,omitempty" jsonschema:"how much git churn influences the ranking, 0..1 (default 0.3); 0 ranks by call-graph centrality alone; ignored outside a git repo"`
	Precise     bool     `json:"precise,omitempty" jsonschema:"for Go modules, enrich with type-checked edges; for Rust Cargo projects, enrich with rust-analyzer call hierarchy when available; slower, requires the language toolchain"`
	Refresh     bool     `json:"refresh,omitempty" jsonschema:"re-index the repo instead of using the cached graph"`
}

type rankedSymbol struct {
	QName   string  `json:"qname"`
	Name    string  `json:"name"`
	Kind    string  `json:"kind"`
	File    string  `json:"file"`
	Line    int     `json:"line"`
	Callers int     `json:"callers"`
	Churn   int     `json:"churn"` // commits touching this symbol's file (0 if no git history)
	Score   float64 `json:"score"`
}

type repoMapOutput struct {
	Map          string         `json:"map"`
	Symbols      []rankedSymbol `json:"symbols"`
	TotalSymbols int            `json:"total_symbols"`
	Included     int            `json:"included"`
	Provider     string         `json:"provider"`
	Truncated    bool           `json:"truncated"`
	Note         string         `json:"note,omitempty"`
}

func repoMap(ctx context.Context, in repoMapInput) (repoMapOutput, error) {
	out := repoMapOutput{}
	maxTokens := in.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1000
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
	out.TotalSymbols = len(g.Defs)
	if len(g.Defs) == 0 {
		out.Note = "No symbols found to rank."
		return out, nil
	}

	pr := g.PageRank(in.Focus)

	// Resolve the churn weight, then load per-file churn only when it can matter: the
	// weight is positive and the root is a git work tree. A failed/empty history simply
	// leaves the map empty, so the blend degrades to pure centrality (see blendScore).
	weight := defaultChurnWeight
	if in.ChurnWeight != nil {
		weight = clamp01(*in.ChurnWeight)
	}
	churn := map[string]int{}
	if weight > 0 {
		churn = fileChurn(root, in.Refresh)
	}
	blended := weight > 0 && len(churn) > 0

	var maxPR, maxLogChurn float64
	for _, v := range pr {
		if v > maxPR {
			maxPR = v
		}
	}
	for _, c := range churn {
		if l := math.Log1p(float64(c)); l > maxLogChurn {
			maxLogChurn = l
		}
	}

	ranked := make([]rankedSymbol, 0, len(g.Defs))
	for q, sym := range g.Defs {
		commits := churn[filepath.ToSlash(sym.File)]
		ranked = append(ranked, rankedSymbol{
			QName:   q,
			Name:    sym.Name,
			Kind:    sym.Kind,
			File:    sym.File,
			Line:    sym.Line,
			Callers: len(g.Callers(q)),
			Churn:   commits,
			Score:   blendScore(pr[q], maxPR, commits, maxLogChurn, weight, blended),
		})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Score != ranked[j].Score {
			return ranked[i].Score > ranked[j].Score
		}
		if ranked[i].Callers != ranked[j].Callers {
			return ranked[i].Callers > ranked[j].Callers
		}
		return ranked[i].QName < ranked[j].QName
	})

	// Greedily include symbols in rank order until the token budget is exhausted,
	// charging a one-time cost for each new file header. Always keep at least one.
	seenFiles := map[string]bool{}
	tokens := 0
	for _, rs := range ranked {
		cost := estTokens(symbolLine(rs))
		if !seenFiles[rs.File] {
			cost += estTokens(rs.File + "\n")
		}
		if len(out.Symbols) > 0 && tokens+cost > maxTokens {
			out.Truncated = true
			break
		}
		tokens += cost
		seenFiles[rs.File] = true
		out.Symbols = append(out.Symbols, rs)
	}
	out.Included = len(out.Symbols)
	out.Map = renderRepoMap(out.Symbols)

	switch {
	case g.Provider == "null":
		out.Note = "Definitions-only provider: symbols are listed but not ranked by call importance (no call graph)."
	case blended:
		out.Note = fmt.Sprintf("Ranked by call-graph centrality (PageRank) blended with git churn (churn weight %.2f) — code that is both load-bearing and changes often rises first. Syntactic edges: a strong orientation signal, not a proof.", weight)
	default:
		out.Note = "Ranked by call-graph centrality (PageRank) over syntactic edges — a strong orientation signal, not a proof of importance. No git churn applied."
	}
	if g.Precise {
		out.Note += " " + edgeCaveat(g)
	} else if in.Precise {
		out.Note += " " + semanticPrecisionUnavailableNote() + edgeCaveat(g)
	}
	return out, nil
}

// renderRepoMap groups the selected symbols by file (files in descending rank order
// of their best symbol, i.e. order of first appearance) and lists each file's symbols
// by line.
func renderRepoMap(syms []rankedSymbol) string {
	var order []string
	groups := map[string][]rankedSymbol{}
	for _, s := range syms {
		if _, ok := groups[s.File]; !ok {
			order = append(order, s.File)
		}
		groups[s.File] = append(groups[s.File], s)
	}
	var b strings.Builder
	for _, f := range order {
		grp := groups[f]
		sort.Slice(grp, func(i, j int) bool { return grp[i].Line < grp[j].Line })
		b.WriteString(f)
		b.WriteByte('\n')
		for _, s := range grp {
			b.WriteString(symbolLine(s))
		}
	}
	return b.String()
}

func symbolLine(s rankedSymbol) string {
	kind := s.Kind
	if kind == "" {
		kind = "def"
	}
	var tags []string
	if s.Callers > 0 {
		tags = append(tags, fmt.Sprintf("callers: %d", s.Callers))
	}
	if s.Churn > 0 {
		tags = append(tags, fmt.Sprintf("churn: %d", s.Churn))
	}
	suffix := ""
	if len(tags) > 0 {
		suffix = "  (" + strings.Join(tags, ", ") + ")"
	}
	return fmt.Sprintf("  :%-5d %-8s %s%s\n", s.Line, kind, s.Name, suffix)
}

// blendScore fuses call-graph centrality with git churn into one ranking score in [0,1].
// Each signal is normalized independently: centrality by the max PageRank score, churn by
// the max log-commit-count (commit counts are heavy-tailed, so a linear scale would let one
// runaway file flatten every other signal — log compresses that tail). The blend is
// (1-w)*centrality + w*churn.
//
// When churn was not blended (no git history, or weight 0) the result is the pure
// normalized centrality, whose ordering is identical to raw PageRank — which is exactly why
// a non-git repo ranks as if this fusion did not exist.
func blendScore(pr, maxPR float64, commits int, maxLogChurn, weight float64, blended bool) float64 {
	var centrality float64
	if maxPR > 0 {
		centrality = pr / maxPR
	}
	if !blended {
		return centrality
	}
	var churn float64
	if maxLogChurn > 0 {
		churn = math.Log1p(float64(commits)) / maxLogChurn
	}
	return (1-weight)*centrality + weight*churn
}

const churnCacheTTL = 5 * time.Minute

type churnCacheEntry struct {
	expires time.Time
	values  map[string]int
}

var churnCache = struct {
	sync.Mutex
	entries map[string]churnCacheEntry
}{entries: map[string]churnCacheEntry{}}

// fileChurn returns per-file commit counts keyed by slash-normalized repo-relative path,
// or an empty map outside a git work tree (so callers degrade cleanly to no churn signal).
// Shared by repo_map's ranking blend and context_pack's relevance scoring.
func fileChurn(root string, refresh bool) map[string]int {
	return fileChurnWithMax(root, churnScanCommits, refresh)
}

func fileChurnWithMax(root string, maxCommits int, refresh bool) map[string]int {
	key := fmt.Sprintf("%s\x00%d", root, maxCommits)
	now := time.Now()
	if !refresh {
		churnCache.Lock()
		if ent, ok := churnCache.entries[key]; ok && now.Before(ent.expires) {
			out := copyIntMap(ent.values)
			churnCache.Unlock()
			return out
		}
		churnCache.Unlock()
	}

	churn := map[string]int{}
	if git.Available(root) {
		if hist, err := git.History(root, maxCommits); err == nil {
			for _, fs := range hist {
				churn[filepath.ToSlash(fs.Path)] = fs.Commits
			}
		}
	}

	churnCache.Lock()
	churnCache.entries[key] = churnCacheEntry{expires: now.Add(churnCacheTTL), values: copyIntMap(churn)}
	churnCache.Unlock()
	return churn
}

func copyIntMap(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func clamp01(f float64) float64 {
	switch {
	case f < 0:
		return 0
	case f > 1:
		return 1
	default:
		return f
	}
}

// estTokens is a cheap ~4-chars-per-token estimate; exactness is not required for a
// budget heuristic.
func estTokens(s string) int {
	if t := len(s) / 4; t > 1 {
		return t
	}
	return 1
}

func registerRepoMapTool(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "repo_map",
		Description: "Rank the codebase by call-graph centrality (PageRank), blended with git churn when available, and return a compact, token-budgeted map of the most important symbols — the heavily-relied-upon, actively-changing core. Load it first for orientation. Pass focus (symbols/files) to bias the ranking toward an area you care about, or churn_weight to tune how much commit frequency matters.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in repoMapInput) (*mcp.CallToolResult, repoMapOutput, error) {
		out, err := repoMap(ctx, in)
		return nil, out, err
	})
}
