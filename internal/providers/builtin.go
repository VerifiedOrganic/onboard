package providers

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"

	ts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// Builtin is the pure-Go tree-sitter code-graph engine.
type Builtin struct{}

const maxIndexedFileBytes = 4 << 20

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
	calleeRecv  string
	allowBare   bool
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
	var hclEng *hclEngine
	hclEngKnown := false
	var reused, retagged int
	var skippedLarge []string

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
			// OpenTofu renamed Terraform's extensions; gotreesitter does not
			// register them, so alias them to the HCL grammar here.
			if ext := strings.ToLower(filepath.Ext(p)); ext == ".tofu" || ext == ".tofuvars" {
				entry = grammars.DetectLanguageByName("hcl")
			}
		}
		if entry == nil {
			return nil
		}
		info, statErr := d.Info()
		if statErr == nil && info.Size() > maxIndexedFileBytes {
			rel, _ := filepath.Rel(root, p)
			skippedLarge = append(skippedLarge, filepath.ToSlash(rel))
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
				perFile[rel] = fileData{
					lang:    pf.Lang,
					defs:    pf.Defs,
					refs:    fromDiskRefs(pf.Refs),
					imports: fromDiskImports(pf.Imports),
				}
				fresh.Files[rel] = pf
				reused++
				return nil
			}
		}

		if entry.Name == "svelte" {
			defs, refs := tagSvelteFile(rel, src, taggers)
			imports := parseJSImports(root, rel, src)
			perFile[rel] = fileData{lang: entry.Name, defs: defs, refs: refs, imports: imports}
			fresh.Files[rel] = diskFile{
				Hash:    h,
				Lang:    entry.Name,
				Defs:    defs,
				Refs:    toDiskRefs(refs),
				Imports: toDiskImports(imports),
			}
			retagged++
			return nil
		}

		if entry.Name == "hcl" {
			// Lock files are provider metadata, not architecture; the deps tool
			// reads them. Indexing them would pollute the graph with one
			// "provider" def per registry mirror entry.
			if base := filepath.Base(p); base == ".terraform.lock.hcl" || base == ".opentofu.lock.hcl" {
				return nil
			}
			if !hclEngKnown {
				hclEng = newHCLEngine()
				hclEngKnown = true
			}
			if hclEng == nil {
				return nil
			}
			defs, refs, imports := tagHCLFile(root, rel, src, hclEng)
			if len(defs) == 0 && len(refs) == 0 {
				return nil
			}
			perFile[rel] = fileData{lang: "hcl", defs: defs, refs: refs, imports: imports}
			fresh.Files[rel] = diskFile{
				Hash:    h,
				Lang:    "hcl",
				Defs:    defs,
				Refs:    toDiskRefs(refs),
				Imports: toDiskImports(imports),
			}
			retagged++
			return nil
		}

		if entry.Name == "html" {
			defs, refs := tagHTMLFile(rel, src)
			perFile[rel] = fileData{lang: entry.Name, defs: defs, refs: refs}
			fresh.Files[rel] = diskFile{
				Hash: h,
				Lang: entry.Name,
				Defs: defs,
				Refs: toDiskRefs(refs),
			}
			retagged++
			return nil
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
		var imports map[string]resolvedImport
		if entry.Name == "javascript" || entry.Name == "typescript" || entry.Name == "tsx" {
			imports = parseJSImports(root, rel, src)
		}
		perFile[rel] = fileData{lang: entry.Name, defs: defs, refs: refs, imports: imports}
		fresh.Files[rel] = diskFile{
			Hash:    h,
			Lang:    entry.Name,
			Defs:    defs,
			Refs:    toDiskRefs(refs),
			Imports: toDiskImports(imports),
		}
		retagged++
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	g := assembleGraph(perFile, root)
	g.reused, g.retagged = reused, retagged
	if len(skippedLarge) > 0 {
		g.Note = fmt.Sprintf("Skipped %d file(s) larger than %d MiB during indexing: %s", len(skippedLarge), maxIndexedFileBytes>>20, strings.Join(skippedLarge, ", "))
	}
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
			// The @definition.method capture spans the whole declaration, so the bytes
			// before the method name hold the receiver clause: "func (h *T) ".
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
			// Rust tree-sitter tags usually report associated functions as plain
			// definitions; qualify anything inside an impl/trait scope so maps and traces
			// distinguish Engine::new from free fn new.
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
	var refs []rawRef
	for _, t := range tags {
		if !strings.HasPrefix(t.Kind, "reference.") {
			continue
		}
		caller := enclosing(fileDefs, t.Range.StartByte, t.Range.EndByte)
		if caller == "" {
			caller = rel + "::(top-level)"
		}
		ref := rawRef{callerQName: caller, callerFile: rel, calleeName: t.Name, allowBare: true}
		switch lang {
		case "rust":
			ref.calleeRecv, ref.allowBare = rustRefHint(src, t.Range.StartByte, t.NameRange.StartByte, byQName[caller])
		case "javascript", "typescript", "tsx", "svelte":
			ref.calleeRecv, ref.allowBare = jsRefHint(src, t.Range.StartByte, t.NameRange.StartByte)
		}
		refs = append(refs, ref)
	}
	return defs, refs
}

// assembleGraph merges per-file defs/refs into a Graph and resolves each reference's
// callee name to a definition: prefer a same-file definition, else a globally unique
// one. Ambiguous names are left unresolved rather than guessed — guessing would
// manufacture false edges. Resolution is order-independent, so the result is the same
// whether files were freshly tagged or reused from cache.
func assembleGraph(perFile map[string]fileData, _ string) *Graph {
	g := &Graph{
		Provider: "builtin",
		Defs:     map[string]*Symbol{},
		Forward:  map[string][]string{},
		Reverse:  map[string][]string{},
	}
	defsByName := map[string][]string{}     // name -> qnames (global)
	defsByFileName := map[string][]string{} // file\x00name -> qnames (same-file resolution)
	defsByDirName := map[string][]string{}  // dir\x00name -> qnames (same-package/directory resolution)
	defsByRecvName := map[string][]string{}
	defsByFileRecvName := map[string][]string{}
	defsByDirRecvName := map[string][]string{}
	langSet := map[string]bool{}
	var refs []rawRef
	fileImports := make(map[string]map[string]resolvedImport)

	for file, fd := range perFile {
		langSet[fd.lang] = true
		g.Files++
		if len(fd.imports) > 0 {
			fileImports[file] = fd.imports
		}
		for _, sym := range fd.defs {
			if sym == nil {
				continue
			}
			g.Defs[sym.QName] = sym
			defsByName[sym.Name] = append(defsByName[sym.Name], sym.QName)
			defsByFileName[sym.File+"\x00"+sym.Name] = append(defsByFileName[sym.File+"\x00"+sym.Name], sym.QName)
			defsByDirName[dirOf(sym.File)+"\x00"+sym.Name] = append(defsByDirName[dirOf(sym.File)+"\x00"+sym.Name], sym.QName)
			if sym.Recv != "" {
				defsByRecvName[sym.Recv+"\x00"+sym.Name] = append(defsByRecvName[sym.Recv+"\x00"+sym.Name], sym.QName)
				defsByFileRecvName[sym.File+"\x00"+sym.Recv+"\x00"+sym.Name] = append(defsByFileRecvName[sym.File+"\x00"+sym.Recv+"\x00"+sym.Name], sym.QName)
				defsByDirRecvName[dirOf(sym.File)+"\x00"+sym.Recv+"\x00"+sym.Name] = append(defsByDirRecvName[dirOf(sym.File)+"\x00"+sym.Recv+"\x00"+sym.Name], sym.QName)
			}
		}
		refs = append(refs, fd.refs...)
	}

	edges := newGraphEdgeSet()
	for _, r := range refs {
		callee := r.lookup(defsByFileName, defsByDirName, defsByName, defsByFileRecvName, defsByDirRecvName, defsByRecvName, fileImports)
		if callee == "" || callee == r.callerQName {
			if callee == "" {
				g.Unresolved++
			}
			continue
		}
		edges.add(g, r.callerQName, callee)
	}

	g.Langs = sortedKeys(langSet)
	if g.Files == 0 {
		g.Note = "No files matched a supported grammar with a tags query."
	} else {
		g.Note = "Call edges are syntactic (name + lexical scope), not type-checked; treat as likely, not proven."
	}
	return g
}

func (r rawRef) lookup(byFileName, byDirName, byName, byFileRecvName, byDirRecvName, byRecvName map[string][]string, fileImports map[string]map[string]resolvedImport) string {
	// 0. HCL (Terraform/Terragrunt) uses its own scoping: same file, then same
	// directory (module), plus explicit cross-module targets — never global
	// by-name (which would link same-named variables across modules).
	if isHCLFile(r.callerFile) {
		return r.lookupHCL(byFileName, byDirName, fileImports)
	}

	// 1. Check template associated file resolution (Angular component template matching)
	if strings.HasSuffix(r.callerFile, ".html") {
		assoc := strings.TrimSuffix(r.callerFile, ".html") + ".ts"
		if cands := byFileName[assoc+"\x00"+r.calleeName]; len(cands) == 1 {
			return cands[0]
		}
	}

	// 2. Resolve imports / aliases
	if imports, ok := fileImports[r.callerFile]; ok {
		if imp, ok := imports[r.calleeName]; ok {
			switch imp.targetName {
			case "default":
				if cands := byFileName[imp.targetFile+"\x00default"]; len(cands) == 1 {
					return cands[0]
				}
				if cands := byFileName[imp.targetFile+"\x00"+r.calleeName]; len(cands) == 1 {
					return cands[0]
				}
				baseName := strings.TrimSuffix(filepath.Base(imp.targetFile), filepath.Ext(imp.targetFile))
				if cands := byFileName[imp.targetFile+"\x00"+baseName]; len(cands) == 1 {
					return cands[0]
				}
				if sole := soleDefinitionInFile(imp.targetFile, byFileName); sole != "" {
					return sole
				}
			case "*":
				return ""
			default:
				if cands := byFileName[imp.targetFile+"\x00"+imp.targetName]; len(cands) == 1 {
					return cands[0]
				}
			}
		}
	}

	// 3. Resolve receivers (method calls)
	if r.calleeRecv != "" {
		if imports, ok := fileImports[r.callerFile]; ok {
			if imp, ok := imports[r.calleeRecv]; ok {
				if cands := byFileName[imp.targetFile+"\x00"+r.calleeName]; len(cands) == 1 {
					return cands[0]
				}
			}
		}

		if q := lookupRecv(r.callerFile, r.calleeRecv, r.calleeName, byFileRecvName, byDirRecvName, byRecvName); q != "" {
			return q
		}
		if left, _, ok := strings.Cut(r.calleeRecv, " as "); ok && left != "" {
			if q := lookupRecv(r.callerFile, left, r.calleeName, byFileRecvName, byDirRecvName, byRecvName); q != "" {
				return q
			}
		}
	}

	if !r.allowBare {
		return ""
	}
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

func soleDefinitionInFile(file string, byFileName map[string][]string) string {
	prefix := file + "\x00"
	var sole string
	count := 0
	for k, cands := range byFileName {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		for _, q := range cands {
			count++
			if count > 1 {
				return ""
			}
			sole = q
		}
	}
	return sole
}

func lookupRecv(file, recv, name string, byFileRecvName, byDirRecvName, byRecvName map[string][]string) string {
	if cands := byFileRecvName[file+"\x00"+recv+"\x00"+name]; len(cands) == 1 {
		return cands[0]
	}
	if cands := byDirRecvName[dirOf(file)+"\x00"+recv+"\x00"+name]; len(cands) == 1 {
		return cands[0]
	}
	if cands := byRecvName[recv+"\x00"+name]; len(cands) == 1 {
		return cands[0]
	}

	// Case fallback for JS/TS: lowercase variable receiver -> uppercase class/receiver type
	if len(recv) > 0 && unicode.IsLower(rune(recv[0])) {
		capitalized := string(unicode.ToUpper(rune(recv[0]))) + recv[1:]
		if cands := byFileRecvName[file+"\x00"+capitalized+"\x00"+name]; len(cands) == 1 {
			return cands[0]
		}
		if cands := byDirRecvName[dirOf(file)+"\x00"+capitalized+"\x00"+name]; len(cands) == 1 {
			return cands[0]
		}
		if cands := byRecvName[capitalized+"\x00"+name]; len(cands) == 1 {
			return cands[0]
		}
	}

	return ""
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

// langTagsOverrides maps language names to explicit tags queries that replace the
// generic inferred query. Use this when a language's inferred query misses important
// patterns (e.g. Rust method calls via field_expression).
var langTagsOverrides = map[string]string{
	"rust":       rustTagsQuery,
	"javascript": jsTagsQuery,
	"typescript": tsTagsQuery,
	"tsx":        tsxTagsQuery,
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
	if q, ok := langTagsOverrides[entry.Name]; ok {
		if t, err := ts.NewTagger(lang, q); err == nil {
			return t
		}
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
