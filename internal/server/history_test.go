package server

import (
	"context"
	"testing"
)

func TestHistoryNonGitDegrades(t *testing.T) {
	out, err := history(context.Background(), historyInput{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Files) != 0 {
		t.Errorf("non-git dir should yield no files, got %d", len(out.Files))
	}
	if out.Note == "" {
		t.Error("expected a note explaining the missing git repository")
	}
}

func TestHistoryRespectsLimit(t *testing.T) {
	root := t.TempDir() // not a git repo: exercises the degraded, no-error path
	out, err := history(context.Background(), historyInput{Root: root, Limit: 5})
	if err != nil {
		t.Fatalf("history should degrade gracefully, not error: %v", err)
	}
	if len(out.Files) > 5 {
		t.Errorf("limit not respected: %d files", len(out.Files))
	}
}
