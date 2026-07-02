package providers_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/VerifiedOrganic/onboard/internal/indexer"
	"github.com/VerifiedOrganic/onboard/internal/providers"
)

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func calleesOf(g *providers.Graph, name string) []string {
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
	g1, err := (indexer.Builtin{}).IndexWithCache(ctx, root, cache)
	if err != nil {
		t.Fatal(err)
	}
	if g1.Files != 2 || g1.Retagged != 2 || g1.Reused != 0 {
		t.Fatalf("first run: files=%d retagged=%d reused=%d, want 2/2/0", g1.Files, g1.Retagged, g1.Reused)
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatalf("cache file not written: %v", err)
	}
	// Cross-file edge helper -> Util resolved.
	if got := calleesOf(g1, "helper"); !containsSubstr(got, "Util") {
		t.Errorf("helper should call Util; callees=%v", got)
	}

	// Second run, no changes: everything reused, nothing re-parsed; graph unchanged.
	g2, err := (indexer.Builtin{}).IndexWithCache(ctx, root, cache)
	if err != nil {
		t.Fatal(err)
	}
	if g2.Reused != 2 || g2.Retagged != 0 {
		t.Fatalf("unchanged run: reused=%d retagged=%d, want 2/0", g2.Reused, g2.Retagged)
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
	g3, err := (indexer.Builtin{}).IndexWithCache(ctx, root, cache)
	if err != nil {
		t.Fatal(err)
	}
	if g3.Retagged != 1 || g3.Reused != 1 {
		t.Fatalf("after editing app.go: retagged=%d reused=%d, want 1/1", g3.Retagged, g3.Reused)
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
	g, err := (indexer.Builtin{}).IndexWithCache(context.Background(), root, cache)
	if err != nil {
		t.Fatal(err)
	}
	if g.Reused != 0 || g.Retagged != 1 {
		t.Errorf("stale-version cache should be ignored: reused=%d retagged=%d, want 0/1", g.Reused, g.Retagged)
	}
}

func TestIndexWithoutCacheStillWorks(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", "package a\nfunc F() {}\nfunc G() { F() }\n")
	g, err := (indexer.Builtin{}).Index(context.Background(), root)
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

func TestDiskCacheRoundTripPreservesAllFields(t *testing.T) {
	t.Parallel()

	symbol := &providers.Symbol{
		QName:  "src/service.go::Service.Handle",
		Name:   "Handle",
		Kind:   "method",
		File:   "src/service.go",
		Line:   12,
		Column: 8,
		Lang:   "go",
		Recv:   "Service",
		Test:   true,
		Public: true,
	}
	assertNoZeroExportedFields(t, "providers.Symbol", *symbol)

	resolvedImport := providers.ResolvedImport{
		TargetFile: "src/dependency.go",
		TargetName: "Dependency",
	}
	assertNoZeroExportedFields(t, "providers.ResolvedImport", resolvedImport)
	imports := map[string]providers.ResolvedImport{
		"dep": resolvedImport,
	}

	cache := filepath.Join(t.TempDir(), "cache", "index.json")
	providers.SaveDiskIndex(cache, &providers.DiskIndex{
		Version: providers.CacheVersion,
		Files: map[string]providers.DiskFile{
			"src/service.go": {
				Hash:    "non-empty-hash",
				Lang:    "go",
				Defs:    []*providers.Symbol{symbol},
				Imports: providers.ToDiskImports(imports),
			},
		},
	})

	loaded := providers.LoadDiskIndex(cache)
	if loaded == nil {
		t.Fatal("LoadDiskIndex returned nil")
	}
	file, ok := loaded.Files["src/service.go"]
	if !ok {
		t.Fatalf("loaded files = %v, want src/service.go", loaded.Files)
	}
	if len(file.Defs) != 1 {
		t.Fatalf("loaded defs len = %d, want 1", len(file.Defs))
	}
	if !reflect.DeepEqual(*symbol, *file.Defs[0]) {
		t.Fatalf("symbol round-trip mismatch:\n got: %#v\nwant: %#v", *file.Defs[0], *symbol)
	}

	loadedImports := providers.FromDiskImports(file.Imports)
	if !reflect.DeepEqual(imports, loadedImports) {
		t.Fatalf("resolved import round-trip mismatch:\n got: %#v\nwant: %#v", loadedImports, imports)
	}
}

func assertNoZeroExportedFields(t *testing.T, name string, value any) {
	t.Helper()

	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	typ := v.Type()
	var zeroFields []string
	for i := range v.NumField() {
		field := typ.Field(i)
		if field.PkgPath != "" {
			continue
		}
		if v.Field(i).IsZero() {
			zeroFields = append(zeroFields, field.Name)
		}
	}
	if len(zeroFields) > 0 {
		t.Fatalf("%s fixture has zero exported fields: %s", name, strings.Join(zeroFields, ", "))
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
