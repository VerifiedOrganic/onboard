package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitGraphFixture builds a small, hermetic git repo where call-graph centrality and git
// churn deliberately disagree: helper is the high-centrality hub (committed once) while
// Tweak has no callers (low centrality) but its file is committed many times. That split
// is what lets the churn-fusion tests observe each signal independently. Global/system git
// config is neutralized so the commits do not depend on the developer's environment.
func gitGraphFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	env := append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	gitCmd := func(args ...string) {
		t.Helper()
		// #nosec G204 -- git is the fixed executable in an isolated test repo.
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitCmd("init", "-q")
	write("app.go", "package app\n\nfunc helper() int { return 1 }\n\n"+
		"func Run() int { return helper() }\n\nfunc Main() int { return Run() }\n")
	gitCmd("add", "-A")
	gitCmd("commit", "-q", "-m", "init app")
	// Churn util.go across six commits so it dominates the history signal.
	for i := 0; i < 6; i++ {
		write("util.go", fmt.Sprintf("package app\n\nfunc Tweak() int { return %d }\n", i))
		gitCmd("add", "-A")
		gitCmd("commit", "-q", "-m", fmt.Sprintf("tweak %d", i))
	}
	return root
}

func TestRepoMapChurnLiftsHotFiles(t *testing.T) {
	root := gitGraphFixture(t)
	rankOf := func(in repoMapInput) map[string]int {
		t.Helper()
		out, err := repoMap(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]int{}
		for i, s := range out.Symbols {
			m[s.Name] = i
		}
		return m
	}
	w0, w1 := 0.0, 1.0
	cold := rankOf(repoMapInput{Root: root, ChurnWeight: &w0, Refresh: true}) // centrality only
	hot := rankOf(repoMapInput{Root: root, ChurnWeight: &w1})                 // churn only

	if _, ok := cold["Tweak"]; !ok {
		t.Fatal("Tweak should appear in the centrality-only map")
	}
	if hot["Tweak"] >= cold["Tweak"] {
		t.Errorf("churn should lift the high-churn Tweak: rank %d (churn=1.0) should beat %d (churn=0)",
			hot["Tweak"], cold["Tweak"])
	}
	// With churn dominating, the most-committed file's symbol tops the map.
	if hot["Tweak"] != 0 {
		t.Errorf("with churn_weight=1 Tweak (6 commits) should rank first, got %d", hot["Tweak"])
	}
}

func TestRepoMapBlendsChurnByDefault(t *testing.T) {
	root := gitGraphFixture(t)
	out, err := repoMap(context.Background(), repoMapInput{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Note, "churn") {
		t.Errorf("note should mention the churn blend in a git repo; got %q", out.Note)
	}
	var saw bool
	for _, s := range out.Symbols {
		switch s.Name {
		case "Tweak":
			saw = true
			if s.Churn != 6 {
				t.Errorf("Tweak churn = %d, want 6", s.Churn)
			}
		case "helper", "Run", "Main":
			if s.Churn != 1 {
				t.Errorf("%s churn = %d, want 1", s.Name, s.Churn)
			}
		}
	}
	if !saw {
		t.Error("expected Tweak in the symbols list")
	}
}

func TestFileChurnCacheReturnsCopies(t *testing.T) {
	root := gitGraphFixture(t)
	first := fileChurn(context.Background(), root, true)
	if first["util.go"] != 6 {
		t.Fatalf("util.go churn = %d, want 6", first["util.go"])
	}
	first["util.go"] = 0
	second := fileChurn(context.Background(), root, false)
	if second["util.go"] != 6 {
		t.Fatalf("cached churn was mutated through caller map: got %d, want 6", second["util.go"])
	}
}

func TestRepoMap(t *testing.T) {
	root := graphFixture(t)
	out, err := repoMap(context.Background(), repoMapInput{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if out.Provider != "builtin" {
		t.Fatalf("provider = %q, want builtin (note: %s)", out.Provider, out.Note)
	}
	if out.Included == 0 || len(out.Symbols) == 0 {
		t.Fatal("expected at least one ranked symbol")
	}
	// The most-relied-upon symbols (helper, Run) should rank above the entry Main.
	rankOf := map[string]int{}
	for i, s := range out.Symbols {
		rankOf[s.Name] = i
	}
	if r, ok := rankOf["Run"]; !ok {
		t.Errorf("Run should appear in the ranked map; got %v", out.Map)
	} else if m, ok := rankOf["Main"]; ok && r > m {
		t.Errorf("Run (rank %d) should outrank Main (rank %d)", r, m)
	}
	if !strings.Contains(out.Map, "app.go") {
		t.Errorf("rendered map should reference app.go; got:\n%s", out.Map)
	}
}

func TestRepoMapTokenBudgetTruncates(t *testing.T) {
	root := graphFixture(t)
	full, err := repoMap(context.Background(), repoMapInput{Root: root, MaxTokens: 100000})
	if err != nil {
		t.Fatal(err)
	}
	tiny, err := repoMap(context.Background(), repoMapInput{Root: root, MaxTokens: 1})
	if err != nil {
		t.Fatal(err)
	}
	if tiny.Included < 1 {
		t.Error("a tiny budget should still include at least one symbol")
	}
	if tiny.Included >= full.Included {
		t.Errorf("tiny budget (%d) should include fewer symbols than a large one (%d)", tiny.Included, full.Included)
	}
	if !tiny.Truncated {
		t.Error("a tiny budget should report Truncated")
	}
}

func TestRepoMapEmptyRepoDegrades(t *testing.T) {
	root := t.TempDir()
	out, err := repoMap(context.Background(), repoMapInput{Root: root, Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Included != 0 {
		t.Errorf("empty repo should rank 0 symbols, got %d", out.Included)
	}
	if out.Note == "" {
		t.Error("expected a note for an empty repo")
	}
}
