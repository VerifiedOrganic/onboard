package providers_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/VerifiedOrganic/onboard/internal/indexer"
	"github.com/VerifiedOrganic/onboard/internal/precision"
	"github.com/VerifiedOrganic/onboard/internal/providers"
)

func writeGoFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// dispatchModule writes a self-contained Go module where the only call to Area() is through
// the Shape interface — so the syntactic resolver (name + scope) cannot know whether it
// lands on Circle.Area or Square.Area and leaves it unresolved, while the type checker can.
func dispatchModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeGoFile(t, root, "go.mod", "module example.com/disp\n\ngo 1.21\n")
	writeGoFile(t, root, "shape.go", `package disp

type Shape interface{ Area() float64 }

type Circle struct{ R float64 }

func (c Circle) Area() float64 { return 3.14 * c.R * c.R }

type Square struct{ S float64 }

func (s Square) Area() float64 { return s.S * s.S }

func Measure(s Shape) float64 { return s.Area() }

func Run() float64 { return Measure(Circle{R: 2}) }
`)
	return root
}

func defsNamed(g *providers.Graph, name string) []*providers.Symbol {
	var out []*providers.Symbol
	for _, s := range g.Defs {
		if s.Name == name {
			out = append(out, s)
		}
	}
	return out
}

func TestEnrichGoResolvesInterfaceDispatch(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not installed")
	}
	root := dispatchModule(t)
	ctx := context.Background()

	// Syntactic baseline: Measure's call to Area() is ambiguous, so no edge is resolved.
	base, err := (indexer.Builtin{}).Index(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	measures := defsNamed(base, "Measure")
	if len(measures) != 1 {
		t.Fatalf("expected exactly one Measure def, got %d", len(measures))
	}
	measureQN := measures[0].QName
	for _, c := range base.Callees(measureQN) {
		if sym := base.Defs[c]; sym != nil && sym.Name == "Area" {
			t.Fatalf("syntactic graph unexpectedly resolved the interface dispatch to %s — "+
				"the test can no longer prove the precision layer adds value", c)
		}
	}

	// Precise: VTA over the type-checked SSA resolves Measure -> Circle.Area.
	g, err := (indexer.Builtin{}).Index(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	added, err := precision.EnrichGo(ctx, root, g)
	if err != nil {
		t.Fatalf("EnrichGo: %v", err)
	}
	if !g.Precise {
		t.Fatal("EnrichGo should mark the graph precise when it resolves Go edges")
	}
	if added < 1 {
		t.Errorf("expected at least one newly-resolved edge, got %d", added)
	}

	// Measure must now reach an Area method, and that edge must be marked proven.
	var reachedArea string
	for _, c := range g.Callees(measureQN) {
		if sym := g.Defs[c]; sym != nil && sym.Name == "Area" {
			reachedArea = c
			break
		}
	}
	if reachedArea == "" {
		t.Fatalf("precise graph should resolve Measure -> Area; callees were %v", g.Callees(measureQN))
	}
	if !g.IsProven(measureQN, reachedArea) {
		t.Errorf("the resolved dispatch edge %s -> %s should be marked proven", measureQN, reachedArea)
	}
	// VTA is precise: only Circle flows into Measure, so Square.Area must NOT be a callee.
	for _, c := range g.Callees(measureQN) {
		if sym := g.Defs[c]; sym != nil && sym.Name == "Area" && c != reachedArea {
			t.Errorf("VTA over-approximated: Measure should reach exactly one Area, also got %s", c)
		}
	}
}

func TestEnrichGoNoopOutsideModule(t *testing.T) {
	root := t.TempDir() // .go files but no go.mod anywhere above
	writeGoFile(t, root, "app.go", "package app\n\nfunc Run() int { return 1 }\n")
	ctx := context.Background()
	g, err := (indexer.Builtin{}).Index(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	added, err := precision.EnrichGo(ctx, root, g)
	if err != nil {
		t.Fatalf("EnrichGo should never error, got %v", err)
	}
	if added != 0 || g.Precise {
		t.Errorf("outside a Go module EnrichGo must be a no-op, got added=%d precise=%v", added, g.Precise)
	}
	if len(g.PrecisionNotes) == 0 {
		t.Fatal("outside a Go module should record a precision note")
	}
}
