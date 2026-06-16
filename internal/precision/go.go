// Package precision enriches code graphs with type-checked semantic call edges.
package precision

import (
	"context"
	"fmt"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/callgraph/vta"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	"github.com/VerifiedOrganic/onboard/internal/providers"
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
func EnrichGo(ctx context.Context, root string, g *providers.Graph) (int, error) {
	if g == nil || len(g.Defs) == 0 {
		return 0, nil
	}
	if !goAvailable(root) {
		if providers.GraphHasLang(g, "go") {
			g.AddPrecisionNote("Go precision unavailable: `go` command or go.mod not found")
		}
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, goPrecisionTimeout)
	defer cancel()

	edges, note := goPreciseEdges(ctx, root)
	if note != "" {
		g.AddPrecisionNote(note)
	}
	if ctx.Err() == context.DeadlineExceeded {
		g.AddPrecisionNote(fmt.Sprintf("Go precision timed out after %s", goPrecisionTimeout))
	}
	if len(edges) == 0 {
		if note == "" && ctx.Err() == nil {
			g.AddPrecisionNote("Go precision returned zero semantic call edges")
		}
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
		if s == nil {
			continue
		}
		f := filepath.ToSlash(s.File)
		byPos[providers.PosKey(f, s.Line, s.Name)] = q
		byFileName[f+"\x00"+s.Name] = append(byFileName[f+"\x00"+s.Name], q)
	}
	resolve := func(absFile string, line int, name string) (string, bool) {
		rel, err := filepath.Rel(root, absFile)
		if err != nil {
			return "", false
		}
		f := filepath.ToSlash(rel)
		if q, ok := byPos[providers.PosKey(f, line, name)]; ok {
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
	edgesSeen := providers.EdgeSetFromGraph(g)
	added := 0
	proved := false
	for _, e := range edges {
		caller, ok := resolve(e.callerFile, e.callerLine, e.callerName)
		if !ok {
			continue
		}
		callee, ok := resolve(e.calleeFile, e.calleeLine, e.calleeName)
		if !ok || caller == callee {
			continue
		}
		key := providers.EdgeKey(caller, callee)
		if g.ProvenEdges[key] {
			continue
		}
		proved = true
		g.ProvenEdges[key] = true
		if edgesSeen.Add(g, caller, callee) {
			added++
		}
	}
	if proved {
		g.MarkPrecision("go")
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

// goPreciseEdges loads the module under root, builds SSA, and returns its type-checked call
// edges (VTA refined over CHA — far less interface-dispatch noise than CHA alone). It
// recovers from any panic in the analysis libraries and returns nil on any problem, so the
// caller can treat precision as best-effort.
func goPreciseEdges(ctx context.Context, root string) (out []posEdge, note string) {
	defer func() {
		if r := recover(); r != nil {
			out = nil // a panic in go/ssa or vta must never crash the server
			note = fmt.Sprintf("Go precision panicked and was skipped: %v", r)
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
	if err != nil {
		return nil, "Go precision package load failed: " + err.Error()
	}
	if len(pkgs) == 0 {
		return nil, "Go precision package load returned no packages"
	}
	var pkgErrs []string
	for _, pkg := range pkgs {
		for _, pe := range pkg.Errors {
			pkgErrs = append(pkgErrs, pe.Error())
		}
	}
	if len(pkgErrs) > 0 {
		return nil, "Go precision package load had errors: " + strings.Join(pkgErrs, "; ")
	}

	prog, ssaPkgs := ssautil.AllPackages(pkgs, ssa.InstantiateGenerics)
	for i, sp := range ssaPkgs {
		if sp == nil && i < len(pkgs) {
			return nil, "Go precision SSA build failed for package " + pkgs[i].ID
		}
	}
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
	return out, ""
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
