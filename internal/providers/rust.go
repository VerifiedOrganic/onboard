package providers

import (
	"strings"
	"unicode"
)

// rustTagsQuery is an explicit tree-sitter tags query for Rust. The generic inferred query
// misses method calls via field_expression (self.foo(), obj.bar()) because it only matches
// field_identifier as a direct child of call_expression, but Rust nests it inside
// field_expression. This query also captures modules, macros, constants, and type aliases.
const rustTagsQuery = `
; Definitions
(function_item (identifier) @name) @definition.function
(function_signature_item (identifier) @name) @definition.function
(struct_item (type_identifier) @name) @definition.type
(enum_item (type_identifier) @name) @definition.type
(trait_item (type_identifier) @name) @definition.type
(type_item (type_identifier) @name) @definition.type
(mod_item (identifier) @name) @definition.module
(macro_definition (identifier) @name) @definition.macro
(const_item (identifier) @name) @definition.constant
(static_item (identifier) @name) @definition.variable

; Call references — direct, method, scoped, and macro calls.
(call_expression (identifier) @name) @reference.call
(call_expression (field_expression (field_identifier) @name)) @reference.call
(call_expression (scoped_identifier (identifier) @name)) @reference.call
(macro_invocation (identifier) @name) @reference.call
`

// rustOwner returns the nearest enclosing impl/trait owner at nameStart, if any. It is a
// lightweight source scan layered on top of tree-sitter tags: the tagger already identified
// the definition, but Rust's tags do not consistently attach the impl/trait container. This
// keeps symbol display useful without turning the universal provider into a Rust parser.
func rustOwner(src []byte, nameStart uint32) string {
	limit := int(nameStart)
	if limit <= 0 || limit > len(src) {
		return ""
	}
	text := string(src[:limit])
	bestOpen := -1
	bestOwner := ""
	for _, kw := range []string{"impl", "trait"} {
		pos := 0
		for {
			i := indexRustKeyword(text[pos:], kw)
			if i < 0 {
				break
			}
			start := pos + i
			openRel := strings.IndexByte(text[start:], '{')
			if openRel < 0 {
				break
			}
			open := start + openRel
			if open >= limit {
				break
			}
			if rustScopeOpenAt(text[open:limit]) {
				if owner := parseRustOwnerHeader(text[start:open]); owner != "" && open > bestOpen {
					bestOpen = open
					bestOwner = owner
				}
			}
			pos = start + len(kw)
		}
	}
	return bestOwner
}

// rustDefinitionIsTest marks Rust unit-test functions whose file path often looks like
// normal production code (src/lib.rs). It intentionally accepts the common async-test
// attribute forms too: #[tokio::test], #[async_std::test], and similar.
func rustDefinitionIsTest(src []byte, declStart uint32) bool {
	start := int(declStart)
	if start < 0 || start > len(src) {
		return false
	}
	from := start - 768
	if from < 0 {
		from = 0
	}
	prefix := string(src[from:start])
	return strings.Contains(prefix, "#[test]") ||
		strings.Contains(prefix, "::test]") ||
		strings.Contains(prefix, "#[cfg(test)]")
}

func indexRustKeyword(s, kw string) int {
	offset := 0
	for {
		i := strings.Index(s[offset:], kw)
		if i < 0 {
			return -1
		}
		i += offset
		if rustBoundary(s, i-1) && rustBoundary(s, i+len(kw)) {
			return i
		}
		offset = i + len(kw)
	}
}

func rustBoundary(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return true
	}
	r := rune(s[i])
	return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_')
}

func rustScopeOpenAt(s string) bool {
	depth := 0
	for _, r := range s {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth <= 0 {
				return false
			}
		}
	}
	return depth > 0
}

func parseRustOwnerHeader(header string) string {
	header = strings.TrimSpace(collapseSpace(header))
	switch {
	case strings.HasPrefix(header, "trait "):
		return rustTypeName(strings.TrimSpace(strings.TrimPrefix(header, "trait ")))
	case strings.HasPrefix(header, "unsafe trait "):
		return rustTypeName(strings.TrimSpace(strings.TrimPrefix(header, "unsafe trait ")))
	case strings.HasPrefix(header, "impl"):
		rest := strings.TrimSpace(strings.TrimPrefix(header, "impl"))
		rest = trimRustQualifiers(rest)
		rest = trimLeadingRustGenerics(rest)
		if i := strings.LastIndex(rest, " for "); i >= 0 {
			trait := rustTypeName(rest[:i])
			typ := rustTypeName(rest[i+5:])
			switch {
			case typ != "" && trait != "":
				return typ + " as " + trait
			case typ != "":
				return typ
			}
		}
		return rustTypeName(rest)
	default:
		return ""
	}
}

func trimRustQualifiers(s string) string {
	for {
		before := s
		for _, q := range []string{"unsafe ", "const ", "async "} {
			s = strings.TrimSpace(strings.TrimPrefix(s, q))
		}
		if s == before {
			return s
		}
	}
}

func trimLeadingRustGenerics(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "<") {
		return s
	}
	depth := 0
	for i, r := range s {
		switch r {
		case '<':
			depth++
		case '>':
			depth--
			if depth == 0 {
				return strings.TrimSpace(s[i+1:])
			}
		}
	}
	return s
}

func rustTypeName(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, " where "); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexAny(s, "({"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	for _, p := range []string{"&", "&mut ", "*const ", "*mut ", "dyn ", "mut "} {
		s = strings.TrimSpace(strings.TrimPrefix(s, p))
	}
	if i := strings.IndexByte(s, '<'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	best := ""
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == ':')
	}) {
		part = strings.Trim(part, ":")
		if part == "" || rustKeyword(part) {
			continue
		}
		best = part
	}
	return best
}

func rustKeyword(s string) bool {
	switch s {
	case "impl", "for", "where", "dyn", "mut", "const", "unsafe", "pub", "crate", "super", "self", "Self":
		return true
	default:
		return false
	}
}

func collapseSpace(s string) string {
	var b strings.Builder
	lastSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		b.WriteRune(r)
		lastSpace = false
	}
	return b.String()
}
