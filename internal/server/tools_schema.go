package server

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/VerifiedOrganic/onboard/internal/scan"
)

// schema extracts a database schema from SQL DDL (CREATE TABLE statements in .sql files /
// migrations) into entities + relationships and renders a Mermaid erDiagram. Like deps, it
// produces FACTS read from the DDL, so the cartographer no longer has to guess the data model.
// It is intentionally a focused DDL reader, not a full SQL parser; it degrades on exotic
// syntax rather than failing.

const maxSchemaFiles = 200

type schemaInput struct {
	Root string `json:"root,omitempty" jsonschema:"repo root; defaults to the working directory"`
}

type schemaOutput struct {
	Entities      []scan.Entity       `json:"entities"`
	Relationships []scan.Relationship `json:"relationships"`
	Mermaid       string              `json:"mermaid,omitempty"`
	Note          string              `json:"note,omitempty"`
}

func schemaExtract(_ context.Context, in schemaInput) (schemaOutput, error) {
	out := schemaOutput{}
	root, err := resolveRoot(in.Root)
	if err != nil {
		return out, err
	}

	seen := map[string]bool{}
	scanned := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && scan.ShouldSkipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".sql") {
			return nil
		}
		if scanned >= maxSchemaFiles {
			return fs.SkipDir
		}
		scanned++
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		ents, rels := scan.ParseDDL(string(data), filepath.ToSlash(rel))
		for _, e := range ents {
			if seen[e.Name] {
				continue
			}
			seen[e.Name] = true
			out.Entities = append(out.Entities, e)
		}
		out.Relationships = append(out.Relationships, rels...)
		return nil
	})

	if len(out.Entities) == 0 {
		out.Note = "No CREATE TABLE statements found in any .sql file."
		return out, nil
	}

	out.Relationships = scan.FilterRelationships(out.Relationships, seen)
	sort.Slice(out.Entities, func(i, j int) bool { return out.Entities[i].Name < out.Entities[j].Name })
	out.Mermaid = scan.RenderERD(out.Entities, out.Relationships)
	out.Note = "Entities and relationships parsed from SQL DDL (facts). A focused DDL reader, not a full SQL parser: exotic dialects or programmatically-built schemas may be missed."
	return out, nil
}

func registerSchemaTool(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "schema",
		Description: "Extract a database schema from SQL DDL (CREATE TABLE statements in .sql files / migrations): entities with columns and primary/foreign keys, the relationships between them, and a Mermaid erDiagram. Facts parsed from the DDL, not inferred. Use to ground an ERD or understand the data model.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in schemaInput) (*mcp.CallToolResult, schemaOutput, error) {
		out, err := schemaExtract(ctx, in)
		return nil, out, err
	})
}