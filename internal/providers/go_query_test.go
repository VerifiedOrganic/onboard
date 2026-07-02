package providers_test

import (
	"context"
	"slices"
	"testing"

	"github.com/VerifiedOrganic/onboard/internal/indexer"
)

func TestGoTagsNoBuiltinTypeSymbols(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write(t, root, "main.go", `
package p

func Exported(a int) bool { return helper(a) != "" }

func helper(n int) string { return "" }

type Widget struct{}

func (w *Widget) Method() error { return nil }
`)

	g, err := indexer.Builtin{}.Index(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if g.Files == 0 {
		t.Fatalf("no files indexed; note: %s", g.Note)
	}

	builtinNames := map[string]bool{
		"bool":   true,
		"string": true,
		"int":    true,
		"error":  true,
	}
	for _, sym := range g.Defs {
		if builtinNames[sym.Name] {
			t.Fatalf("builtin type %q was indexed as %s; defs: %v", sym.Name, sym.Kind, defNames(g))
		}
	}

	for _, want := range []struct {
		name string
		kind string
	}{
		{name: "Exported", kind: "function"},
		{name: "helper", kind: "function"},
		{name: "Method", kind: "method"},
	} {
		t.Run(want.name, func(t *testing.T) {
			t.Parallel()

			syms := g.FindSymbols(want.name)
			if len(syms) == 0 {
				t.Fatalf("no symbol named %q (defs: %v)", want.name, defNames(g))
			}
			if got := syms[0].Kind; got != want.kind {
				t.Fatalf("%s kind = %q, want %q", want.name, got, want.kind)
			}
		})
	}

	exported := qnameOf(t, g, "Exported")
	helper := qnameOf(t, g, "helper")
	if !slices.Contains(g.Callees(exported), helper) {
		t.Fatalf("Exported should call helper; got callees=%v", g.Callees(exported))
	}
}
