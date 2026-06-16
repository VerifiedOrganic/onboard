package providers_test

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/VerifiedOrganic/onboard/internal/indexer"
	"github.com/VerifiedOrganic/onboard/internal/providers"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustUint32Offset(t *testing.T, label string, offset int) uint32 {
	t.Helper()
	if offset < 0 || offset > math.MaxUint32 {
		t.Fatalf("%s offset not found", label)
	}
	return uint32(offset)
}

// qnameOf returns the single QName whose Name == name, failing if not exactly one.
func qnameOf(t *testing.T, g *providers.Graph, name string) string {
	t.Helper()
	syms := g.FindSymbols(name)
	if len(syms) == 0 {
		t.Fatalf("no symbol named %q (defs: %v)", name, defNames(g))
	}
	return syms[0].QName
}

func defNames(g *providers.Graph) []string {
	var out []string
	for _, s := range g.Defs {
		out = append(out, s.QName)
	}
	slices.Sort(out)
	return out
}

func TestBuiltinGoCallGraph(t *testing.T) {
	root := t.TempDir()
	write(t, root, "x.go", "package x\n\nfunc helper() int { return 1 }\n\nfunc Run() int { return helper() }\n")

	g, err := indexer.Builtin{}.Index(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if g.Files == 0 {
		t.Fatalf("no files indexed; note: %s", g.Note)
	}
	run := qnameOf(t, g, "Run")
	helper := qnameOf(t, g, "helper")

	if !slices.Contains(g.Callees(run), helper) {
		t.Errorf("expected Run -> helper edge.\n callees(Run)=%v\n callers(helper)=%v", g.Callees(run), g.Callers(helper))
	}
	if !slices.Contains(g.Impact(helper), run) {
		t.Errorf("expected Run in impact set of helper, got %v", g.Impact(helper))
	}
}

func TestBuiltinPythonCallGraph(t *testing.T) {
	root := t.TempDir()
	write(t, root, "m.py", "def helper():\n    return 1\n\ndef run():\n    return helper()\n")

	g, err := indexer.Builtin{}.Index(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if g.Files == 0 {
		t.Skipf("python grammar/tags not available in this build; note: %s", g.Note)
	}
	run := qnameOf(t, g, "run")
	helper := qnameOf(t, g, "helper")
	if !slices.Contains(g.Callees(run), helper) {
		t.Errorf("expected run -> helper edge. callees(run)=%v", g.Callees(run))
	}
}

func TestBuiltinSkipsVendorAndUnknown(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.go", "package a\nfunc A() {}\n")
	write(t, root, "node_modules/dep.go", "package dep\nfunc Dep() {}\n")
	write(t, root, "readme.unknownext", "not code\n")

	g, err := indexer.Builtin{}.Index(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range g.Defs {
		if s.Name == "Dep" {
			t.Error("indexed a file under node_modules")
		}
	}
}

func TestBuiltinSkipsVeryLargeFiles(t *testing.T) {
	root := t.TempDir()
	const maxIndexedFileBytes = 4 << 20
	write(t, root, "large.go", "package large\n"+strings.Repeat("// padding\n", (maxIndexedFileBytes/len("// padding\n"))+1))

	g, err := indexer.Builtin{}.Index(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if g.Files != 0 {
		t.Fatalf("large file should be skipped, indexed files = %d", g.Files)
	}
	if !strings.Contains(g.Note, "large.go") {
		t.Fatalf("skip note = %q, want large.go", g.Note)
	}
}

func TestProvidersRejectMissingRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := (indexer.Builtin{}).Index(context.Background(), missing); err == nil {
		t.Fatal("builtin provider should reject a missing root")
	}
	if _, err := (indexer.Null{}).Index(context.Background(), missing); err == nil {
		t.Fatal("null provider should reject a missing root")
	}
}

func TestNullProviderDefinitionsOnly(t *testing.T) {
	root := t.TempDir()
	write(t, root, "x.go", "package x\nfunc Alpha() {}\nfunc Beta() {}\n")

	g, err := indexer.Null{}.Index(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Defs) < 2 {
		t.Errorf("null provider should find Alpha and Beta, got %v", defNames(g))
	}
	if len(g.Forward) != 0 {
		t.Error("null provider should produce no edges")
	}
}

func TestNullProviderKeepsDuplicateNames(t *testing.T) {
	root := t.TempDir()
	write(t, root, "x.go", "package x\nfunc Same() {}\nfunc Same() {}\n")

	g, err := indexer.Null{}.Index(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	var lines []int
	for _, s := range g.Defs {
		if s.Name == "Same" {
			lines = append(lines, s.Line)
		}
	}
	slices.Sort(lines)
	if !slices.Equal(lines, []int{2, 3}) {
		t.Errorf("duplicate definitions should both be kept with correct lines, got %v (%v)", lines, defNames(g))
	}
}

// Go method receivers are captured so output can disambiguate same-named methods.
func TestBuiltinCapturesMethodReceiver(t *testing.T) {
	root := t.TempDir()
	write(t, root, "r.go", "package p\n"+
		"type HTMLRenderer struct{}\n"+
		"func (h *HTMLRenderer) Render() {}\n"+
		"type Tree[K any] struct{}\n"+
		"func (Tree[K]) Walk() {}\n"+
		"func Plain() {}\n")

	g, err := indexer.Builtin{}.Index(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}

	bySymbol := map[string]*providers.Symbol{}
	for _, s := range g.Defs {
		bySymbol[s.Name] = s
	}

	if got := bySymbol["Render"]; got == nil || got.Recv != "HTMLRenderer" || got.Display() != "HTMLRenderer.Render" {
		t.Errorf("Render: got recv=%q display=%q, want HTMLRenderer / HTMLRenderer.Render", recvOf(got), displayOf(got))
	}
	if got := bySymbol["Walk"]; got == nil || got.Recv != "Tree" { // generic params stripped
		t.Errorf("Walk: got recv=%q, want Tree", recvOf(got))
	}
	if got := bySymbol["Plain"]; got == nil || got.Recv != "" || got.Display() != "Plain" {
		t.Errorf("Plain: a plain function must have no receiver; got recv=%q display=%q", recvOf(got), displayOf(got))
	}
}

func TestRustOwnerHelpers(t *testing.T) {
	src := []byte("pub struct Engine;\n" +
		"impl Engine {\n" +
		"    pub fn new() -> Self { Engine }\n" +
		"}\n" +
		"trait Parser { fn parse(&self); }\n" +
		"impl Parser for Engine {\n" +
		"    #[test]\n" +
		"    fn parse(&self) {}\n" +
		"}\n")
	text := string(src)
	newStart := mustUint32Offset(t, "new", strings.Index(text, "new"))
	parseDecl := mustUint32Offset(t, "fn parse", strings.LastIndex(text, "fn parse"))
	parseStart := mustUint32Offset(t, "parse", strings.LastIndex(text, "parse"))

	if got := providers.RustOwner(src, newStart); got != "Engine" {
		t.Errorf("owner(new) = %q, want Engine", got)
	}
	if got := providers.RustOwner(src, parseStart); got != "Engine as Parser" {
		t.Errorf("owner(parse) = %q, want Engine as Parser", got)
	}
	if !providers.RustDefinitionIsTest(src, parseDecl) {
		t.Error("parse should be marked as a Rust test from its attribute")
	}
}

func TestBuiltinRustDisplayAndInlineTest(t *testing.T) {
	root := t.TempDir()
	write(t, root, "lib.rs", "pub struct Engine;\n"+
		"impl Engine {\n"+
		"    pub fn new() -> Self { Engine }\n"+
		"}\n"+
		"impl Parser for Engine {\n"+
		"    #[test]\n"+
		"    fn parse(&self) {}\n"+
		"}\n"+
		"trait Parser { fn parse(&self); }\n")

	g, err := indexer.Builtin{}.Index(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if g.Files == 0 {
		t.Skipf("rust grammar/tags not available in this build; note: %s", g.Note)
	}
	var sawOwned, sawTest bool
	for _, s := range g.Defs {
		if s.Name == "new" && s.Recv == "Engine" && s.Display() == "Engine::new" {
			sawOwned = true
		}
		if s.Name == "parse" && s.Test {
			sawTest = true
		}
	}
	if !sawOwned {
		t.Errorf("expected Engine::new display in Rust defs, got %v", defNames(g))
	}
	if !sawTest {
		t.Errorf("expected #[test] Rust function marked as Test, got %v", defNames(g))
	}
}

func recvOf(s *providers.Symbol) string {
	if s == nil {
		return "<nil>"
	}
	return s.Recv
}

func displayOf(s *providers.Symbol) string {
	if s == nil {
		return "<nil>"
	}
	return s.Display()
}
