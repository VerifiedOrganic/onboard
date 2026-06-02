package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/VerifiedOrganic/onboard/internal/providers"
)

const (
	minMapNodes = 5
	maxMapNodes = 12
)

type mapNode struct {
	ID          string   `json:"id"`
	Label       string   `json:"label,omitempty"`
	Description string   `json:"description,omitempty"`
	Files       []string `json:"files,omitempty"`
}

type mapEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label,omitempty"`
}

type renderMapInput struct {
	Root       string    `json:"root,omitempty" jsonschema:"repo root; defaults to the working directory"`
	Topic      string    `json:"topic,omitempty" jsonschema:"title for the diagram, e.g. 'Architecture' or 'Auth flow'"`
	Format     string    `json:"format,omitempty" jsonschema:"html (self-contained interactive map) or mermaid (diagram-as-code); default html"`
	Nodes      []mapNode `json:"nodes,omitempty" jsonschema:"explicit nodes; if omitted, a package-level map is derived from the code graph"`
	Edges      []mapEdge `json:"edges,omitempty" jsonschema:"explicit edges between node ids; ignored unless nodes are given"`
	OutputPath string    `json:"output_path,omitempty" jsonschema:"absolute path to write the file; if empty the content is only returned"`
	Refresh    bool      `json:"refresh,omitempty" jsonschema:"re-index the repo instead of using the cached graph (only when deriving)"`
}

type renderMapOutput struct {
	Format    string `json:"format"`
	Content   string `json:"content"`
	Path      string `json:"path,omitempty"`
	NodeCount int    `json:"node_count"`
	Derived   bool   `json:"derived"`
	Truncated bool   `json:"truncated"`
	Note      string `json:"note,omitempty"`
}

func renderMap(ctx context.Context, in renderMapInput) (renderMapOutput, error) {
	out := renderMapOutput{Format: strings.ToLower(in.Format)}
	if out.Format != "mermaid" {
		out.Format = "html"
	}
	topic := in.Topic
	if topic == "" {
		topic = "Codebase map"
	}

	nodes, edges := in.Nodes, in.Edges
	if len(nodes) == 0 {
		root, err := resolveRoot(in.Root)
		if err != nil {
			return out, err
		}
		g, err := indexGraph(ctx, root, in.Refresh, false) // package-level import map: syntactic is sufficient
		if err != nil {
			return out, err
		}
		var truncated bool
		nodes, edges, truncated = deriveMap(g)
		out.Derived = true
		out.Truncated = truncated
		if len(nodes) == 0 {
			out.Note = "No structural nodes could be derived (no cross-package call edges found). Provide explicit nodes/edges to render a map."
			return out, nil
		}
		if len(nodes) < minMapNodes {
			out.Note = fmt.Sprintf("Only %d nodes derived; a map is usually most legible with %d–%d.", len(nodes), minMapNodes, maxMapNodes)
		}
	}

	// Ensure node ids are mermaid-safe and edges reference known ids.
	idMap := map[string]string{}
	for i := range nodes {
		safe := sanitizeID(nodes[i].ID)
		idMap[nodes[i].ID] = safe
		nodes[i].ID = safe
		if nodes[i].Label == "" {
			nodes[i].Label = nodes[i].ID
		}
	}
	var keptEdges []mapEdge
	known := map[string]bool{}
	for _, n := range nodes {
		known[n.ID] = true
	}
	for _, e := range edges {
		from := idMap[e.From]
		if from == "" {
			from = sanitizeID(e.From)
		}
		to := idMap[e.To]
		if to == "" {
			to = sanitizeID(e.To)
		}
		if known[from] && known[to] && from != to {
			keptEdges = append(keptEdges, mapEdge{From: from, To: to, Label: e.Label})
		}
	}
	out.NodeCount = len(nodes)

	if out.Format == "mermaid" {
		out.Content = renderMermaid(topic, nodes, keptEdges)
	} else {
		out.Content = renderMapHTML(topic, nodes, keptEdges)
	}

	if in.OutputPath != "" {
		// OutputPath is caller-controlled by design: render_map is a file-writing
		// tool and the MCP caller (the agent) is trusted, like Write. The content
		// is always returned inline too, so writing is opt-in.
		if err := os.MkdirAll(filepath.Dir(in.OutputPath), 0o700); err != nil {
			return out, err
		}
		if err := os.WriteFile(in.OutputPath, []byte(out.Content), 0o644); err != nil {
			return out, err
		}
		out.Path = in.OutputPath
	}
	return out, nil
}

// deriveMap aggregates the file-level call graph to a directory-level dependency
// map, keeping the most-connected directories (5–12 nodes).
func deriveMap(g *providers.Graph) (nodes []mapNode, edges []mapEdge, truncated bool) {
	dirOf := func(qname string) string {
		file := qname
		if s := g.Defs[qname]; s != nil {
			file = s.File
		} else if i := strings.Index(qname, "::"); i > 0 {
			file = qname[:i]
		}
		d := filepath.ToSlash(filepath.Dir(file))
		if d == "." || d == "" {
			return "(root)"
		}
		return d
	}

	filesByDir := map[string]map[string]bool{}
	for _, s := range g.Defs {
		d := filepath.ToSlash(filepath.Dir(s.File))
		if d == "." || d == "" {
			d = "(root)"
		}
		if filesByDir[d] == nil {
			filesByDir[d] = map[string]bool{}
		}
		filesByDir[d][s.File] = true
	}

	deg := map[string]int{}
	edgeCount := map[string]map[string]int{}
	for from, tos := range g.Forward {
		fd := dirOf(from)
		for _, to := range tos {
			td := dirOf(to)
			if fd == td {
				continue
			}
			if edgeCount[fd] == nil {
				edgeCount[fd] = map[string]int{}
			}
			edgeCount[fd][td]++
			deg[fd]++
			deg[td]++
		}
	}

	type ranked struct {
		dir string
		deg int
	}
	var all []ranked
	for d := range filesByDir {
		all = append(all, ranked{d, deg[d]})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].deg != all[j].deg {
			return all[i].deg > all[j].deg
		}
		return all[i].dir < all[j].dir
	})
	if len(all) > maxMapNodes {
		all = all[:maxMapNodes]
		truncated = true
	}

	kept := map[string]bool{}
	for _, r := range all {
		kept[r.dir] = true
		files := make([]string, 0, len(filesByDir[r.dir]))
		for f := range filesByDir[r.dir] {
			files = append(files, f)
		}
		sort.Strings(files)
		nodes = append(nodes, mapNode{
			ID:          r.dir,
			Label:       r.dir,
			Description: fmt.Sprintf("%d file(s), %d call connection(s)", len(files), r.deg),
			Files:       files,
		})
	}
	for fd, tos := range edgeCount {
		if !kept[fd] {
			continue
		}
		for td, n := range tos {
			if !kept[td] {
				continue
			}
			label := ""
			if n > 1 {
				label = fmt.Sprintf("%d", n)
			}
			edges = append(edges, mapEdge{From: fd, To: td, Label: label})
		}
	}
	return nodes, edges, truncated
}

// Mermaid/HTML rendering (renderMermaid, renderMapHTML, the sanitizers, and the HTML
// template) lives in map_render.go.

func registerMapTool(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "render_map",
		Description: "Render a navigable map of the codebase. With explicit nodes/edges it renders exactly those; otherwise it derives a package-level dependency map from the code graph. Format 'html' produces a self-contained interactive file (Mermaid + pan/zoom + click-to-detail); 'mermaid' produces diagram-as-code suitable for committing.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in renderMapInput) (*mcp.CallToolResult, renderMapOutput, error) {
		out, err := renderMap(ctx, in)
		return nil, out, err
	})
}
