package server

import (
	"context"
	"testing"

	"github.com/VerifiedOrganic/onboard/internal/git"
	"github.com/VerifiedOrganic/onboard/internal/providers"
)

func TestTouchedSymbolsAttributesByRange(t *testing.T) {
	// Three functions at lines 3, 10, 20. A symbol's extent runs to the next symbol's line.
	defs := []*providers.Symbol{
		{Name: "A", QName: "f.go::A", Line: 3, Kind: "function"},
		{Name: "B", QName: "f.go::B", Line: 10, Kind: "function"},
		{Name: "C", QName: "f.go::C", Line: 20, Kind: "function"},
	}

	// A change at line 12 falls within B's extent [10, 19] — only B is touched.
	got := touchedSymbols(defs, []git.Hunk{{Start: 12, End: 12}})
	if len(got) != 1 || got[0].Name != "B" {
		t.Errorf("change at line 12 should touch only B, got %v", names(got))
	}

	// A change spanning lines 5-25 overlaps all three.
	got = touchedSymbols(defs, []git.Hunk{{Start: 5, End: 25}})
	if len(got) != 3 {
		t.Errorf("wide change should touch A, B, C; got %v", names(got))
	}

	// A change at line 1 (preamble, before the first symbol) maps to nothing.
	if got = touchedSymbols(defs, []git.Hunk{{Start: 1, End: 1}}); len(got) != 0 {
		t.Errorf("preamble change should touch no symbol, got %v", names(got))
	}
}

func names(syms []*providers.Symbol) []string {
	out := make([]string, len(syms))
	for i, s := range syms {
		out[i] = s.Name
	}
	return out
}

func TestExplainDiffNonGitDegrades(t *testing.T) {
	out, err := explainDiff(context.Background(), explainDiffInput{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("non-git dir should degrade gracefully, not error: %v", err)
	}
	if out.Note == "" {
		t.Error("expected a note explaining the missing git repository")
	}
	if len(out.ChangedSymbols) != 0 {
		t.Errorf("non-git dir should yield no changed symbols, got %d", len(out.ChangedSymbols))
	}
}
