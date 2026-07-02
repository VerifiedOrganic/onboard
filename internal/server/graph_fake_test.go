package server

import (
	"context"
	"testing"

	"github.com/VerifiedOrganic/onboard/internal/providers"
)

type fakeGraph struct{ g *providers.Graph }

func (f fakeGraph) Index(_ context.Context, _ string, _ bool, _ bool) (*providers.Graph, error) {
	return f.g, nil
}

func TestDeadCodeWithFakeGraph(t *testing.T) {
	t.Parallel()

	qname := "orphan.go::orphan"
	g := &providers.Graph{
		Provider: "builtin",
		Defs: map[string]*providers.Symbol{
			qname: {
				QName: qname,
				Name:  "orphan",
				Kind:  "function",
				File:  "orphan.go",
				Line:  7,
				Lang:  "go",
			},
		},
		Forward: map[string][]string{},
		Reverse: map[string][]string{},
		Files:   1,
		Langs:   []string{"go"},
	}
	deps := newDeps()
	deps.Graph = fakeGraph{g: g}
	ctx := contextWithDeps(context.Background(), deps)

	out, err := deadCode(ctx, deadCodeInput{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Orphans) != 1 {
		t.Fatalf("orphans = %+v, want one fake graph orphan", out.Orphans)
	}
	orphan := out.Orphans[0]
	if orphan.QName != qname || orphan.Confidence != "high" || orphan.Kind != "function" {
		t.Fatalf("orphan = %+v, want %s/high/function", orphan, qname)
	}
}
