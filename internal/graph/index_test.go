package graph

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestIndexCachesPreciseSeparately(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "main.go")
	if err := os.WriteFile(main, []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := &Service{cache: NewCache()}
	ctx := context.Background()

	syn, err := svc.Index(ctx, root, false, false)
	if err != nil {
		t.Fatal(err)
	}
	pre, err := svc.Index(ctx, root, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if syn == pre {
		t.Fatal("expected distinct cache entries for precise vs syntactic")
	}
	if syn.Precise {
		t.Fatal("syntactic index should not be precise")
	}
}
