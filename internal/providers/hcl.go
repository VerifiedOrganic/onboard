package providers

// HCL (Terraform / Terragrunt / OpenTofu) support.
//
// The embedded grammar set ships a full HCL grammar mapped to .hcl/.tf/.tfvars,
// but the generic inferred tags query knows nothing about HCL's node shapes
// (block/attribute/variable_expr), so without this file a Terraform repo indexes
// to zero symbols. tagHCLFile walks the parse tree directly — like the Svelte and
// HTML taggers — because definitions live in block *labels* (string literals),
// which tags queries cannot capture cleanly.
//
// The graph model maps Terraform onto the existing Symbol/Graph types:
//
//   - A directory is a module: all .tf files in one dir share an evaluation
//     scope, so dir-scoped name resolution (byDirName) is exactly Terraform's
//     own rule for var./local. references.
//   - Definitions: variable/output/locals entries, module calls, resources,
//     data sources, providers, Terragrunt stacks (terragrunt.hcl) and config
//     layers (root.hcl, env.hcl, ...). Names carry the Terraform address
//     ("var.nodes", "module.inventory", "output.api_endpoint") so references
//     resolve by exact name within their scope.
//   - References: var.x / local.x (dir-scoped), module.N (dir-scoped) plus the
//     cross-module hop module.N.out -> target module's output.out, module-call
//     arguments -> target module's variables, and Terragrunt include /
//     dependency / terraform.source / inputs wiring.
//
// Cross-module targets are recorded per file in the existing resolvedImport
// mechanism (the same channel the JS provider uses for ESM imports), so they
// survive the per-file disk cache round-trip. Resolution never falls back to a
// global by-name match: a var.x that does not resolve in its own module is
// broken Terraform, and linking it to another module's var.x would manufacture
// a false edge. Unresolved HCL refs are counted in Graph.Unresolved like any
// other language's.
//
// Known blind spots (documented, not silent): registry/git module sources are
// external dependencies (no edge; the deps tool surfaces them), dynamic
// for_each module keys and read_terragrunt_config indirection are not followed,
// and generate-block heredoc bodies are treated as opaque strings.

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	ts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// hclEngine bundles the parser with its language so node types can be decoded.
// One engine is built lazily per index run and reused across files (grammar
// decompression is expensive).
type hclEngine struct {
	parser *ts.Parser
	lang   *ts.Language
}

func newHCLEngine() *hclEngine {
	entry := grammars.DetectLanguageByName("hcl")
	if entry == nil {
		return nil
	}
	lang := entry.Language()
	if lang == nil {
		return nil
	}
	return &hclEngine{parser: ts.NewParser(lang), lang: lang}
}

// hclExts are the file extensions resolved with HCL scoping rules. .tofu and
// .tofuvars are OpenTofu's renamed extensions; gotreesitter does not register
// them, so detection is aliased in indexBuiltin.
var hclExts = map[string]bool{
	".tf":       true,
	".tfvars":   true,
	".hcl":      true,
	".tofu":     true,
	".tofuvars": true,
}

func isHCLFile(file string) bool {
	return hclExts[strings.ToLower(path.Ext(file))]
}

// hclBuiltinPrefixes are expression roots that are Terraform/Terragrunt keywords,
// not user symbols. var/local/module/data/dependency are handled specially.
var hclBuiltinRoots = map[string]bool{
	"each": true, "count": true, "self": true, "path": true,
	"terraform": true, "terragrunt": true, "include": true,
}

// hclModuleMetaArgs are module-call arguments that are meta-arguments, not
// variables of the target module.
var hclModuleMetaArgs = map[string]bool{
	"source": true, "version": true, "count": true, "for_each": true,
	"providers": true, "depends_on": true,
}

// tgConfigBlocks identify a non-terragrunt.hcl *.hcl file as a Terragrunt
// configuration layer (root.hcl / env.hcl / region.hcl ...).
var tgConfigBlocks = map[string]bool{
	"remote_state": true, "generate": true, "include": true, "dependency": true,
	"dependencies": true, "terraform": true, "inputs": true, "locals": true,
}

// safeParseHCL parses src, swallowing any input-dependent panic so one
// pathological file cannot abort indexing (same contract as safeTag).
func (e *hclEngine) safeParse(src []byte) (tree *ts.Tree) {
	defer func() {
		if recover() != nil {
			tree = nil
		}
	}()
	t, err := e.parser.Parse(src)
	if err != nil {
		return nil
	}
	return t
}

// tagHCLFile extracts definitions, raw references, and cross-module import
// targets from one HCL file. root is the absolute repo root (used to resolve
// module sources and find_in_parent_folders against the filesystem); rel is the
// slash-separated repo-relative path.
func tagHCLFile(root, rel string, src []byte, eng *hclEngine) ([]*Symbol, []rawRef, map[string]resolvedImport) {
	tree := eng.safeParse(src)
	if tree == nil {
		return nil, nil, nil
	}
	defer tree.Release()

	w := &hclWalker{
		root:    root,
		rel:     rel,
		src:     src,
		lang:    eng.lang,
		local:   map[string]*Symbol{},
		imports: map[string]resolvedImport{},
	}

	rootNode := tree.RootNode()
	body := firstNamedChildOfType(rootNode, w.lang, "body")
	if body == nil {
		return nil, nil, nil
	}

	base := path.Base(rel)
	isTerragrunt := base == "terragrunt.hcl"
	isLayer := !isTerragrunt && strings.HasSuffix(base, ".hcl") && hasTerragruntShape(body, w.lang, src)
	if isTerragrunt || isLayer {
		w.fileDef = w.addFileDef(rootNode, base, isTerragrunt)
	}

	for _, child := range namedChildren(body) {
		switch child.Type(w.lang) {
		case "block":
			w.topBlock(child)
		case "attribute":
			w.topAttribute(child)
		}
	}

	// References are collected after all defs so enclosing() sees every span.
	w.collectRefs(body)

	if len(w.imports) == 0 {
		w.imports = nil
	}
	return w.defs, w.refs, w.imports
}

type hclWalker struct {
	root    string
	rel     string
	src     []byte
	lang    *ts.Language
	local   map[string]*Symbol
	defs    []*Symbol
	spans   []defSpan
	refs    []rawRef
	imports map[string]resolvedImport
	fileDef *Symbol // terragrunt stack/config-layer symbol spanning the file
}

// addDef registers a definition and its byte span (for caller attribution).
func (w *hclWalker) addDef(name, kind string, n *ts.Node, public bool) *Symbol {
	line := int(n.StartPoint().Row) + 1
	qn := uniqueQName(w.local, w.rel, name, line)
	sym := &Symbol{
		QName:  qn,
		Name:   name,
		Kind:   kind,
		File:   w.rel,
		Line:   line,
		Column: int(n.StartPoint().Column),
		Lang:   "hcl",
		Public: public,
	}
	w.local[qn] = sym
	w.defs = append(w.defs, sym)
	w.spans = append(w.spans, defSpan{qname: qn, start: n.StartByte(), end: n.EndByte()})
	return sym
}

// addFileDef creates the Terragrunt stack/config-layer symbol that spans the
// whole file, so top-level attributes (inputs = {...}) attribute to it.
func (w *hclWalker) addFileDef(rootNode *ts.Node, base string, isStack bool) *Symbol {
	if isStack {
		name := path.Base(path.Dir(w.rel))
		if name == "." || name == "/" || name == "" {
			name = "stack"
		}
		return w.addDef(name, "stack", rootNode, true)
	}
	return w.addDef(strings.TrimSuffix(base, ".hcl"), "config", rootNode, true)
}

// topBlock handles a top-level HCL block: definitions plus any cross-module
// source recording.
func (w *hclWalker) topBlock(block *ts.Node) {
	ident, labels := blockHeader(block, w.lang, w.src)
	switch ident {
	case "variable":
		if len(labels) >= 1 {
			w.addDef("var."+labels[0], "variable", block, true)
		}
	case "output":
		if len(labels) >= 1 {
			w.addDef("output."+labels[0], "output", block, true)
		}
	case "locals":
		if body := firstNamedChildOfType(block, w.lang, "body"); body != nil {
			for _, attr := range namedChildren(body) {
				if attr.Type(w.lang) != "attribute" {
					continue
				}
				if name := attributeName(attr, w.lang, w.src); name != "" {
					w.addDef("local."+name, "local", attr, false)
				}
			}
		}
	case "module":
		if len(labels) >= 1 {
			name := labels[0]
			modDef := w.addDef("module."+name, "module_call", block, false)
			if src := blockStringAttr(block, w.lang, w.src, "source"); src != "" {
				if dir, ok := resolveHCLSourceDir(w.root, w.rel, src); ok {
					w.imports["module."+name] = resolvedImport{targetFile: dir, targetName: "dir"}
				}
			}
			if body := firstNamedChildOfType(block, w.lang, "body"); body != nil {
				for _, attr := range namedChildren(body) {
					if attr.Type(w.lang) != "attribute" {
						continue
					}
					key := attributeName(attr, w.lang, w.src)
					if key == "" || hclModuleMetaArgs[key] {
						continue
					}
					w.refs = append(w.refs, rawRef{
						callerQName: modDef.QName,
						callerFile:  w.rel,
						calleeName:  "var." + key,
						calleeRecv:  "hcl-module:" + name,
					})
				}
			}
		}
	case "resource":
		if len(labels) >= 2 {
			w.addDef(labels[0]+"."+labels[1], "resource", block, false)
		}
	case "data":
		if len(labels) >= 2 {
			w.addDef("data."+labels[0]+"."+labels[1], "data", block, false)
		}
	case "provider":
		if len(labels) >= 1 {
			w.addDef("provider."+labels[0], "provider", block, false)
		}
	case "dependency":
		if len(labels) >= 1 && w.fileDef != nil {
			dep := w.addDef("dependency."+labels[0], "dependency", block, false)
			if cfg := blockStringAttr(block, w.lang, w.src, "config_path"); cfg != "" {
				if dir, ok := resolveHCLSourceDir(w.root, w.rel, cfg); ok {
					target := path.Join(dir, "terragrunt.hcl")
					stackName := path.Base(dir)
					if stackName == "." || stackName == "" {
						stackName = "stack"
					}
					key := "dependency." + labels[0]
					w.imports[key] = resolvedImport{targetFile: target, targetName: stackName}
					w.refs = append(w.refs, rawRef{
						callerQName: dep.QName,
						callerFile:  w.rel,
						calleeRecv:  "hcl-import:" + key,
					})
				}
			}
		}
	case "include":
		if w.fileDef == nil {
			return
		}
		name := "root"
		if len(labels) >= 1 {
			name = labels[0]
		}
		if target := w.resolveIncludePath(block); target != "" {
			key := "include." + name
			w.imports[key] = resolvedImport{
				targetFile: target,
				targetName: strings.TrimSuffix(path.Base(target), ".hcl"),
			}
			w.refs = append(w.refs, rawRef{
				callerQName: w.fileDef.QName,
				callerFile:  w.rel,
				calleeRecv:  "hcl-import:" + key,
			})
		}
	case "terraform":
		// In a Terragrunt file, terraform { source = ... } points the stack at
		// its module. In plain .tf this block holds settings — no definition.
		if w.fileDef == nil {
			return
		}
		if src := blockStringAttr(block, w.lang, w.src, "source"); src != "" {
			if dir, ok := resolveHCLSourceDir(w.root, w.rel, src); ok {
				w.imports["terraform.source"] = resolvedImport{targetFile: dir, targetName: "dir"}
			}
		}
	}
}

// topAttribute handles top-level attributes. In Terragrunt files, the keys of
// inputs = {...} are variables of the sourced module. In .tfvars files every
// top-level attribute assigns a variable of the enclosing directory's module.
func (w *hclWalker) topAttribute(attr *ts.Node) {
	name := attributeName(attr, w.lang, w.src)
	if name == "" {
		return
	}
	ext := strings.ToLower(path.Ext(w.rel))
	if ext == ".tfvars" || ext == ".tofuvars" {
		w.refs = append(w.refs, rawRef{
			callerQName: w.rel + "::(top-level)",
			callerFile:  w.rel,
			calleeName:  "var." + name,
		})
		return
	}
	if name == "inputs" && w.fileDef != nil {
		caller := w.fileDef.QName
		for _, key := range objectKeys(attr, w.lang, w.src) {
			w.refs = append(w.refs, rawRef{
				callerQName: caller,
				callerFile:  w.rel,
				calleeName:  "var." + key,
				calleeRecv:  "hcl-tgsource",
			})
		}
	}
}

// resolveIncludePath extracts the include block's path expression: either
// find_in_parent_folders("name.hcl") (searched upward from the parent dir,
// per Terragrunt semantics) or a literal relative path.
func (w *hclWalker) resolveIncludePath(block *ts.Node) string {
	body := firstNamedChildOfType(block, w.lang, "body")
	if body == nil {
		return ""
	}
	for _, attr := range namedChildren(body) {
		if attr.Type(w.lang) != "attribute" || attributeName(attr, w.lang, w.src) != "path" {
			continue
		}
		if fn := findDescendantOfType(attr, w.lang, "function_call"); fn != nil {
			fnName := ""
			if id := firstNamedChildOfType(fn, w.lang, "identifier"); id != nil {
				fnName = id.Text(w.src)
			}
			if fnName != "find_in_parent_folders" {
				return ""
			}
			arg := ""
			if lit := findDescendantOfType(fn, w.lang, "template_literal"); lit != nil {
				arg = lit.Text(w.src)
			}
			if arg == "" {
				// Bare find_in_parent_folders(): legacy default is the nearest
				// parent terragrunt.hcl; newer setups use root.hcl.
				if t := findInParentFolders(w.root, w.rel, "root.hcl"); t != "" {
					return t
				}
				return findInParentFolders(w.root, w.rel, "terragrunt.hcl")
			}
			return findInParentFolders(w.root, w.rel, arg)
		}
		if lit := findDescendantOfType(attr, w.lang, "template_literal"); lit != nil {
			if dir, ok := resolveHCLSourceDir(w.root, w.rel, lit.Text(w.src)); ok || dir != "" {
				return dir
			}
			joined := path.Clean(path.Join(path.Dir(w.rel), lit.Text(w.src)))
			if !strings.HasPrefix(joined, "..") {
				return joined
			}
		}
		return ""
	}
	return ""
}

// collectRefs walks the whole tree once, turning variable_expr traversal chains
// (var.x, local.x, module.n.out, data.t.n.attr, dependency.x.outputs.y) into
// raw references attributed to the smallest enclosing definition.
func (w *hclWalker) collectRefs(n *ts.Node) {
	if n.Type(w.lang) == "variable_expr" {
		w.refFromTraversal(n)
	}
	for _, c := range namedChildren(n) {
		w.collectRefs(c)
	}
}

func (w *hclWalker) refFromTraversal(varExpr *ts.Node) {
	id := firstNamedChildOfType(varExpr, w.lang, "identifier")
	if id == nil {
		return
	}
	rootName := id.Text(w.src)
	if hclBuiltinRoots[rootName] {
		return
	}

	// Collect the get_attr chain following the variable_expr.
	var chain []string
	for sib := varExpr.NextSibling(); sib != nil; sib = sib.NextSibling() {
		if sib.Type(w.lang) != "get_attr" {
			break
		}
		if cid := firstNamedChildOfType(sib, w.lang, "identifier"); cid != nil {
			chain = append(chain, cid.Text(w.src))
		}
	}

	start, end := varExpr.StartByte(), varExpr.EndByte()
	caller := enclosing(w.spans, start, end)
	if caller == "" {
		caller = w.rel + "::(top-level)"
	}
	add := func(name, recv string) {
		w.refs = append(w.refs, rawRef{
			callerQName: caller,
			callerFile:  w.rel,
			calleeName:  name,
			calleeRecv:  recv,
		})
	}

	switch rootName {
	case "var":
		if len(chain) >= 1 {
			// Inside a module-call block, bare expressions still reference the
			// *caller's* scope (module arguments are expressions evaluated in
			// the calling module), so var.x resolves in this directory.
			add("var."+chain[0], "")
		}
	case "local":
		if len(chain) >= 1 {
			add("local."+chain[0], "")
		}
	case "module":
		if len(chain) >= 1 {
			add("module."+chain[0], "")
			if len(chain) >= 2 {
				add("output."+chain[1], "hcl-module:"+chain[0])
			}
		}
	case "data":
		if len(chain) >= 2 {
			add("data."+chain[0]+"."+chain[1], "")
		}
	case "dependency":
		if len(chain) >= 1 {
			add("dependency."+chain[0], "")
		}
	default:
		// A bare resource reference: aws_instance.web, redfish_power.on.
		// Only meaningful with at least one attribute step and a plausible
		// resource-type name; bare identifiers (function args, for iterators,
		// type exprs) are skipped.
		if len(chain) >= 1 && strings.Contains(rootName, "_") {
			add(rootName+"."+chain[0], "")
		}
	}
}

// lookupHCL resolves an HCL reference. Scoping is Terraform's own: same file,
// then same directory (module). There is deliberately no global by-name
// fallback — see the package comment.
func (r rawRef) lookupHCL(byFileName, byDirName map[string][]string, fileImports map[string]map[string]resolvedImport) string {
	imports := fileImports[r.callerFile]
	switch {
	case strings.HasPrefix(r.calleeRecv, "hcl-import:"):
		imp, ok := imports[strings.TrimPrefix(r.calleeRecv, "hcl-import:")]
		if !ok {
			return ""
		}
		if cands := byFileName[imp.targetFile+"\x00"+imp.targetName]; len(cands) == 1 {
			return cands[0]
		}
		return ""
	case strings.HasPrefix(r.calleeRecv, "hcl-module:"):
		imp, ok := imports["module."+strings.TrimPrefix(r.calleeRecv, "hcl-module:")]
		if !ok {
			return ""
		}
		if cands := byDirName[imp.targetFile+"\x00"+r.calleeName]; len(cands) == 1 {
			return cands[0]
		}
		return ""
	case r.calleeRecv == "hcl-tgsource":
		imp, ok := imports["terraform.source"]
		if !ok {
			return ""
		}
		if cands := byDirName[imp.targetFile+"\x00"+r.calleeName]; len(cands) == 1 {
			return cands[0]
		}
		return ""
	}
	if cands := byFileName[r.callerFile+"\x00"+r.calleeName]; len(cands) == 1 {
		return cands[0]
	}
	if cands := byDirName[dirOf(r.callerFile)+"\x00"+r.calleeName]; len(cands) == 1 {
		return cands[0]
	}
	return ""
}

// resolveHCLSourceDir resolves a module/config source string to a repo-relative
// directory. Returns ok=false for external sources (registry, git) and for
// interpolations it cannot evaluate — those are dependencies, not graph edges.
func resolveHCLSourceDir(root, fromRel, src string) (string, bool) {
	s := strings.TrimSpace(src)
	if s == "" {
		return "", false
	}
	if strings.Contains(s, "${get_repo_root()}") {
		s = strings.ReplaceAll(s, "${get_repo_root()}", "")
		s = strings.ReplaceAll(s, "//", "/")
		s = strings.Trim(s, "/")
		return cleanRepoRelDir(root, s)
	}
	if strings.Contains(s, "${") {
		return "", false // unsupported interpolation (path_relative_*, locals, ...)
	}
	if strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") || s == "." || s == ".." {
		s = strings.ReplaceAll(s, "//", "/")
		return cleanRepoRelDir(root, path.Join(path.Dir(fromRel), s))
	}
	return "", false // registry or git source: external dependency
}

// cleanRepoRelDir normalizes a candidate repo-relative dir and verifies it
// exists inside the repo. "" (repo root) is valid.
func cleanRepoRelDir(root, dir string) (string, bool) {
	dir = path.Clean(dir)
	if dir == "." {
		dir = ""
	}
	if strings.HasPrefix(dir, "..") {
		return "", false
	}
	if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir))); err != nil || !info.IsDir() {
		return "", false
	}
	return dir, true
}

// findInParentFolders mirrors Terragrunt's find_in_parent_folders: search for
// name in the file's parent directories (not its own), stopping at repo root.
func findInParentFolders(root, fromRel, name string) string {
	dir := path.Dir(fromRel)
	for dir != "." && dir != "/" {
		dir = path.Dir(dir)
		cand := name
		if dir != "." {
			cand = path.Join(dir, name)
		}
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(cand))); err == nil && !info.IsDir() {
			return cand
		}
		if dir == "." {
			break
		}
	}
	return ""
}

// hasTerragruntShape reports whether a top-level body looks like a Terragrunt
// configuration layer rather than arbitrary HCL (Packer, Nomad, ...): it must
// contain at least one Terragrunt-typical block or an inputs attribute.
func hasTerragruntShape(body *ts.Node, lang *ts.Language, src []byte) bool {
	for _, child := range namedChildren(body) {
		switch child.Type(lang) {
		case "block":
			if id := firstNamedChildOfType(child, lang, "identifier"); id != nil && tgConfigBlocks[id.Text(src)] {
				return true
			}
		case "attribute":
			if name := attributeName(child, lang, src); name == "inputs" || name == "skip" {
				return true
			}
		}
	}
	return false
}

// blockHeader returns a block's type identifier and its string labels.
func blockHeader(block *ts.Node, lang *ts.Language, src []byte) (string, []string) {
	var ident string
	var labels []string
	for _, c := range namedChildren(block) {
		switch c.Type(lang) {
		case "identifier":
			if ident == "" {
				ident = c.Text(src)
			}
		case "string_lit":
			labels = append(labels, stringLitText(c, src))
		case "block_start", "body":
			return ident, labels
		}
	}
	return ident, labels
}

// attributeName returns the identifier on the left of an attribute assignment.
func attributeName(attr *ts.Node, lang *ts.Language, src []byte) string {
	if id := firstNamedChildOfType(attr, lang, "identifier"); id != nil {
		return id.Text(src)
	}
	return ""
}

// blockStringAttr returns the string value of a named attribute inside a block
// body ("" when absent or not a string). Interpolated strings — which parse as
// template_expr, not string_lit — are returned with their ${...} segments
// verbatim so resolveHCLSourceDir can pattern-match get_repo_root().
func blockStringAttr(block *ts.Node, lang *ts.Language, src []byte, name string) string {
	body := firstNamedChildOfType(block, lang, "body")
	if body == nil {
		return ""
	}
	for _, attr := range namedChildren(body) {
		if attr.Type(lang) != "attribute" || attributeName(attr, lang, src) != name {
			continue
		}
		expr := firstNamedChildOfType(attr, lang, "expression")
		if expr == nil {
			return ""
		}
		text := strings.TrimSpace(expr.Text(src))
		if len(text) >= 2 && strings.HasPrefix(text, `"`) && strings.HasSuffix(text, `"`) {
			return text[1 : len(text)-1]
		}
		return ""
	}
	return ""
}

// objectKeys returns the identifier keys of an object literal under an
// attribute, e.g. the keys of inputs = { a = 1, b = 2 }.
func objectKeys(attr *ts.Node, lang *ts.Language, src []byte) []string {
	obj := findDescendantOfType(attr, lang, "object")
	if obj == nil {
		return nil
	}
	var keys []string
	for _, elem := range namedChildren(obj) {
		if elem.Type(lang) != "object_elem" {
			continue
		}
		// Key is the first expression child: (object_elem (expression
		// (variable_expr (identifier))) (expression ...)).
		if keyExpr := firstNamedChildOfType(elem, lang, "expression"); keyExpr != nil {
			if id := findDescendantOfType(keyExpr, lang, "identifier"); id != nil {
				keys = append(keys, id.Text(src))
			} else if lit := findDescendantOfType(keyExpr, lang, "template_literal"); lit != nil {
				keys = append(keys, lit.Text(src))
			}
		}
	}
	return keys
}

// stringLitText returns a string literal's content. Interpolations are kept
// verbatim (the raw ${...} text) so source strings can be pattern-matched.
func stringLitText(lit *ts.Node, src []byte) string {
	text := lit.Text(src)
	text = strings.TrimPrefix(text, `"`)
	text = strings.TrimSuffix(text, `"`)
	return text
}

func namedChildren(n *ts.Node) []*ts.Node {
	count := n.NamedChildCount()
	out := make([]*ts.Node, 0, count)
	for i := 0; i < count; i++ {
		if c := n.NamedChild(i); c != nil {
			out = append(out, c)
		}
	}
	return out
}

func firstNamedChildOfType(n *ts.Node, lang *ts.Language, typ string) *ts.Node {
	for _, c := range namedChildren(n) {
		if c.Type(lang) == typ {
			return c
		}
	}
	return nil
}

// findDescendantOfType returns the first descendant of the given type
// (depth-first), or nil.
func findDescendantOfType(n *ts.Node, lang *ts.Language, typ string) *ts.Node {
	for _, c := range namedChildren(n) {
		if c.Type(lang) == typ {
			return c
		}
		if d := findDescendantOfType(c, lang, typ); d != nil {
			return d
		}
	}
	return nil
}
