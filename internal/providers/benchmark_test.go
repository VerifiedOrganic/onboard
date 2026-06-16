package providers_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/VerifiedOrganic/onboard/internal/indexer"
	"github.com/VerifiedOrganic/onboard/internal/providers"
)

func BenchmarkPageRank(b *testing.B) {
	g := benchmarkGraph()
	seeds := []string{"main.go::main"}
	b.ReportAllocs()
	for b.Loop() {
		_ = g.PageRank(seeds)
	}
}

func BenchmarkFindSymbols(b *testing.B) {
	g := benchmarkGraph()
	b.ReportAllocs()
	for b.Loop() {
		_ = g.FindSymbols("Handle")
	}
}

func benchmarkGraph() *providers.Graph {
	return &providers.Graph{
		Defs: map[string]*providers.Symbol{
			"main.go::main":   {QName: "main.go::main", Name: "main", File: "main.go", Line: 1, Lang: "go"},
			"main.go::Handle": {QName: "main.go::Handle", Name: "Handle", File: "main.go", Line: 10, Lang: "go"},
			"util.go::helper": {QName: "util.go::helper", Name: "helper", File: "util.go", Line: 3, Lang: "go"},
			"util.go::Handle": {QName: "util.go::Handle", Name: "Handle", File: "util.go", Line: 8, Lang: "go"},
		},
		Forward: map[string][]string{
			"main.go::main":   {"main.go::Handle"},
			"main.go::Handle": {"util.go::helper", "util.go::Handle"},
		},
		Reverse: map[string][]string{
			"main.go::Handle": {"main.go::main"},
			"util.go::helper": {"main.go::Handle"},
			"util.go::Handle": {"main.go::Handle"},
		},
	}
}

func BenchmarkBuiltinIndexSmallRepo(b *testing.B) {
	root := b.TempDir()
	main := filepath.Join(root, "main.go")
	if err := os.WriteFile(main, []byte(`package main
func main() { helper() }
func helper() {}
`), 0o600); err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := (indexer.Builtin{}).Index(ctx, root); err != nil {
			b.Fatal(err)
		}
	}
}
