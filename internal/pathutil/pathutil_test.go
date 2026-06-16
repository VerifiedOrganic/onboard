package pathutil_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/VerifiedOrganic/onboard/internal/apperrors"
	"github.com/VerifiedOrganic/onboard/internal/pathutil"
)

func TestResolveRootDefaultsToCwd(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(wd)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pathutil.ResolveRoot("")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("ResolveRoot(\"\") = %q, want %q", got, want)
	}
}

func TestJoinUnderRootRejectsEscape(t *testing.T) {
	root := t.TempDir()
	_, err := pathutil.JoinUnderRoot(root, "../outside")
	if err == nil {
		t.Fatal("expected escape error")
	}
	if !apperrors.Is(err, apperrors.ErrPathEscapesRoot) {
		t.Fatalf("err = %v, want ErrPathEscapesRoot", err)
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
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(realRoot, "a", "b", "file.txt")
	if got != want {
		t.Fatalf("JoinUnderRoot = %q, want %q", got, want)
	}
}

func TestJoinUnderRootRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	_, err := pathutil.JoinUnderRoot(root, filepath.Join("link", "file.txt"))
	if err == nil {
		t.Fatal("expected symlink escape error")
	}
	if !apperrors.Is(err, apperrors.ErrPathEscapesRoot) {
		t.Fatalf("err = %v, want ErrPathEscapesRoot", err)
	}
}
