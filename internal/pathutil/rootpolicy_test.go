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
	if got != nested {
		t.Fatalf("ResolveRoot = %q, want %q", got, nested)
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
