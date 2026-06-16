package server

import (
	"os"
	"path/filepath"
	"testing"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/VerifiedOrganic/onboard/internal/apperrors"
	"github.com/VerifiedOrganic/onboard/internal/pathutil"
)

func TestIntegrationRootPolicyRejectsOutsideAllowlist(t *testing.T) {
	allowed := t.TempDir()
	other := t.TempDir()
	writeFixtureFile(t, allowed, "go.mod", "module example.com/allowed\n\ngo 1.21\n")

	resetDeps()
	t.Cleanup(resetDeps)
	Configure(WithRootPolicy(pathutil.NewRootPolicy(allowed)))

	cs, ctx := connect(t)
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "recon",
		Arguments: map[string]any{"root": other},
	})
	if err != nil {
		t.Fatalf("call recon: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected tool error for root outside allowlist")
	}
}

func TestIntegrationRootPolicyAllowsListedRoot(t *testing.T) {
	allowed := t.TempDir()
	writeFixtureFile(t, allowed, "go.mod", "module example.com/allowed\n\ngo 1.21\n")

	resetDeps()
	t.Cleanup(resetDeps)
	Configure(WithRootPolicy(pathutil.NewRootPolicy(allowed)))

	cs, ctx := connect(t)
	var out reconOutput
	callStructured(ctx, t, cs, "recon", map[string]any{"root": allowed}, &out)
	if out.Root != allowed {
		t.Fatalf("recon root = %q, want %q", out.Root, allowed)
	}
}

func TestResolveRootRejectsOutsidePolicy(t *testing.T) {
	base := t.TempDir()
	other := t.TempDir()
	resetDeps()
	t.Cleanup(resetDeps)
	Configure(WithRootPolicy(pathutil.NewRootPolicy(base)))

	_, err := resolveRoot(other)
	if err == nil {
		t.Fatal("expected error")
	}
	if !apperrors.Is(err, apperrors.ErrRootNotAllowed) {
		t.Fatalf("err = %v, want ErrRootNotAllowed", err)
	}
}

func TestResolveRootAllowsNestedUnderPolicy(t *testing.T) {
	base := t.TempDir()
	nested := filepath.Join(base, "repo")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	resetDeps()
	t.Cleanup(resetDeps)
	Configure(WithRootPolicy(pathutil.NewRootPolicy(base)))

	got, err := resolveRoot(nested)
	if err != nil {
		t.Fatal(err)
	}
	if got != nested {
		t.Fatalf("resolveRoot = %q, want %q", got, nested)
	}
}
