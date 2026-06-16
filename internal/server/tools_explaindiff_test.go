package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestExplainDiffIncludesDeletedFileSymbols(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test User")

	writeRepoFile(t, root, "lib.go", "package p\n\nfunc Deleted() {}\n")
	writeRepoFile(t, root, "app.go", "package p\n\nfunc Use() { Deleted() }\n")
	writeRepoFile(t, root, "app_test.go", "package p\n\nimport \"testing\"\n\nfunc TestUse(t *testing.T) { Use() }\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "base")

	if err := os.Remove(filepath.Join(root, "lib.go")); err != nil {
		t.Fatal(err)
	}
	out, err := explainDiff(context.Background(), explainDiffInput{Root: root, Base: "HEAD", Refresh: true})
	if err != nil {
		t.Fatal(err)
	}

	if len(out.ChangedFiles) != 1 || out.ChangedFiles[0].Status != "D" || out.ChangedFiles[0].Path != "lib.go" {
		t.Fatalf("ChangedFiles = %+v, want deleted lib.go", out.ChangedFiles)
	}
	var deleted changedSymbol
	for _, sym := range out.ChangedSymbols {
		if sym.Symbol == "Deleted" {
			deleted = sym
			break
		}
	}
	if deleted.Symbol == "" {
		t.Fatalf("deleted function was not reported; changed symbols: %+v", out.ChangedSymbols)
	}
	if deleted.DirectCallers != 1 {
		t.Errorf("DirectCallers = %d, want 1", deleted.DirectCallers)
	}
	if deleted.ImpactedCount < 2 {
		t.Errorf("ImpactedCount = %d, want caller and test", deleted.ImpactedCount)
	}
	if !containsStringWith(out.AtRiskTests, "app_test.go::TestUse") {
		t.Errorf("AtRiskTests = %v, want app_test.go::TestUse", out.AtRiskTests)
	}
	if !strings.Contains(out.Note, "Deleted files are analyzed against the base tree") {
		t.Errorf("Note did not explain deletion handling: %q", out.Note)
	}
}

func runGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	// #nosec G204 -- git is the fixed executable and arguments are not shell-expanded.
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

func containsStringWith(values []string, needle string) bool {
	for _, v := range values {
		if strings.Contains(v, needle) {
			return true
		}
	}
	return false
}
