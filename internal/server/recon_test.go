package server

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeFixture builds a small repo tree for recon to scan.
func writeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                    "module example.com/x\n",
		"main.go":                   "package main\nfunc main(){}\n",
		"Dockerfile":                "FROM scratch\n",
		"internal/svc/svc.go":       "package svc\n",
		"internal/svc/svc_test.go":  "package svc\n",
		".github/workflows/ci.yml":  "name: ci\n",
		"node_modules/dep/index.js": "// should be skipped\n",
		"web/next.config.js":        "module.exports={}\n",
	}
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestRecon(t *testing.T) {
	root := writeFixture(t)
	_, out, err := recon(context.Background(), nil, reconInput{Root: root})
	if err != nil {
		t.Fatalf("recon: %v", err)
	}

	if !slices.Contains(out.Stack, "Go") {
		t.Errorf("stack = %v, want it to contain Go", out.Stack)
	}
	if !slices.Contains(out.Frameworks, "Next.js") {
		t.Errorf("frameworks = %v, want Next.js", out.Frameworks)
	}
	if !slices.Contains(out.Tooling, "Docker") {
		t.Errorf("tooling = %v, want Docker", out.Tooling)
	}
	if !slices.Contains(out.Tooling, "GitHub Actions") {
		t.Errorf("tooling = %v, want GitHub Actions", out.Tooling)
	}
	if !slices.Contains(out.EntryPoints, "main.go") {
		t.Errorf("entry_points = %v, want main.go", out.EntryPoints)
	}
	if len(out.TestLayout) == 0 {
		t.Errorf("expected a test dir to be detected")
	}
	// node_modules must be pruned from the tree and not counted as a dir.
	for _, d := range out.DirTree {
		if d == "node_modules/" {
			t.Errorf("dir_tree should not include node_modules: %v", out.DirTree)
		}
	}
}

func TestReconEmptyRootDefaults(t *testing.T) {
	// An empty root should default to cwd and not error.
	_, _, err := recon(context.Background(), nil, reconInput{Root: ""})
	if err != nil {
		t.Fatalf("recon with empty root: %v", err)
	}
}

func TestReconRejectsMissingRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if _, _, err := recon(context.Background(), nil, reconInput{Root: missing}); err == nil {
		t.Fatal("expected an error for a missing root")
	}
}

func TestReconHotspots(t *testing.T) {
	root := gitGraphFixture(t) // util.go churned 6×, app.go committed once
	_, out, err := recon(context.Background(), nil, reconInput{Root: root})
	if err != nil {
		t.Fatalf("recon: %v", err)
	}
	if len(out.Hotspots) == 0 {
		t.Fatal("expected git churn hotspots in a git repo")
	}
	if !strings.Contains(out.Hotspots[0], "util.go") {
		t.Errorf("top hotspot should be the most-churned file util.go; got %v", out.Hotspots)
	}
}

func TestReconNoHotspotsOutsideGit(t *testing.T) {
	root := writeFixture(t) // a plain tree, not a git work tree
	_, out, err := recon(context.Background(), nil, reconInput{Root: root})
	if err != nil {
		t.Fatalf("recon: %v", err)
	}
	if len(out.Hotspots) != 0 {
		t.Errorf("a non-git repo should yield no hotspots, got %v", out.Hotspots)
	}
}

func TestShouldSkipDir(t *testing.T) {
	cases := map[string]bool{
		"node_modules": true,
		"vendor":       true,
		".git":         true,
		".idea":        true,
		".github":      false, // explicitly kept
		"src":          false,
		"cmd":          false,
	}
	for name, want := range cases {
		if got := shouldSkipDir(name); got != want {
			t.Errorf("shouldSkipDir(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestAddUniqueAndKeys(t *testing.T) {
	got := addUnique(addUnique(addUnique(nil, "a"), "b"), "a")
	if len(got) != 2 {
		t.Errorf("addUnique kept dupes: %v", got)
	}
	k := keys(map[string]bool{"z": true, "a": true})
	if !slices.Equal(k, []string{"a", "z"}) {
		t.Errorf("keys not sorted: %v", k)
	}
}
