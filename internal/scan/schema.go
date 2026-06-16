package scan

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// A focused reader for SQL DDL — just enough to turn CREATE TABLE statements into the
// entities, columns, and primary/foreign keys the schema tool renders.

// Column is one column on a parsed entity.
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
	PK   bool   `json:"pk,omitempty"`
	FK   bool   `json:"fk,omitempty"`
}

// Entity is one CREATE TABLE result.
type Entity struct {
	Name    string   `json:"name"`
	File    string   `json:"file"`
	Columns []Column `json:"columns"`
}

// Relationship is a foreign-key edge between entities.
type Relationship struct {
	From   string `json:"from"` // the table holding the foreign key
	To     string `json:"to"`   // the referenced table
	Column string `json:"column,omitempty"`
}

var (
	createTableRe  = regexp.MustCompile("(?is)create\\s+table\\s+(?:if\\s+not\\s+exists\\s+)?([`\"\\[\\]\\w.]+)\\s*\\(")
	pkConstraintRe = regexp.MustCompile(`(?is)primary\s+key\s*\(([^)]*)\)`)
	fkConstraintRe = regexp.MustCompile("(?is)foreign\\s+key\\s*\\(([^)]*)\\)\\s*references\\s+([`\"\\[\\]\\w.]+)\\s*(?:\\(([^)]*)\\))?")
	inlineRefRe    = regexp.MustCompile("(?is)references\\s+([`\"\\[\\]\\w.]+)\\s*(?:\\(([^)]*)\\))?")
)

// ParseDDL pulls every CREATE TABLE out of one SQL file. For each it captures the balanced
// parenthesized body, splits it into top-level definitions, and classifies each as a column
// or a table-level constraint (primary/foreign key).
func ParseDDL(sql, file string) ([]Entity, []Relationship) {
	var entities []Entity
	var rels []Relationship
	for _, loc := range createTableRe.FindAllStringSubmatchIndex(sql, -1) {
		name := CleanIdent(sql[loc[2]:loc[3]])
		openParen := loc[1] - 1
		closeParen := matchParen(sql, openParen)
		if closeParen < 0 {
			continue
		}
		ent := Entity{Name: name, File: file}
		for _, def := range splitTopLevel(sql[openParen+1 : closeParen]) {
			if def == "" {
				continue
			}
			classifyDef(def, &ent, &rels)
		}
		entities = append(entities, ent)
	}
	return entities, rels
}

func classifyDef(def string, ent *Entity, rels *[]Relationship) {
	upper := strings.ToUpper(strings.TrimSpace(def))
	switch {
	case strings.HasPrefix(upper, "PRIMARY KEY"):
		for _, col := range splitCols(firstGroup(pkConstraintRe, def)) {
			markColumn(ent, col, func(c *Column) { c.PK = true })
		}
	case strings.HasPrefix(upper, "FOREIGN KEY"), strings.HasPrefix(upper, "CONSTRAINT"), strings.HasPrefix(upper, "KEY"):
		if m := fkConstraintRe.FindStringSubmatch(def); m != nil {
			local := strings.TrimSpace(firstCol(m[1]))
			target := CleanIdent(m[2])
			markColumn(ent, local, func(c *Column) { c.FK = true })
			*rels = append(*rels, Relationship{From: ent.Name, To: target, Column: local})
		}
	case strings.HasPrefix(upper, "UNIQUE"), strings.HasPrefix(upper, "CHECK"), strings.HasPrefix(upper, "INDEX"), strings.HasPrefix(upper, "EXCLUDE"):
	default:
		parseColumn(def, ent, rels)
	}
}

func parseColumn(def string, ent *Entity, rels *[]Relationship) {
	fields := strings.Fields(def)
	if len(fields) < 1 {
		return
	}
	col := Column{Name: CleanIdent(fields[0])}
	if len(fields) >= 2 {
		col.Type = CleanIdent(fields[1])
	}
	upper := strings.ToUpper(def)
	if strings.Contains(upper, "PRIMARY KEY") {
		col.PK = true
	}
	if m := inlineRefRe.FindStringSubmatch(def); m != nil {
		col.FK = true
		*rels = append(*rels, Relationship{From: ent.Name, To: CleanIdent(m[1]), Column: col.Name})
	}
	ent.Columns = append(ent.Columns, col)
}

func markColumn(ent *Entity, name string, set func(*Column)) {
	name = CleanIdent(name)
	for i := range ent.Columns {
		if strings.EqualFold(ent.Columns[i].Name, name) {
			set(&ent.Columns[i])
			return
		}
	}
}

func matchParen(s string, openIdx int) int {
	depth := 0
	for i := openIdx; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func splitTopLevel(s string) []string {
	var parts []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	parts = append(parts, strings.TrimSpace(s[start:]))
	return parts
}

func splitCols(s string) []string {
	var out []string
	for _, c := range strings.Split(s, ",") {
		if c = CleanIdent(strings.TrimSpace(c)); c != "" {
			out = append(out, c)
		}
	}
	return out
}

func firstCol(s string) string {
	if i := strings.IndexByte(s, ','); i >= 0 {
		return s[:i]
	}
	return s
}

func firstGroup(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

// CleanIdent strips SQL identifier quoting (double quotes, backticks, brackets) and an
// optional schema qualifier, returning the bare table/column name.
func CleanIdent(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "`\"[]")
	if i := strings.LastIndexByte(s, '.'); i >= 0 {
		s = strings.Trim(s[i+1:], "`\"[]")
	}
	return s
}

// FilterRelationships keeps only relationships whose endpoints are both known tables, and dedupes.
func FilterRelationships(rels []Relationship, known map[string]bool) []Relationship {
	out := rels[:0]
	seen := map[string]bool{}
	for _, r := range rels {
		r.To = CleanIdent(r.To)
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

// RenderERD emits a Mermaid erDiagram. Mermaid is strict: entity names and column types must
// be single tokens, so both are sanitized. A foreign key A.col -> B is drawn B ||--o{ A
// (one B has many A).
func RenderERD(entities []Entity, rels []Relationship) string {
	var b strings.Builder
	b.WriteString("erDiagram\n")
	for _, e := range entities {
		b.WriteString(fmt.Sprintf("  %s {\n", ERDToken(e.Name)))
		for _, c := range e.Columns {
			typ := ERDToken(c.Type)
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
			b.WriteString(fmt.Sprintf("    %s %s%s\n", typ, ERDToken(c.Name), key))
		}
		b.WriteString("  }\n")
	}
	for _, r := range rels {
		label := r.Column
		if label == "" {
			label = "fk"
		}
		b.WriteString(fmt.Sprintf("  %s ||--o{ %s : %q\n", ERDToken(r.To), ERDToken(r.From), label))
	}
	return b.String()
}

// ERDToken makes a name safe to use as a Mermaid erDiagram identifier/type token.
func ERDToken(s string) string {
	s = CleanIdent(s)
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			return r
		}
		return '_'
	}, s)
}
