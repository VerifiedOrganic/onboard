package pathutil_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/VerifiedOrganic/onboard/internal/pathutil"
)

func TestResolveRootDefaultsToCwd(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got, err := pathutil.ResolveRoot("")
	if err != nil {
		t.Fatal(err)
	}
	if got != wd {
		t.Fatalf("ResolveRoot(\"\") = %q, want %q", got, wd)
	}
}

func TestJoinUnderRootRejectsEscape(t *testing.T) {
	root := t.TempDir()
	_, err := pathutil.JoinUnderRoot(root, "../outside")
	if err == nil {
		t.Fatal("expected escape error")
	}
}

func TestJoinUnderRootAllowsNested(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := pathutil.JoinUnderRoot(root, filepath.Join("a", "b", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(nested, "file.txt")
	if got != want {
		t.Fatalf("JoinUnderRoot = %q, want %q", got, want)
	}
}
