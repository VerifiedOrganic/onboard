package providers_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VerifiedOrganic/onboard/internal/indexer"
	"github.com/VerifiedOrganic/onboard/internal/precision"
	"github.com/VerifiedOrganic/onboard/internal/testenv"
)

func TestEnrichRustWithRustAnalyzer(t *testing.T) {
	if _, err := exec.LookPath("cargo"); err != nil {
		testenv.SkipUnlessTool(t, "cargo not installed")
	}
	if _, err := exec.LookPath("rust-analyzer"); err != nil {
		testenv.SkipUnlessTool(t, "rust-analyzer not installed")
	}
	root := t.TempDir()
	writeRustPrecisionFixture(t, root, "Cargo.toml", "[package]\nname = \"smoke\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	writeRustPrecisionFixture(t, root, "src/lib.rs", "pub fn helper() -> i32 { 1 }\npub fn run() -> i32 { helper() }\n")
	if !precision.RustAnalyzerAvailable(root) {
		testenv.SkipUnlessTool(t, "rust-analyzer binary is present but cannot run for this toolchain")
	}
	g, err := indexer.Builtin{}.Index(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if g.Files == 0 {
		t.Skipf("rust grammar/tags not available in this build; note: %s", g.Note)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if _, err := precision.EnrichRust(ctx, root, g); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.Precision, "rust-analyzer") {
		t.Fatalf("expected rust-analyzer precision marker; precision=%q note=%q defs=%v", g.Precision, g.Note, defNames(g))
	}
	run := qnameOf(t, g, "run")
	helper := qnameOf(t, g, "helper")
	if !g.IsProven(run, helper) {
		t.Fatalf("expected rust-analyzer to prove run -> helper; callees=%v", g.Callees(run))
	}
}

func writeRustPrecisionFixture(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
