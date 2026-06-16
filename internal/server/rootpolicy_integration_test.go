package server

import (
	"context"
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

	cs, ctx := connect(t, WithRootPolicy(pathutil.NewRootPolicy(allowed)))
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

	cs, ctx := connect(t, WithRootPolicy(pathutil.NewRootPolicy(allowed)))
	var out reconOutput
	callStructured(ctx, t, cs, "recon", map[string]any{"root": allowed}, &out)
	want, err := filepath.EvalSymlinks(allowed)
	if err != nil {
		t.Fatal(err)
	}
	if out.Root != want {
		t.Fatalf("recon root = %q, want %q", out.Root, want)
	}
}

func TestResolveRootRejectsOutsidePolicy(t *testing.T) {
	base := t.TempDir()
	other := t.TempDir()
	resetDeps()
	t.Cleanup(resetDeps)
	Configure(WithRootPolicy(pathutil.NewRootPolicy(base)))

	_, err := resolveRoot(context.Background(), other)
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

	got, err := resolveRoot(context.Background(), nested)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(nested)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("resolveRoot = %q, want %q", got, want)
	}
}
