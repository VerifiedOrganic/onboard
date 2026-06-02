package server

import (
	"regexp"
	"strings"
)

// A focused reader for SQL DDL — just enough to turn CREATE TABLE statements into the
// entities, columns, and primary/foreign keys the schema tool renders. It is deliberately
// NOT a full SQL parser: it captures the balanced table body, splits it at parenthesis-depth-0
// commas, and classifies each definition with a few targeted regexes, degrading on exotic
// dialects rather than failing. The schema tool (tools_schema.go) owns the walk and rendering.

var (
	createTableRe  = regexp.MustCompile("(?is)create\\s+table\\s+(?:if\\s+not\\s+exists\\s+)?([`\"\\[\\]\\w.]+)\\s*\\(")
	pkConstraintRe = regexp.MustCompile(`(?is)primary\s+key\s*\(([^)]*)\)`)
	fkConstraintRe = regexp.MustCompile("(?is)foreign\\s+key\\s*\\(([^)]*)\\)\\s*references\\s+([`\"\\[\\]\\w.]+)\\s*(?:\\(([^)]*)\\))?")
	inlineRefRe    = regexp.MustCompile("(?is)references\\s+([`\"\\[\\]\\w.]+)\\s*(?:\\(([^)]*)\\))?")
)

// parseDDL pulls every CREATE TABLE out of one SQL file. For each it captures the balanced
// parenthesized body, splits it into top-level definitions, and classifies each as a column
// or a table-level constraint (primary/foreign key).
func parseDDL(sql, file string) ([]entity, []relationship) {
	var entities []entity
	var rels []relationship
	for _, loc := range createTableRe.FindAllStringSubmatchIndex(sql, -1) {
		name := cleanIdent(sql[loc[2]:loc[3]])
		openParen := loc[1] - 1 // the regex match ends at '('
		closeParen := matchParen(sql, openParen)
		if closeParen < 0 {
			continue
		}
		ent := entity{Name: name, File: file}
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

// classifyDef sorts one comma-delimited definition into a column or a table constraint,
// updating the entity's columns and the relationship list.
func classifyDef(def string, ent *entity, rels *[]relationship) {
	upper := strings.ToUpper(strings.TrimSpace(def))
	switch {
	case strings.HasPrefix(upper, "PRIMARY KEY"):
		for _, col := range splitCols(firstGroup(pkConstraintRe, def)) {
			markColumn(ent, col, func(c *column) { c.PK = true })
		}
	case strings.HasPrefix(upper, "FOREIGN KEY"), strings.HasPrefix(upper, "CONSTRAINT"), strings.HasPrefix(upper, "KEY"):
		if m := fkConstraintRe.FindStringSubmatch(def); m != nil {
			local := strings.TrimSpace(firstCol(m[1]))
			target := cleanIdent(m[2])
			markColumn(ent, local, func(c *column) { c.FK = true })
			*rels = append(*rels, relationship{From: ent.Name, To: target, Column: local})
		}
	case strings.HasPrefix(upper, "UNIQUE"), strings.HasPrefix(upper, "CHECK"), strings.HasPrefix(upper, "INDEX"), strings.HasPrefix(upper, "EXCLUDE"):
		// table-level constraint that defines no column or relationship we model
	default:
		parseColumn(def, ent, rels)
	}
}

// parseColumn reads a "name TYPE constraints..." column definition.
func parseColumn(def string, ent *entity, rels *[]relationship) {
	fields := strings.Fields(def)
	if len(fields) < 1 {
		return
	}
	col := column{Name: cleanIdent(fields[0])}
	if len(fields) >= 2 {
		col.Type = cleanIdent(fields[1])
	}
	upper := strings.ToUpper(def)
	if strings.Contains(upper, "PRIMARY KEY") {
		col.PK = true
	}
	if m := inlineRefRe.FindStringSubmatch(def); m != nil {
		col.FK = true
		*rels = append(*rels, relationship{From: ent.Name, To: cleanIdent(m[1]), Column: col.Name})
	}
	ent.Columns = append(ent.Columns, col)
}

func markColumn(ent *entity, name string, set func(*column)) {
	name = cleanIdent(name)
	for i := range ent.Columns {
		if strings.EqualFold(ent.Columns[i].Name, name) {
			set(&ent.Columns[i])
			return
		}
	}
}

// matchParen returns the index of the ')' that closes the '(' at openIdx, or -1.
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

// splitTopLevel splits on commas that sit at parenthesis depth 0, so DECIMAL(10,2) and
// PRIMARY KEY (a, b) are not split mid-expression.
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
		if c = cleanIdent(strings.TrimSpace(c)); c != "" {
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

// cleanIdent strips SQL identifier quoting (double quotes, backticks, brackets) and an
// optional schema qualifier, returning the bare table/column name.
func cleanIdent(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "`\"[]")
	if i := strings.LastIndexByte(s, '.'); i >= 0 {
		s = strings.Trim(s[i+1:], "`\"[]")
	}
	return s
}
