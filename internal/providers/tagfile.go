package providers

import (
	"strings"

	ts "github.com/odvcencio/gotreesitter"
)

// TagFile extracts definitions and raw call references from tree-sitter tags for one file.
func TagFile(rel, lang string, src []byte, tags []ts.Tag) ([]*Symbol, []RawRef) {
	return tagFile(rel, lang, src, tags)
}

func tagFile(rel, lang string, src []byte, tags []ts.Tag) ([]*Symbol, []RawRef) {
	local := map[string]*Symbol{}
	var defs []*Symbol
	var fileDefs []defSpan
	byQName := map[string]*Symbol{}
	for _, t := range tags {
		if !strings.HasPrefix(t.Kind, "definition.") {
			continue
		}
		kind := strings.TrimPrefix(t.Kind, "definition.")
		line := int(t.NameRange.StartPoint.Row) + 1
		qn := uniqueQName(local, rel, t.Name, line)
		sym := &Symbol{
			QName:  qn,
			Name:   t.Name,
			Kind:   kind,
			File:   rel,
			Line:   line,
			Column: int(t.NameRange.StartPoint.Column),
			Lang:   lang,
		}
		if kind == "method" && lang == "go" {
			sym.Recv = goReceiverType(src, t.Range.StartByte, t.NameRange.StartByte)
		}
		if kind == "method" && (lang == "javascript" || lang == "typescript" || lang == "tsx" || lang == "svelte") {
			if classQName := enclosing(fileDefs, t.Range.StartByte, t.Range.EndByte); classQName != "" {
				if parentSym, ok := local[classQName]; ok && (parentSym.Kind == "class" || parentSym.Kind == "component") {
					if idx := strings.LastIndex(classQName, "::"); idx >= 0 {
						sym.Recv = classQName[idx+2:]
					}
				}
			}
		}
		if lang == "rust" {
			sym.Recv = rustOwner(src, t.NameRange.StartByte)
			if sym.Recv != "" && kind == "function" {
				sym.Kind = "method"
			}
			sym.Test = rustDefinitionIsTest(src, t.Range.StartByte)
			sym.Public = rustDefinitionIsPublic(src, t.Range.StartByte, t.NameRange.StartByte)
		}
		local[qn] = sym
		byQName[qn] = sym
		defs = append(defs, sym)
		fileDefs = append(fileDefs, defSpan{qname: qn, start: t.Range.StartByte, end: t.Range.EndByte})
	}
	var refs []RawRef
	for _, t := range tags {
		if !strings.HasPrefix(t.Kind, "reference.") {
			continue
		}
		caller := enclosing(fileDefs, t.Range.StartByte, t.Range.EndByte)
		if caller == "" {
			caller = rel + "::(top-level)"
		}
		ref := RawRef{CallerQName: caller, CallerFile: rel, CalleeName: t.Name, AllowBare: true}
		switch lang {
		case "rust":
			ref.CalleeRecv, ref.AllowBare = rustRefHint(src, t.Range.StartByte, t.NameRange.StartByte, byQName[caller])
		case "javascript", "typescript", "tsx", "svelte":
			ref.CalleeRecv, ref.AllowBare = jsRefHint(src, t.Range.StartByte, t.NameRange.StartByte)
		}
		refs = append(refs, ref)
	}
	return defs, refs
}

func goReceiverType(src []byte, declStart, nameStart uint32) string {
	if int(declStart) > len(src) || int(nameStart) > len(src) || declStart >= nameStart {
		return ""
	}
	header := string(src[declStart:nameStart])
	open := strings.IndexByte(header, '(')
	if open < 0 {
		return ""
	}
	closeRel := strings.IndexByte(header[open:], ')')
	if closeRel < 0 {
		return ""
	}
	recv := strings.TrimSpace(header[open+1 : open+closeRel])
	if recv == "" {
		return ""
	}
	if sp := strings.IndexAny(recv, " \t"); sp >= 0 {
		recv = strings.TrimSpace(recv[sp+1:])
	}
	recv = strings.TrimPrefix(recv, "*")
	if i := strings.IndexByte(recv, '['); i >= 0 {
		recv = recv[:i]
	}
	return strings.TrimSpace(recv)
}

func enclosing(defs []defSpan, start, end uint32) string {
	best := ""
	bestSize := ^uint32(0)
	for _, d := range defs {
		if d.start <= start && end <= d.end {
			if size := d.end - d.start; size < bestSize {
				bestSize = size
				best = d.qname
			}
		}
	}
	return best
}
