package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderMapDerived(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a", "a.go"), "package a\n\nfunc Helper() int { return 1 }\n")
	mustWrite(t, filepath.Join(root, "b", "b.go"), "package b\n\nfunc Use() int { return a.Helper() }\n")

	out, err := renderMap(context.Background(), renderMapInput{Root: root, Topic: "Arch", Format: "mermaid", Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Derived {
		t.Error("expected Derived=true when no nodes supplied")
	}
	if out.NodeCount < 2 {
		t.Fatalf("expected >=2 derived nodes, got %d (note: %s)", out.NodeCount, out.Note)
	}
	if !strings.Contains(out.Content, "flowchart LR") {
		t.Error("mermaid output missing flowchart header")
	}
	if !strings.Contains(out.Content, "-->") {
		t.Errorf("expected a cross-package edge in:\n%s", out.Content)
	}
}

func TestRenderMapHTMLExplicit(t *testing.T) {
	out, err := renderMap(context.Background(), renderMapInput{
		Topic:  "Auth flow",
		Format: "html",
		Nodes: []mapNode{
			{ID: "auth", Label: "Auth", Description: "login + sessions", Files: []string{"auth.go"}},
			{ID: "db", Label: "Database"},
		},
		Edges: []mapEdge{{From: "auth", To: "db", Label: "queries"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Derived {
		t.Error("explicit nodes should not be marked derived")
	}
	if out.NodeCount != 2 {
		t.Errorf("node count = %d, want 2", out.NodeCount)
	}
	for _, want := range []string{
		"<!doctype html>", "<svg id=\"svg\"", "flowchart LR",
		"auth", "db", `auth --\u003e|\"queries\"| db`, "showNode", "Reset", "Auth flow",
	} {
		if !strings.Contains(out.Content, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
	for _, blocked := range []string{"https://", "cdn.jsdelivr.net", "svg-pan-zoom", "src=\"http", "import mermaid"} {
		if strings.Contains(out.Content, blocked) {
			t.Errorf("self-contained HTML should not reference %q", blocked)
		}
	}
	// details JSON must carry the description for the click panel
	if !strings.Contains(out.Content, "login + sessions") {
		t.Error("HTML missing node description in DETAILS")
	}
}

func TestRenderMapWritesFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "out", "map.html")
	out, err := renderMap(context.Background(), renderMapInput{
		Root:       root,
		Topic:      "X",
		Format:     "html",
		Nodes:      []mapNode{{ID: "n1", Label: "One"}, {ID: "n2", Label: "Two"}},
		OutputPath: path,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Path != path {
		t.Errorf("Path = %q, want %q", out.Path, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected file written: %v", err)
	}
	if !strings.Contains(string(data), "<!doctype html>") {
		t.Error("written file is not the rendered HTML")
	}
}

func TestRenderMapRejectsOutputPathOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "map.html")
	_, err := renderMap(context.Background(), renderMapInput{
		Root:       root,
		Topic:      "X",
		Format:     "html",
		Nodes:      []mapNode{{ID: "n1", Label: "One"}, {ID: "n2", Label: "Two"}},
		OutputPath: outside,
	})
	if err == nil {
		t.Fatal("expected output path outside root to be rejected")
	}
	if !strings.Contains(err.Error(), "must stay within repo root") {
		t.Fatalf("error = %v, want repo-root restriction", err)
	}
}

func TestSanitizeID(t *testing.T) {
	cases := map[string]string{
		"internal/server": "internal_server",
		"a.b-c":           "a_b_c",
		"123":             "n123",
		"":                "n",
	}
	for in, want := range cases {
		if got := sanitizeID(in); got != want {
			t.Errorf("sanitizeID(%q) = %q, want %q", in, got, want)
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
