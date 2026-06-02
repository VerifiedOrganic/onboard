package providers

import (
	"context"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	ts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// Builtin is the pure-Go tree-sitter code-graph engine.
type Builtin struct{}

// Name returns the provider identifier.
func (Builtin) Name() string { return "builtin" }

// defSpan records a definition's byte span so references can be attributed to the
// innermost enclosing definition (the caller).
type defSpan struct {
	qname      string
	start, end uint32
}

type rawRef struct {
	callerQName string
	callerFile  string
	calleeName  string
}

// Index walks root and builds a syntactic call graph via tree-sitter tags, parsing
// every file. IndexWithCache is the incremental variant.
func (Builtin) Index(ctx context.Context, root string) (*Graph, error) {
	return indexBuiltin(ctx, root, "")
}

// IndexWithCache is Index plus a persistent, content-hashed per-file cache at cachePath.
// A file whose contents are unchanged since the last run reuses its cached tags instead
// of being re-parsed (parsing dominates indexing cost); changed and new files are
// re-tagged, deleted files drop out, and the full reference set is re-resolved every
// time so the graph stays correct. A missing, unreadable, or stale-version cache is
// ignored (full rebuild); cache write failures are swallowed. cachePath == "" disables
// persistence (identical to Index).
func (Builtin) IndexWithCache(ctx context.Context, root, cachePath string) (*Graph, error) {
	return indexBuiltin(ctx, root, cachePath)
}

func indexBuiltin(ctx context.Context, root, cachePath string) (*Graph, error) {
	root, err := normalizeRoot(root)
	if err != nil {
		return nil, err
	}

	var prev *diskIndex
	if cachePath != "" {
		prev = loadDiskIndex(cachePath)
	}
	fresh := &diskIndex{Version: cacheVersion, Files: map[string]diskFile{}}
	perFile := map[string]fileData{}

	// One tagger per language (decompressing a grammar + compiling its tags query is
	// expensive; reuse across files). A nil entry means "unsupported, skip".
	taggers := map[string]*ts.Tagger{}
	var reused, retagged int

	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if p != root && skipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}

		entry := grammars.DetectLanguage(p)
		if entry == nil {
			return nil
		}
		src, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		h := hashBytes(src)

		// Reuse an unchanged file verbatim — skipping the expensive parse/tag step.
		if prev != nil {
			if pf, ok := prev.Files[rel]; ok && pf.Hash == h {
				perFile[rel] = fileData{lang: pf.Lang, defs: pf.Defs, refs: fromDiskRefs(pf.Refs)}
				fresh.Files[rel] = pf
				reused++
				return nil
			}
		}

		tagger, known := taggers[entry.Name]
		if !known {
			tagger = buildTagger(entry)
			taggers[entry.Name] = tagger
		}
		if tagger == nil {
			return nil // language present but no usable tags query
		}
		tags := safeTag(tagger, src)
		if len(tags) == 0 {
			return nil
		}
		defs, refs := tagFile(rel, entry.Name, src, tags)
		perFile[rel] = fileData{lang: entry.Name, defs: defs, refs: refs}
		fresh.Files[rel] = diskFile{Hash: h, Lang: entry.Name, Defs: defs, Refs: toDiskRefs(refs)}
		retagged++
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	g := assembleGraph(perFile)
	g.reused, g.retagged = reused, retagged
	if cachePath != "" {
		saveDiskIndex(cachePath, fresh)
	}
	return g, nil
}

// tagFile extracts a file's definitions and raw call references from its tree-sitter
// tags. QNames are made unique within the file, which makes them globally unique because
// the file path is part of the QName — so a file can be tagged in isolation and cached.
func tagFile(rel, lang string, src []byte, tags []ts.Tag) ([]*Symbol, []rawRef) {
	local := map[string]*Symbol{}
	var defs []*Symbol
	var fileDefs []defSpan
	for _, t := range tags {
		if !strings.HasPrefix(t.Kind, "definition.") {
			continue
		}
		kind := strings.TrimPrefix(t.Kind, "definition.")
		line := int(t.NameRange.StartPoint.Row) + 1
		qn := uniqueQName(local, rel, t.Name, line)
		sym := &Symbol{QName: qn, Name: t.Name, Kind: kind, File: rel, Line: line, Lang: lang}
		if kind == "method" && lang == "go" {
			// The @definition.method capture spans the whole declaration, so the bytes
			// before the method name hold the receiver clause: "func (h *T) ".
			sym.Recv = goReceiverType(src, t.Range.StartByte, t.NameRange.StartByte)
		}
		local[qn] = sym
		defs = append(defs, sym)
		fileDefs = append(fileDefs, defSpan{qname: qn, start: t.Range.StartByte, end: t.Range.EndByte})
	}
	var refs []rawRef
	for _, t := range tags {
		if !strings.HasPrefix(t.Kind, "reference.") {
			continue
		}
		caller := enclosing(fileDefs, t.Range.StartByte, t.Range.EndByte)
		if caller == "" {
			caller = rel + "::(top-level)"
		}
		refs = append(refs, rawRef{callerQName: caller, callerFile: rel, calleeName: t.Name})
	}
	return defs, refs
}

// assembleGraph merges per-file defs/refs into a Graph and resolves each reference's
// callee name to a definition: prefer a same-file definition, else a globally unique
// one. Ambiguous names are left unresolved rather than guessed — guessing would
// manufacture false edges. Resolution is order-independent, so the result is the same
// whether files were freshly tagged or reused from cache.
func assembleGraph(perFile map[string]fileData) *Graph {
	g := &Graph{
		Provider: "builtin",
		Defs:     map[string]*Symbol{},
		Forward:  map[string][]string{},
		Reverse:  map[string][]string{},
	}
	defsByName := map[string][]string{}     // name -> qnames (global)
	defsByFileName := map[string][]string{} // file\x00name -> qnames (same-file resolution)
	defsByDirName := map[string][]string{}  // dir\x00name -> qnames (same-package/directory resolution)
	langSet := map[string]bool{}
	var refs []rawRef

	for _, fd := range perFile {
		langSet[fd.lang] = true
		g.Files++
		for _, sym := range fd.defs {
			g.Defs[sym.QName] = sym
			defsByName[sym.Name] = append(defsByName[sym.Name], sym.QName)
			defsByFileName[sym.File+"\x00"+sym.Name] = append(defsByFileName[sym.File+"\x00"+sym.Name], sym.QName)
			defsByDirName[dirOf(sym.File)+"\x00"+sym.Name] = append(defsByDirName[dirOf(sym.File)+"\x00"+sym.Name], sym.QName)
		}
		refs = append(refs, fd.refs...)
	}

	for _, r := range refs {
		callee := r.lookup(defsByFileName, defsByDirName, defsByName)
		if callee == "" || callee == r.callerQName {
			if callee == "" {
				g.Unresolved++
			}
			continue
		}
		g.Forward[r.callerQName] = appendUnique(g.Forward[r.callerQName], callee)
		g.Reverse[callee] = appendUnique(g.Reverse[callee], r.callerQName)
	}

	g.Langs = sortedKeys(langSet)
	if g.Files == 0 {
		g.Note = "No files matched a supported grammar with a tags query."
	} else {
		g.Note = "Call edges are syntactic (name + lexical scope), not type-checked; treat as likely, not proven."
	}
	return g
}

func (r rawRef) lookup(byFileName, byDirName, byName map[string][]string) string {
	// Resolve at the narrowest scope where the name is unambiguous, widening only when a
	// scope yields no single match. At every tier the rule is identical: resolve ONLY when
	// exactly one candidate exists, so an ambiguous name is left unresolved rather than
	// mis-attributed to whichever same-named symbol happened to be defined last.
	//
	//  1. Same file      — the tightest lexical scope.
	//  2. Same directory  — the package scope. In Go a top-level name is unique per package,
	//     so a name that collides across the repo is usually unique within its own directory;
	//     this tier recovers the many method/function calls (Render, Name, Run, ...) that the
	//     name-only global check would otherwise drop as ambiguous. The len==1 guard keeps it
	//     honest: two same-named methods in one package stay unresolved, never guessed.
	//  3. Whole repo      — a globally unique name.
	if cands := byFileName[r.callerFile+"\x00"+r.calleeName]; len(cands) == 1 {
		return cands[0]
	}
	if cands := byDirName[dirOf(r.callerFile)+"\x00"+r.calleeName]; len(cands) == 1 {
		return cands[0]
	}
	if cands := byName[r.calleeName]; len(cands) == 1 {
		return cands[0]
	}
	return "" // ambiguous or unknown
}

// goReceiverType extracts the receiver type name from a Go method declaration header —
// the source between the "func" keyword and the method name. It strips the optional
// receiver variable, a leading pointer, and any generic type parameters:
//
//	"func (h *HTMLRenderer) "  -> "HTMLRenderer"
//	"func (Tree[K, V]) "       -> "Tree"
//	"func (s server) "         -> "server"
//
// Returns "" if it cannot find a receiver (defensive — the caller treats "" as "no
// qualification available"). Bounds are validated so a malformed span never panics.
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
	// Drop the receiver variable name, if present ("h *T" -> "*T"); an anonymous
	// receiver ("*T" or "T") has no leading space and is kept whole.
	if sp := strings.IndexAny(recv, " \t"); sp >= 0 {
		recv = strings.TrimSpace(recv[sp+1:])
	}
	recv = strings.TrimPrefix(recv, "*")
	if i := strings.IndexByte(recv, '['); i >= 0 { // strip generic type params
		recv = recv[:i]
	}
	return strings.TrimSpace(recv)
}

// dirOf returns the slash directory of a repo-relative file path, or "" for a top-level
// file. Paths are stored slash-separated (filepath.ToSlash at tag time), so path.Dir is
// correct on every OS where filepath.Dir would not be.
func dirOf(file string) string {
	if d := path.Dir(file); d != "." {
		return d
	}
	return ""
}

// safeTag runs the tagger, swallowing any input-dependent panic from a grammar so
// one pathological source file cannot abort indexing of the whole repo.
func safeTag(tg *ts.Tagger, src []byte) (tags []ts.Tag) {
	defer func() {
		if recover() != nil {
			tags = nil
		}
	}()
	return tg.Tag(src)
}

// buildTagger constructs a Tagger for a language, or nil if it has no tags query
// or fails to load. Failures are swallowed so one bad grammar can't abort indexing.
func buildTagger(entry *grammars.LangEntry) (tg *ts.Tagger) {
	defer func() {
		if recover() != nil {
			tg = nil
		}
	}()
	lang := entry.Language()
	if lang == nil {
		return nil
	}
	tagsQuery := grammars.ResolveTagsQuery(*entry)
	if strings.TrimSpace(tagsQuery) == "" {
		return nil
	}
	t, err := ts.NewTagger(lang, tagsQuery)
	if err != nil {
		return nil
	}
	return t
}

// enclosing returns the qname of the smallest definition whose byte span contains
// [start,end), or "" if the reference is at file scope.
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
