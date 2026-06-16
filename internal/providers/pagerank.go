package providers

import "path/filepath"

// PageRank-based symbol-importance ranking over the call graph. Lives apart from the Graph
// data structure and its query methods (provider.go) because it is a self-contained
// algorithm — the orientation/ranking layer, not the graph itself.

// PageRank scores every node in the call graph by centrality. Edges flow
// caller -> callee, so a symbol called (directly or transitively) by many important
// symbols accumulates a high score — the heavily-relied-upon core of the codebase.
// The ranking is deterministic (nodes are processed in sorted order).
//
// seeds (optional) personalize the teleport distribution toward those nodes, biasing
// the ranking toward an area of focus. A seed may be an exact QName, a repo-relative
// file path (selects every symbol defined in that file), or a bare symbol name.
// Unknown seeds are ignored; if none resolve, the ranking is uniform (global
// importance). Nodes that appear only as call endpoints (e.g. file-scope callers) are
// included so rank can flow from call sites that are not themselves definitions.
func (g *Graph) PageRank(seeds []string) map[string]float64 {
	nodeSet := map[string]bool{}
	for q := range g.Defs {
		nodeSet[q] = true
	}
	for caller, callees := range g.Forward {
		nodeSet[caller] = true
		for _, c := range callees {
			nodeSet[c] = true
		}
	}
	n := len(nodeSet)
	if n == 0 {
		return map[string]float64{}
	}
	nodes := sortedKeys(nodeSet)
	idx := make(map[string]int, n)
	for i, q := range nodes {
		idx[q] = i
	}
	out := make([][]int, n)
	for caller, callees := range g.Forward {
		ci := idx[caller]
		for _, callee := range callees {
			out[ci] = append(out[ci], idx[callee])
		}
	}

	teleport := make([]float64, n)
	if seedSet := g.expandSeeds(seeds); len(seedSet) > 0 {
		w := 1.0 / float64(len(seedSet))
		hits := 0
		for q := range seedSet {
			if i, ok := idx[q]; ok {
				teleport[i] = w
				hits++
			}
		}
		if hits == 0 { // seeds resolved to no actual nodes — fall back to uniform
			for i := range teleport {
				teleport[i] = 1.0 / float64(n)
			}
		}
	} else {
		for i := range teleport {
			teleport[i] = 1.0 / float64(n)
		}
	}

	const (
		damping    = 0.85
		iterations = 50
	)
	rank := make([]float64, n)
	for i := range rank {
		rank[i] = 1.0 / float64(n)
	}
	next := make([]float64, n)
	for it := 0; it < iterations; it++ {
		var dangling float64
		for i := 0; i < n; i++ {
			if len(out[i]) == 0 {
				dangling += rank[i]
			}
		}
		// Base mass: random-restart plus the dangling-node mass, both spread over the
		// teleport vector so total rank is conserved at 1.
		for i := 0; i < n; i++ {
			next[i] = (1-damping)*teleport[i] + damping*dangling*teleport[i]
		}
		for i := 0; i < n; i++ {
			if len(out[i]) == 0 {
				continue
			}
			share := damping * rank[i] / float64(len(out[i]))
			for _, j := range out[i] {
				next[j] += share
			}
		}
		rank, next = next, rank
	}

	result := make(map[string]float64, n)
	for i, q := range nodes {
		result[q] = rank[i]
	}
	return result
}

// expandSeeds resolves personalization seeds to a set of node QNames.
func (g *Graph) expandSeeds(seeds []string) map[string]bool {
	set := map[string]bool{}
	for _, s := range seeds {
		if s == "" {
			continue
		}
		if _, ok := g.Defs[s]; ok {
			set[s] = true
		}
		slashed := filepath.ToSlash(s)
		for q, sym := range g.Defs {
			if sym == nil {
				continue
			}
			if filepath.ToSlash(sym.File) == slashed || sym.Name == s {
				set[q] = true
			}
		}
	}
	return set
}
