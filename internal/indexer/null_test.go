package indexer_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/VerifiedOrganic/onboard/internal/indexer"
)

func TestNullIndexesGoDefinitions(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc Foo() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := indexer.Null{}.Index(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if g.Provider != "null" || g.Files != 1 {
		t.Fatalf("graph = %+v", g)
	}
	if len(g.Defs) == 0 {
		t.Fatal("expected at least one definition")
	}
}
