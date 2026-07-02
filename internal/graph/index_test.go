package graph

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VerifiedOrganic/onboard/internal/providers"
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

func TestIndexPreciseEnrichmentFailureAddsNote(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "main.go")
	if err := os.WriteFile(main, []byte("package p\n\nfunc A() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	originalGo := enrichGo
	originalRust := enrichRust
	enrichGo = func(context.Context, string, *providers.Graph) (int, error) {
		return 0, errors.New("boom")
	}
	enrichRust = func(context.Context, string, *providers.Graph) (int, error) {
		return 0, nil
	}
	t.Cleanup(func() {
		enrichGo = originalGo
		enrichRust = originalRust
	})

	g, err := DefaultService().Index(context.Background(), root, true, true)
	if err != nil {
		t.Fatal(err)
	}

	for _, note := range g.PrecisionNotes {
		if strings.Contains(note, "go precision enrichment failed: boom") {
			return
		}
	}
	t.Fatalf("precision notes = %v, want go precision enrichment failed: boom", g.PrecisionNotes)
}
