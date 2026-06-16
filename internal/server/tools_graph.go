package server

import (
	"context"
	"fmt"
	"strings"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/VerifiedOrganic/onboard/internal/providers"
)

const maxTraceNodes = 250

// --- trace_flow ---

type traceFlowInput struct {
	Root    string `json:"root,omitempty" jsonschema:"repo root; defaults to the working directory"`
	Entry   string `json:"entry" jsonschema:"symbol to trace from: a function name, file::name, or a qualified-name substring"`
	Depth   int    `json:"depth,omitempty" jsonschema:"max call depth to follow (default 4)"`
	Format  string `json:"format,omitempty" jsonschema:"set to \"mermaid\" to also return the trace as a Mermaid sequenceDiagram"`
	Precise bool   `json:"precise,omitempty" jsonschema:"for Go modules, enrich with type-checked edges; for Rust Cargo projects, enrich with rust-analyzer call hierarchy when available; slower, requires the language toolchain"`
	Refresh bool   `json:"refresh,omitempty" jsonschema:"re-index the repo instead of using the cached graph"`
}

type traceNode struct {
	QName   string   `json:"qname"`
	Symbol  string   `json:"symbol,omitempty"` // display name: methods qualified by receiver (T.Method)
	File    string   `json:"file"`
	Line    int      `json:"line"`
	Depth   int      `json:"depth"`
	Callees []string `json:"callees,omitempty"`
}

type traceFlowOutput struct {
	Entry      string      `json:"entry"`
	Matched    string      `json:"matched_symbol,omitempty"`
	Candidates []string    `json:"candidates,omitempty"`
	Nodes      []traceNode `json:"nodes"`
	Mermaid    string      `json:"mermaid,omitempty"` // sequenceDiagram, when format="mermaid"
	Truncated  bool        `json:"truncated"`
	Provider   string      `json:"provider"`
	Note       string      `json:"note,omitempty"`
}

func traceFlow(ctx context.Context, in traceFlowInput) (traceFlowOutput, error) {
	out := traceFlowOutput{Entry: in.Entry, Candidates: []string{}, Nodes: []traceNode{}}
	depth := in.Depth
	if depth <= 0 {
		depth = 4
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

	syms := g.FindSymbols(in.Entry)
	if len(syms) == 0 {
		out.Note = "No symbol matched. Provide a function name, file::name, or a qualified-name substring."
		return out, nil
	}
	start := syms[0].QName
	out.Matched = start
	if len(syms) > 1 {
		for _, s := range syms {
			out.Candidates = append(out.Candidates, s.QName)
		}
	}
	switch {
	case g.Provider == "null":
		out.Note = "Definitions-only provider: no call graph available, so the trace shows the entry symbol alone."
	case in.Precise && !g.Precise:
		out.Note = semanticPrecisionUnavailableNote() + edgeCaveat(g)
	default:
		out.Note = edgeCaveat(g) + goPrecisionHint(g, in.Precise)
	}
	out.Note = ambiguityNote(in.Entry, syms) + out.Note

	type item struct {
		q string
		d int
	}
	visited := map[string]bool{}
	seen := map[string]bool{start: true} // everything ever enqueued; used to detect real truncation
	queue := []item{{start, 0}}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		if visited[it.q] {
			continue
		}
		visited[it.q] = true

		node := traceNode{QName: it.q, Depth: it.d}
		if sym := g.Defs[it.q]; sym != nil {
			node.File, node.Line, node.Symbol = sym.File, sym.Line, sym.Display()
		}
		callees := g.Callees(it.q)
		node.Callees = callees
		out.Nodes = append(out.Nodes, node)

		if len(out.Nodes) >= maxTraceNodes {
			out.Truncated = true
			break
		}
		if it.d >= depth {
			// Truncated only if a callee will never be shown (not already seen) —
			// not merely because a depth-limit node happens to have callees.
			for _, c := range callees {
				if !seen[c] {
					out.Truncated = true
					break
				}
			}
			continue
		}
		for _, c := range callees {
			if !seen[c] {
				seen[c] = true
				queue = append(queue, item{c, it.d + 1})
			}
		}
	}
	if in.Format == "mermaid" {
		out.Mermaid = renderSequence(out.Nodes, g)
	}
	return out, nil
}

// renderSequence turns a trace into a Mermaid sequenceDiagram: each discovered caller->callee
// edge (where both ends are in the trace) becomes a message, in breadth-first discovery order.
// That order reflects reachability, not strict runtime sequencing — a serviceable approximation
// the note flags. Participants are the short symbol names, so same-named symbols in different
// files stay on distinct lifelines while still displaying readable labels.
func renderSequence(nodes []traceNode, g *providers.Graph) string {
	inSet := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		inSet[n.QName] = true
	}
	short := func(q string) string {
		if s := g.Defs[q]; s != nil {
			return s.Display()
		}
		if i := strings.LastIndex(q, "::"); i >= 0 {
			return q[i+2:]
		}
		return q
	}
	var b strings.Builder
	b.WriteString("sequenceDiagram\n")
	declared := map[string]bool{}
	for _, n := range nodes {
		id := seqToken(n.QName)
		if !declared[id] {
			declared[id] = true
			b.WriteString("  participant " + id + " as " + short(n.QName) + "\n")
		}
	}
	for _, n := range nodes {
		from := seqToken(n.QName)
		for _, callee := range n.Callees {
			if inSet[callee] {
				b.WriteString("  " + from + "->>" + seqToken(callee) + ": " + short(callee) + "\n")
			}
		}
	}
	return b.String()
}

// seqToken sanitizes a symbol name into a Mermaid sequence participant identifier.
func seqToken(s string) string {
	t := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			return r
		}
		return '_'
	}, s)
	if t == "" {
		return "_"
	}
	if t[0] >= '0' && t[0] <= '9' {
		return "p_" + t
	}
	return t
}

// --- impact ---

type impactInput struct {
	Root    string `json:"root,omitempty" jsonschema:"repo root; defaults to the working directory"`
	Symbol  string `json:"symbol" jsonschema:"symbol whose blast radius to compute: a function name, file::name, or qualified-name substring"`
	Precise bool   `json:"precise,omitempty" jsonschema:"for Go modules, enrich with type-checked edges; for Rust Cargo projects, enrich with rust-analyzer call hierarchy when available; slower, requires the language toolchain"`
	Refresh bool   `json:"refresh,omitempty" jsonschema:"re-index the repo instead of using the cached graph"`
}

type impactOutput struct {
	Symbol            string   `json:"symbol"`
	Matched           string   `json:"matched_symbol,omitempty"`
	Candidates        []string `json:"candidates,omitempty"`
	DirectCallers     []string `json:"direct_callers"`
	TransitiveCallers []string `json:"transitive_callers"`
	AtRiskTests       []string `json:"at_risk_tests"`
	ImpactedCount     int      `json:"impacted_count"`
	Provider          string   `json:"provider"`
	Note              string   `json:"note,omitempty"`
}

func impactAnalysis(ctx context.Context, in impactInput) (impactOutput, error) {
	out := impactOutput{
		Symbol:            in.Symbol,
		Candidates:        []string{},
		DirectCallers:     []string{},
		TransitiveCallers: []string{},
		AtRiskTests:       []string{},
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

	if g.Provider == "null" {
		out.Note = "Definitions-only provider: no call graph, so blast radius cannot be computed. Install a supported-language tree or use a code-graph backend."
		return out, nil
	}

	syms := g.FindSymbols(in.Symbol)
	if len(syms) == 0 {
		out.Note = "No symbol matched. Provide a function name, file::name, or a qualified-name substring."
		return out, nil
	}
	matched := syms[0].QName
	out.Matched = matched
	if len(syms) > 1 {
		for _, s := range syms {
			out.Candidates = append(out.Candidates, s.QName)
		}
	}

	out.DirectCallers = g.Callers(matched)
	out.TransitiveCallers = g.Impact(matched)
	out.ImpactedCount = len(out.TransitiveCallers)
	for _, q := range out.TransitiveCallers {
		if isTestQName(q, g) {
			out.AtRiskTests = append(out.AtRiskTests, q)
		}
	}
	out.Note = edgeCaveat(g) + goPrecisionHint(g, in.Precise)
	if in.Precise && !g.Precise {
		out.Note = semanticPrecisionUnavailableNote() + out.Note
	}
	out.Note = ambiguityNote(in.Symbol, syms) + out.Note
	return out, nil
}

// ambiguityNote makes a silent pick-first visible: when a query matched more
// than one definition, the note leads with that fact so a caller that does not
// inspect the candidates field cannot mistake the result for the only match.
func ambiguityNote(query string, syms []*providers.Symbol) string {
	if len(syms) <= 1 {
		return ""
	}
	return fmt.Sprintf("Ambiguous: %q matched %d definitions; results are for %s — pass a fuller qualified name (see candidates) if that is the wrong one. ",
		query, len(syms), syms[0].QName)
}

func registerGraphTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "trace_flow",
		Description: "Trace an execution flow from an entry symbol through its callees (breadth-first to a depth). Use to follow a request/operation end to end. Backed by a syntactic call graph.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in traceFlowInput) (*mcp.CallToolResult, traceFlowOutput, error) {
		out, err := traceFlow(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "impact",
		Description: "Compute the blast radius of changing a symbol: direct callers, all transitive callers, and which of those are tests. Use to answer 'what breaks if I change X' before editing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in impactInput) (*mcp.CallToolResult, impactOutput, error) {
		out, err := impactAnalysis(ctx, in)
		return nil, out, err
	})
}
