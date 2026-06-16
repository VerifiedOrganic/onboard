package transport

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/VerifiedOrganic/onboard/internal/apperrors"
)

func TestRootPolicyFromEnvUsesAllowedWhenUnset(t *testing.T) {
	t.Setenv("ONBOARD_ALLOWED_ROOT", "")
	cwd := t.TempDir()
	p := RootPolicyFromEnv(cwd)
	if !p.Restricted() {
		t.Fatal("expected restricted policy for HTTP mode")
	}
	got, err := p.ResolveRoot(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if got != cwd {
		t.Fatalf("ResolveRoot = %q, want %q", got, cwd)
	}
}

func TestRootPolicyFromEnvHonorsCommaSeparatedList(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	t.Setenv("ONBOARD_ALLOWED_ROOT", a+","+b)

	p := RootPolicyFromEnv("")
	if !p.Restricted() {
		t.Fatal("expected restricted policy")
	}
	if _, err := p.ResolveRoot(a); err != nil {
		t.Fatalf("resolve a: %v", err)
	}
	if _, err := p.ResolveRoot(b); err != nil {
		t.Fatalf("resolve b: %v", err)
	}
	other := t.TempDir()
	_, err := p.ResolveRoot(other)
	if err == nil {
		t.Fatal("expected rejection for path outside allowlist")
	}
	if !apperrors.Is(err, apperrors.ErrRootNotAllowed) {
		t.Fatalf("err = %v, want ErrRootNotAllowed", err)
	}
}

func TestRootPolicyFromEnvNestedPathAllowed(t *testing.T) {
	base := t.TempDir()
	nested := filepath.Join(base, "project")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ONBOARD_ALLOWED_ROOT", base)

	p := RootPolicyFromEnv("")
	got, err := p.ResolveRoot(nested)
	if err != nil {
		t.Fatal(err)
	}
	if got != nested {
		t.Fatalf("ResolveRoot = %q, want %q", got, nested)
	}
}
