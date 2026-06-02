package server

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// schema extracts a database schema from SQL DDL (CREATE TABLE statements in .sql files /
// migrations) into entities + relationships and renders a Mermaid erDiagram. Like deps, it
// produces FACTS read from the DDL, so the cartographer no longer has to guess the data model.
// It is intentionally a focused DDL reader, not a full SQL parser; it degrades on exotic
// syntax rather than failing.

const maxSchemaFiles = 200

type column struct {
	Name string `json:"name"`
	Type string `json:"type"`
	PK   bool   `json:"pk,omitempty"`
	FK   bool   `json:"fk,omitempty"`
}

type entity struct {
	Name    string   `json:"name"`
	File    string   `json:"file"`
	Columns []column `json:"columns"`
}

type relationship struct {
	From   string `json:"from"` // the table holding the foreign key
	To     string `json:"to"`   // the referenced table
	Column string `json:"column,omitempty"`
}

type schemaInput struct {
	Root string `json:"root,omitempty" jsonschema:"repo root; defaults to the working directory"`
}

type schemaOutput struct {
	Entities      []entity       `json:"entities"`
	Relationships []relationship `json:"relationships"`
	Mermaid       string         `json:"mermaid,omitempty"`
	Note          string         `json:"note,omitempty"`
}

func schemaExtract(_ context.Context, in schemaInput) (schemaOutput, error) {
	out := schemaOutput{}
	root, err := resolveRoot(in.Root)
	if err != nil {
		return out, err
	}

	seen := map[string]bool{} // table name -> already captured (first definition wins)
	scanned := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && shouldSkipDir(d.Name()) {
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
		ents, rels := parseDDL(string(data), filepath.ToSlash(rel))
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

	// Keep only relationships whose endpoints are both known tables, and dedupe.
	out.Relationships = filterRelationships(out.Relationships, seen)
	sort.Slice(out.Entities, func(i, j int) bool { return out.Entities[i].Name < out.Entities[j].Name })
	out.Mermaid = renderERD(out.Entities, out.Relationships)
	out.Note = "Entities and relationships parsed from SQL DDL (facts). A focused DDL reader, not a full SQL parser: exotic dialects or programmatically-built schemas may be missed."
	return out, nil
}

// SQL DDL parsing (parseDDL, classifyDef, column/constraint readers, and the small SQL
// helpers) lives in sqlddl.go.

func filterRelationships(rels []relationship, known map[string]bool) []relationship {
	out := rels[:0]
	seen := map[string]bool{}
	for _, r := range rels {
		r.To = cleanIdent(r.To)
		key := r.From + "\x00" + r.To + "\x00" + r.Column
		if !known[r.To] || !known[r.From] || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return out
}

// renderERD emits a Mermaid erDiagram. Mermaid is strict: entity names and column types must
// be single tokens, so both are sanitized. A foreign key A.col -> B is drawn B ||--o{ A
// (one B has many A).
func renderERD(entities []entity, rels []relationship) string {
	var b strings.Builder
	b.WriteString("erDiagram\n")
	for _, e := range entities {
		b.WriteString(fmt.Sprintf("  %s {\n", erdToken(e.Name)))
		for _, c := range e.Columns {
			typ := erdToken(c.Type)
			if typ == "" {
				typ = "unknown"
			}
			key := ""
			switch {
			case c.PK && c.FK:
				key = " PK,FK"
			case c.PK:
				key = " PK"
			case c.FK:
				key = " FK"
			}
			b.WriteString(fmt.Sprintf("    %s %s%s\n", typ, erdToken(c.Name), key))
		}
		b.WriteString("  }\n")
	}
	for _, r := range rels {
		label := r.Column
		if label == "" {
			label = "fk"
		}
		b.WriteString(fmt.Sprintf("  %s ||--o{ %s : %q\n", erdToken(r.To), erdToken(r.From), label))
	}
	return b.String()
}

// erdToken makes a name safe to use as a Mermaid erDiagram identifier/type token.
func erdToken(s string) string {
	s = cleanIdent(s)
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			return r
		}
		return '_'
	}, s)
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
