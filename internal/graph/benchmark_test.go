package graph

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkIndexSmallRepo(b *testing.B) {
	root := b.TempDir()
	main := filepath.Join(root, "main.go")
	if err := os.WriteFile(main, []byte(`package main
func main() { helper() }
func helper() {}
`), 0o600); err != nil {
		b.Fatal(err)
	}
	svc := DefaultService()
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := svc.Index(ctx, root, true, false); err != nil {
			b.Fatal(err)
		}
	}
}
