package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initRepo creates an isolated git repo with one commit and returns its path.
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
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")
	return dir
}

func commit(t *testing.T, dir, file, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", msg}} {
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
}

func TestAvailableAndHead(t *testing.T) {
	repo := initRepo(t)
	if !Available(repo) {
		t.Fatal("Available should be true in a git repo")
	}
	if Available(t.TempDir()) {
		t.Error("Available should be false outside a repo")
	}
	sha, err := HeadSHA(repo)
	if err != nil || len(sha) < 7 {
		t.Fatalf("HeadSHA = %q, err = %v", sha, err)
	}
	br, err := Branch(repo)
	if err != nil || br == "" {
		t.Fatalf("Branch = %q, err = %v", br, err)
	}
}

func TestCommonDir(t *testing.T) {
	repo := initRepo(t)
	dir, err := CommonDir(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(dir) {
		t.Errorf("CommonDir not absolute: %q", dir)
	}
	if filepath.Base(dir) != ".git" {
		t.Errorf("CommonDir = %q, want it to end in .git", dir)
	}
}

func TestDiffNameStatus(t *testing.T) {
	repo := initRepo(t)
	from, _ := HeadSHA(repo)
	commit(t, repo, "b.txt", "new file\n", "add b")
	commit(t, repo, "space name.txt", "spaced\n", "add spaced name")
	commit(t, repo, "a.txt", "changed\n", "modify a")

	changes, err := DiffNameStatus(repo, from)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range changes {
		got[c.Path] = c.Status
	}
	if got["b.txt"] != "A" {
		t.Errorf("b.txt status = %q, want A", got["b.txt"])
	}
	if got["a.txt"] != "M" {
		t.Errorf("a.txt status = %q, want M", got["a.txt"])
	}
	if got["space name.txt"] != "A" {
		t.Errorf("space name.txt status = %q, want A; full map=%v", got["space name.txt"], got)
	}

	// No changes from HEAD to HEAD.
	head, _ := HeadSHA(repo)
	if c, _ := DiffNameStatus(repo, head); len(c) != 0 {
		t.Errorf("expected no changes from HEAD..HEAD, got %v", c)
	}
}

func TestParseNameStatusZ(t *testing.T) {
	changes := parseNameStatusZ("M\x00space name.txt\x00R100\x00old name.go\x00new name.go\x00C75\x00old copy.go\x00new copy.go\x00")
	if len(changes) != 3 {
		t.Fatalf("parsed %d changes, want 3: %+v", len(changes), changes)
	}
	want := []Change{
		{Status: "M", Path: "space name.txt"},
		{Status: "R100", Path: "new name.go"},
		{Status: "C75", Path: "new copy.go"},
	}
	for i := range want {
		if changes[i] != want[i] {
			t.Errorf("change %d = %+v, want %+v", i, changes[i], want[i])
		}
	}
}

func TestParseUnifiedDiff(t *testing.T) {
	// A modified file (two hunks), an added file, a pure deletion, and a rename — the four
	// statuses parseUnifiedDiff must distinguish, with new-side line ranges.
	out := `diff --git a/internal/x/y.go b/internal/x/y.go
index 111..222 100644
--- a/internal/x/y.go
+++ b/internal/x/y.go
@@ -10,2 +10,3 @@ func Foo() {
+	added
@@ -40 +41,2 @@ func Bar() {
+	more
+	lines
diff --git a/new.go b/new.go
new file mode 100644
--- /dev/null
+++ b/new.go
@@ -0,0 +1,5 @@
+	package p
diff --git a/gone.go b/gone.go
deleted file mode 100644
--- a/gone.go
+++ /dev/null
@@ -1,3 +0,0 @@
-	package p
diff --git a/old/name.go b/new/name.go
similarity index 90%
rename from old/name.go
rename to new/name.go
@@ -5 +5 @@ func Z() {
+	tweak
`
	files := parseUnifiedDiff(out)
	if len(files) != 4 {
		t.Fatalf("parsed %d files, want 4: %+v", len(files), files)
	}
	byPath := map[string]FileDiff{}
	for _, f := range files {
		byPath[f.Path] = f
	}

	if m := byPath["internal/x/y.go"]; m.Status != "M" || len(m.Hunks) != 2 ||
		m.Hunks[0] != (Hunk{Start: 10, End: 12}) || m.Hunks[1] != (Hunk{Start: 41, End: 42}) {
		t.Errorf("modified file = %+v, want M with hunks [{10 12} {41 42}]", m)
	}
	if a := byPath["new.go"]; a.Status != "A" || len(a.Hunks) != 1 || a.Hunks[0] != (Hunk{Start: 1, End: 5}) {
		t.Errorf("added file = %+v, want A with hunk {1 5}", a)
	}
	if d := byPath["gone.go"]; d.Status != "D" || len(d.Hunks) != 0 {
		t.Errorf("deleted file = %+v, want D with no new-side hunks", d)
	}
	if r := byPath["new/name.go"]; r.Status != "R" || len(r.Hunks) != 1 {
		t.Errorf("renamed file = %+v, want R with 1 hunk", r)
	}
}

func TestParseHunkHeaderSingleLine(t *testing.T) {
	h, ok := parseHunkHeader("@@ -40 +41 @@ func Bar() {")
	if !ok || h != (Hunk{Start: 41, End: 41}) {
		t.Errorf("single-line hunk = %v ok=%v, want {41 41}", h, ok)
	}
}

func TestDiffAndDefaultBase(t *testing.T) {
	repo := initRepo(t) // one commit on main: a.txt
	base, _ := HeadSHA(repo)

	commit(t, repo, "feature.go", "package p\nfunc New() {}\n", "add feature")
	commit(t, repo, "a.txt", "one\ntwo\n", "extend a")

	diffs, err := Diff(repo, base)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]FileDiff{}
	for _, d := range diffs {
		byPath[d.Path] = d
	}
	if a := byPath["feature.go"]; a.Status != "A" || len(a.Hunks) == 0 {
		t.Errorf("feature.go = %+v, want A with hunks", a)
	}
	if m := byPath["a.txt"]; m.Status != "M" {
		t.Errorf("a.txt status = %q, want M", m.Status)
	}

	// DefaultBase resolves the merge-base with main; on this branch that is the first commit.
	if db := DefaultBase(repo); db == "" {
		t.Error("DefaultBase should resolve a base when main exists")
	}
}
