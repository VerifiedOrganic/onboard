package providers

import (
	"context"
	"strings"
	"testing"
)

// Regression for the same-file name-clash bug: two same-named methods on different
// receivers in one file must NOT cause a call to be mis-attributed to whichever was
// defined last. The ambiguous edge must be left unresolved instead.
func TestBuiltinSameFileNameClashLeftUnresolved(t *testing.T) {
	root := t.TempDir()
	write(t, root, "p.go", "package p\n\n"+
		"type A struct{}\n"+
		"func (A) Do() {}\n\n"+
		"type B struct{}\n"+
		"func (B) Do() {}\n\n"+
		"func Caller(a A) { a.Do() }\n")

	g, err := Builtin{}.Index(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}

	// Both Do definitions must survive under distinct qnames (no silent overwrite).
	dos := 0
	for _, s := range g.Defs {
		if s.Name == "Do" {
			dos++
		}
	}
	if dos != 2 {
		t.Errorf("expected 2 distinct Do defs, got %d (%v)", dos, defNames(g))
	}

	// The ambiguous call must not resolve to either Do.
	caller := qnameOf(t, g, "Caller")
	for _, c := range g.Callees(caller) {
		if strings.Contains(c, "::Do") {
			t.Errorf("ambiguous same-file call mis-resolved to %s; should be unresolved", c)
		}
	}
}

// A globally-ambiguous name (defined in two files, called from a third) must also
// be left unresolved rather than guessed.
func TestBuiltinCrossFileAmbiguityLeftUnresolved(t *testing.T) {
	root := t.TempDir()
	write(t, root, "x.go", "package p\nfunc Helper() {}\n")
	write(t, root, "y.go", "package p\nfunc Helper() {}\n")
	write(t, root, "z.go", "package p\nfunc Use() { Helper() }\n")

	g, err := Builtin{}.Index(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	use := qnameOf(t, g, "Use")
	for _, c := range g.Callees(use) {
		if strings.Contains(c, "::Helper") {
			t.Errorf("cross-file ambiguous Helper should be unresolved, got edge to %s", c)
		}
	}
}

func TestBuiltinDefaultImportDoesNotGuessAmongMultipleExports(t *testing.T) {
	root := t.TempDir()
	write(t, root, "mod.js", "export function Alpha() {}\nexport function Beta() {}\n")
	write(t, root, "app.js", "import Widget from './mod';\nexport function Use() { Widget(); }\n")

	g, err := Builtin{}.Index(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}

	use := qnameOf(t, g, "Use")
	for _, c := range g.Callees(use) {
		if strings.HasPrefix(c, "mod.js::") {
			t.Errorf("ambiguous default import should not guess a target export, got edge to %s", c)
		}
	}
}

// The unique-name case must still resolve (the fix must not over-suppress).
func TestBuiltinUniqueNameStillResolves(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.go", "package p\nfunc only() {}\nfunc Use() { only() }\n")
	g, err := Builtin{}.Index(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	use := qnameOf(t, g, "Use")
	only := qnameOf(t, g, "only")
	var found bool
	for _, c := range g.Callees(use) {
		if c == only {
			found = true
		}
	}
	if !found {
		t.Errorf("unique-name call should still resolve: callees(Use)=%v", g.Callees(use))
	}
}

// A name that collides across packages but is unique within its own package must resolve to
// the SAME-package definition — and never to the other package's. This is the same-directory
// tier: New() is globally ambiguous (defined in a/ and b/), so the old name-only global check
// would drop the edge entirely; the directory scope recovers it without guessing.
func TestBuiltinSamePackageResolution(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a/a.go", "package a\nfunc New() {}\n")
	write(t, root, "a/use.go", "package a\nfunc Use() { New() }\n")
	write(t, root, "b/b.go", "package b\nfunc New() {}\n")

	g, err := Builtin{}.Index(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}

	use := qnameOf(t, g, "Use")
	var toA, toB bool
	for _, c := range g.Callees(use) {
		switch c {
		case "a/a.go::New":
			toA = true
		case "b/b.go::New":
			toB = true
		}
	}
	if !toA {
		t.Errorf("Use should resolve New() to its same-package a/a.go::New; callees=%v", g.Callees(use))
	}
	if toB {
		t.Errorf("Use must NOT cross packages to b/b.go::New; callees=%v", g.Callees(use))
	}
}

// Two same-named methods in one package must STILL be left unresolved — the directory tier
// must not weaken the precision guarantee for genuinely ambiguous intra-package names.
func TestBuiltinSamePackageMethodClashStaysUnresolved(t *testing.T) {
	root := t.TempDir()
	write(t, root, "p/types.go", "package p\ntype A struct{}\nfunc (A) Do() {}\ntype B struct{}\nfunc (B) Do() {}\n")
	write(t, root, "p/use.go", "package p\nfunc Use(a A) { a.Do() }\n")

	g, err := Builtin{}.Index(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	use := qnameOf(t, g, "Use")
	for _, c := range g.Callees(use) {
		if strings.Contains(c, "::Do") {
			t.Errorf("ambiguous same-package Do should stay unresolved, got edge to %s", c)
		}
	}
}
