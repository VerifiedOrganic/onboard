package providers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func calleesOf(g *Graph, name string) []string {
	syms := g.FindSymbols(name)
	if len(syms) == 0 {
		return nil
	}
	return g.Callees(syms[0].QName)
}

func TestIndexWithCacheReusesUnchangedFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "util.go", "package app\nfunc Util() int { return 2 }\n")
	writeFile(t, root, "app.go", "package app\nfunc helper() int { return Util() }\nfunc Run() int { return helper() }\n")
	cache := filepath.Join(t.TempDir(), "g.json")
	ctx := context.Background()

	// First run: both files parsed from scratch and the cache written.
	g1, err := (Builtin{}).IndexWithCache(ctx, root, cache)
	if err != nil {
		t.Fatal(err)
	}
	if g1.Files != 2 || g1.retagged != 2 || g1.reused != 0 {
		t.Fatalf("first run: files=%d retagged=%d reused=%d, want 2/2/0", g1.Files, g1.retagged, g1.reused)
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatalf("cache file not written: %v", err)
	}
	// Cross-file edge helper -> Util resolved.
	if got := calleesOf(g1, "helper"); !containsSubstr(got, "Util") {
		t.Errorf("helper should call Util; callees=%v", got)
	}

	// Second run, no changes: everything reused, nothing re-parsed; graph unchanged.
	g2, err := (Builtin{}).IndexWithCache(ctx, root, cache)
	if err != nil {
		t.Fatal(err)
	}
	if g2.reused != 2 || g2.retagged != 0 {
		t.Fatalf("unchanged run: reused=%d retagged=%d, want 2/0", g2.reused, g2.retagged)
	}
	if len(g2.Defs) != len(g1.Defs) {
		t.Errorf("def count changed across identical runs: %d vs %d", len(g2.Defs), len(g1.Defs))
	}
	if !containsSubstr(calleesOf(g2, "helper"), "Util") {
		t.Error("cross-file edge lost after reuse from cache")
	}

	// Change only app.go: just that file is re-parsed, util.go is reused, and the new
	// symbol and its edge appear.
	writeFile(t, root, "app.go", "package app\nfunc helper() int { return Util() }\nfunc Run() int { return helper() }\nfunc Extra() int { return Util() }\n")
	g3, err := (Builtin{}).IndexWithCache(ctx, root, cache)
	if err != nil {
		t.Fatal(err)
	}
	if g3.retagged != 1 || g3.reused != 1 {
		t.Fatalf("after editing app.go: retagged=%d reused=%d, want 1/1", g3.retagged, g3.reused)
	}
	if len(g3.FindSymbols("Extra")) == 0 {
		t.Error("new symbol Extra not indexed after incremental update")
	}
	if !containsSubstr(calleesOf(g3, "Extra"), "Util") {
		t.Errorf("new edge Extra->Util missing; callees=%v", calleesOf(g3, "Extra"))
	}
}

func TestIndexWithCacheStaleVersionRebuilds(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", "package a\nfunc F() {}\n")
	cache := filepath.Join(t.TempDir(), "g.json")
	// A cache with the wrong version must be ignored (full rebuild).
	if err := os.WriteFile(cache, []byte(`{"v":999,"files":{"a.go":{"h":"x"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := (Builtin{}).IndexWithCache(context.Background(), root, cache)
	if err != nil {
		t.Fatal(err)
	}
	if g.reused != 0 || g.retagged != 1 {
		t.Errorf("stale-version cache should be ignored: reused=%d retagged=%d, want 0/1", g.reused, g.retagged)
	}
}

func TestIndexWithoutCacheStillWorks(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", "package a\nfunc F() {}\nfunc G() { F() }\n")
	g, err := (Builtin{}).Index(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if g.Files == 0 || len(g.Defs) == 0 {
		t.Fatalf("Index without cache produced empty graph: files=%d defs=%d", g.Files, len(g.Defs))
	}
	if !containsSubstr(calleesOf(g, "G"), "F") {
		t.Error("G should call F even without a cache")
	}
}

func containsSubstr(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
