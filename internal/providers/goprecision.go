package providers

import (
	"context"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/callgraph/vta"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// The Go precision layer is the optional, capability-gated upgrade from the syntactic
// call graph to a TYPE-CHECKED one for Go code. The pure-Go syntactic Builtin engine stays
// the floor for every language; when a Go toolchain and a buildable module are present, this
// layer enriches the graph with edges resolved by the type checker — crucially including
// interface dispatch, which the name-and-scope syntactic resolver deliberately leaves
// unresolved. Those enriched edges are marked "proven" so the honesty note can upgrade from
// "likely" to "proven" for them.
//
// It never breaks the static-binary guarantee: x/tools is pure Go, and the runtime
// dependency (the `go` command, invoked by go/packages) is gated — absent it, EnrichGo is a
// no-op and the graph stays exactly as the syntactic pass built it.

// goPrecisionTimeout bounds whole-program loading + SSA on large modules so an opt-in
// precise query cannot hang a session.
const goPrecisionTimeout = 90 * time.Second

// EnrichGo augments g in place with type-checked Go call edges, returning how many edges it
// newly resolved (edges the syntactic pass had missed, e.g. interface dispatch). It is
// strictly additive — it adds and marks edges, never removes syntactic ones — and entirely
// non-fatal: any missing capability, load error, or analysis panic leaves g untouched and
// returns (0, nil). Callers treat a precise result as a strict upgrade when present.
func EnrichGo(ctx context.Context, root string, g *Graph) (int, error) {
	if g == nil || len(g.Defs) == 0 || !goAvailable(root) {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, goPrecisionTimeout)
	defer cancel()

	edges := goPreciseEdges(ctx, root)
	if len(edges) == 0 {
		return 0, nil
	}

	// Index the syntactic definitions by (file, line, name). Position alone is NOT unique:
	// the tree-sitter pass also tags return-type identifiers (e.g. a def named "float64" on
	// the same line as the function), so keying on position alone would non-deterministically
	// map a call onto the wrong same-line def. The SSA function name and the def name agree,
	// so name disambiguates; a (file, name) fallback covers a rare line mismatch.
	byPos := make(map[string]string, len(g.Defs))
	byFileName := make(map[string][]string, len(g.Defs))
	for q, s := range g.Defs {
		f := filepath.ToSlash(s.File)
		byPos[posKey(f, s.Line, s.Name)] = q
		byFileName[f+"\x00"+s.Name] = append(byFileName[f+"\x00"+s.Name], q)
	}
	resolve := func(absFile string, line int, name string) (string, bool) {
		rel, err := filepath.Rel(root, absFile)
		if err != nil {
			return "", false
		}
		f := filepath.ToSlash(rel)
		if q, ok := byPos[posKey(f, line, name)]; ok {
			return q, true
		}
		if qs := byFileName[f+"\x00"+name]; len(qs) == 1 { // unambiguous fallback only
			return qs[0], true
		}
		return "", false
	}

	if g.ProvenEdges == nil {
		g.ProvenEdges = map[string]bool{}
	}
	added := 0
	for _, e := range edges {
		caller, ok := resolve(e.callerFile, e.callerLine, e.callerName)
		if !ok {
			continue
		}
		callee, ok := resolve(e.calleeFile, e.calleeLine, e.calleeName)
		if !ok || caller == callee {
			continue
		}
		key := edgeKey(caller, callee)
		if g.ProvenEdges[key] {
			continue
		}
		g.ProvenEdges[key] = true
		// Was this edge already in the syntactic graph, or is it newly resolved?
		isNew := true
		for _, c := range g.Forward[caller] {
			if c == callee {
				isNew = false
				break
			}
		}
		if isNew {
			added++
		}
		g.Forward[caller] = appendUnique(g.Forward[caller], callee)
		g.Reverse[callee] = appendUnique(g.Reverse[callee], caller)
	}
	if len(g.ProvenEdges) > 0 {
		g.Precise = true
	}
	return added, nil
}

// posEdge is one directed call edge expressed by the source positions of the caller and
// callee function declarations (absolute paths, as go/packages reports them).
type posEdge struct {
	callerFile string
	callerLine int
	callerName string
	calleeFile string
	calleeLine int
	calleeName string
}

func posKey(slashFile string, line int, name string) string {
	return slashFile + "\x00" + strconv.Itoa(line) + "\x00" + name
}

// goPreciseEdges loads the module under root, builds SSA, and returns its type-checked call
// edges (VTA refined over CHA — far less interface-dispatch noise than CHA alone). It
// recovers from any panic in the analysis libraries and returns nil on any problem, so the
// caller can treat precision as best-effort.
func goPreciseEdges(ctx context.Context, root string) (out []posEdge) {
	defer func() {
		if recover() != nil {
			out = nil // a panic in go/ssa or vta must never crash the server
		}
	}()

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes |
			packages.NeedSyntax | packages.NeedTypesInfo,
		Dir:     root,
		Context: ctx,
		Tests:   false,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil || len(pkgs) == 0 {
		return nil
	}

	prog, _ := ssautil.AllPackages(pkgs, ssa.InstantiateGenerics)
	prog.Build()

	cg := vta.CallGraph(ssautil.AllFunctions(prog), cha.CallGraph(prog))
	cg.DeleteSyntheticNodes()

	fset := prog.Fset
	for fn, node := range cg.Nodes {
		if fn == nil || node == nil {
			continue
		}
		cf, cl, ok := fnPos(fset, fn)
		if !ok {
			continue
		}
		for _, e := range node.Out {
			if e == nil || e.Callee == nil || e.Callee.Func == nil || e.Callee.Func == fn {
				continue
			}
			callee := e.Callee.Func
			ef, el, ok := fnPos(fset, callee)
			if !ok {
				continue
			}
			out = append(out, posEdge{
				callerFile: cf, callerLine: cl, callerName: fn.Name(),
				calleeFile: ef, calleeLine: el, calleeName: callee.Name(),
			})
		}
	}
	return out
}

// fnPos returns the absolute file and 1-based line of a function's name identifier, or
// false for synthetic functions (init, wrappers, bound methods) that have no source.
func fnPos(fset *token.FileSet, fn *ssa.Function) (string, int, bool) {
	pos := fn.Pos()
	if !pos.IsValid() {
		return "", 0, false
	}
	p := fset.Position(pos)
	if p.Filename == "" {
		return "", 0, false
	}
	return p.Filename, p.Line, true
}

// goAvailable reports whether the Go precision layer can run for root: the go command must
// be on PATH and root must sit within a Go module (a go.mod at or above it).
func goAvailable(root string) bool {
	if _, err := exec.LookPath("go"); err != nil {
		return false
	}
	dir := root
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}
