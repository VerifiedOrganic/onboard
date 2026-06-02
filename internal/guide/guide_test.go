package guide

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		// #nosec G204 -- git is the fixed executable in an isolated test repo.
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")
	return dir
}

func TestWriteReadRoundTrip(t *testing.T) {
	repo := initRepo(t)
	body := "# Codebase Guide\n\nSome analysis here.\n"
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	path, err := Write(repo, body, "full", now)
	if err != nil {
		t.Fatal(err)
	}
	// Guide must live inside .git so it is never committed.
	if !strings.Contains(path, ".git") {
		t.Errorf("guide path %q not inside .git", path)
	}

	g, err := Read(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !g.Exists {
		t.Fatal("guide should exist after Write")
	}
	if g.Header.Mode != "full" {
		t.Errorf("mode = %q, want full", g.Header.Mode)
	}
	if g.Header.SHA == "" {
		t.Error("header SHA should be stamped in a git repo")
	}
	if g.Header.Generated != "2026-05-30T12:00:00Z" {
		t.Errorf("generated = %q", g.Header.Generated)
	}
	if strings.TrimSpace(g.Body) != strings.TrimSpace(body) {
		t.Errorf("body round-trip mismatch:\n got: %q\nwant: %q", g.Body, body)
	}
}

func TestReadMissing(t *testing.T) {
	repo := initRepo(t)
	g, err := Read(repo)
	if err != nil {
		t.Fatal(err)
	}
	if g.Exists {
		t.Error("expected Exists=false for a repo with no guide yet")
	}
}

func TestWriteRejectsUnknownMode(t *testing.T) {
	repo := initRepo(t)
	if _, err := Write(repo, "# Guide\n", "partial", time.Now()); err == nil {
		t.Fatal("expected unknown guide mode to be rejected")
	}
}

func TestPathFallbackWithoutGit(t *testing.T) {
	dir := t.TempDir() // not a git repo
	p := Path(dir)
	if !strings.HasPrefix(p, dir) || !strings.Contains(p, ".onboard") {
		t.Errorf("non-git path = %q, want it under <root>/.onboard", p)
	}
}
