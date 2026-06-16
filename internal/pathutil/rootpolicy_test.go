package pathutil_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/VerifiedOrganic/onboard/internal/apperrors"
	"github.com/VerifiedOrganic/onboard/internal/pathutil"
)

func TestRootPolicyAllowsListedRoot(t *testing.T) {
	base := t.TempDir()
	nested := filepath.Join(base, "repo")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	p := pathutil.NewRootPolicy(base)
	got, err := p.ResolveRoot(nested)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(nested)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("ResolveRoot = %q, want %q", got, want)
	}
}

func TestRootPolicyRejectsOutsideAllowlist(t *testing.T) {
	base := t.TempDir()
	other := t.TempDir()
	p := pathutil.NewRootPolicy(base)
	_, err := p.ResolveRoot(other)
	if err == nil {
		t.Fatal("expected error")
	}
	if !apperrors.Is(err, apperrors.ErrRootNotAllowed) {
		t.Fatalf("err = %v, want ErrRootNotAllowed", err)
	}
}

func TestRootPolicyRejectsSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(base, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	p := pathutil.NewRootPolicy(base)
	_, err := p.ResolveRoot(link)
	if err == nil {
		t.Fatal("expected error")
	}
	if !apperrors.Is(err, apperrors.ErrRootNotAllowed) {
		t.Fatalf("err = %v, want ErrRootNotAllowed", err)
	}
}
