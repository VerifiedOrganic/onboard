package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRepoFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDeadCodeFindsOrphans(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "main.go", "package p\n"+
		"func main() { Use() }\n"+ // entry: invokes Use
		"func Use() { used() }\n"+ // reached from main
		"func used() {}\n"+ // reached from Use
		"func orphan() {}\n"+ // unexported, uncalled -> high
		"func Exported() {}\n"+ // exported, uncalled -> medium
		"type T struct{}\n"+
		"func (T) Unused() {}\n") // method, uncalled, no precise -> low

	out, err := deadCode(context.Background(), deadCodeInput{Root: root})
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]orphan{}
	for _, o := range out.Orphans {
		got[o.Symbol] = o
	}

	// The reachable + entry + test symbols must NOT be reported.
	for _, alive := range []string{"main", "Use", "used"} {
		if _, bad := got[alive]; bad {
			t.Errorf("%q is reachable or an entry point and must not be flagged dead", alive)
		}
	}

	if o, ok := got["orphan"]; !ok {
		t.Errorf("unexported uncalled orphan() should be flagged; got %v", out.Orphans)
	} else if o.Confidence != "high" {
		t.Errorf("orphan() confidence = %q, want high", o.Confidence)
	}
	if o, ok := got["Exported"]; !ok {
		t.Error("exported uncalled Exported() should be flagged")
	} else if o.Confidence != "medium" {
		t.Errorf("Exported() confidence = %q, want medium", o.Confidence)
	}
	if o, ok := got["T.Unused"]; !ok {
		t.Errorf("uncalled method T.Unused should be flagged (receiver-qualified); got %v", out.Orphans)
	} else if o.Confidence != "low" || o.Kind != "method" {
		t.Errorf("T.Unused = %q/%q, want low/method", o.Confidence, o.Kind)
	}

	// High-confidence findings must sort ahead of low-confidence ones.
	if len(out.Orphans) > 1 && out.Orphans[0].Confidence == "low" {
		t.Error("orphans are not ranked highest-confidence first")
	}
}

func TestDeadCodeRustPublicAndMethodsAreNotHighConfidence(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "src/lib.rs", `
pub struct Engine;

impl Engine {
    pub fn new() -> Self { Self }
    pub fn used(&self) { self.helper() }
    fn helper(&self) {}
    pub fn unused_public_method(&self) {}
    fn unused_private_method(&self) {}
}

pub fn public_api() {}
fn private_orphan() {}
fn caller() { Engine::new().used() }
`)

	out, err := deadCode(context.Background(), deadCodeInput{Root: root, Refresh: true})
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]orphan{}
	for _, o := range out.Orphans {
		got[o.Symbol] = o
	}
	for _, alive := range []string{"Engine::new", "Engine::used", "Engine::helper"} {
		if _, bad := got[alive]; bad {
			t.Errorf("%s is reachable and must not be flagged dead; got %v", alive, out.Orphans)
		}
	}
	if o, ok := got["public_api"]; !ok {
		t.Errorf("uncalled Rust pub fn should be listed as a public API lead; got %v", out.Orphans)
	} else if !o.Exported || o.Confidence != "medium" {
		t.Errorf("public_api = exported %v confidence %q, want exported medium", o.Exported, o.Confidence)
	}
	if o, ok := got["private_orphan"]; !ok {
		t.Errorf("private Rust orphan should be listed; got %v", out.Orphans)
	} else if o.Confidence != "high" {
		t.Errorf("private_orphan confidence = %q, want high", o.Confidence)
	}
	for _, method := range []string{"Engine::unused_public_method", "Engine::unused_private_method"} {
		if o, ok := got[method]; !ok {
			t.Errorf("%s should be listed as an uncalled method lead; got %v", method, out.Orphans)
		} else if o.Confidence != "low" {
			t.Errorf("%s confidence = %q, want low because syntactic Rust method dispatch is incomplete", method, o.Confidence)
		}
	}
}

func TestDeadCodeFrameworkManagedExcluded(t *testing.T) {
	root := t.TempDir()
	// Next.js page default export
	writeRepoFile(t, root, "app/page.tsx", `
export default function Page() { return null; }
`)
	// SvelteKit endpoint
	writeRepoFile(t, root, "src/routes/api/+server.ts", `
export async function GET() { return null; }
export async function POST() { return null; }
`)
	// Remix / React Router loader & action
	writeRepoFile(t, root, "app/routes/item.tsx", `
export async function loader() { return null; }
export async function action() { return null; }
`)

	out, err := deadCode(context.Background(), deadCodeInput{Root: root, Refresh: true})
	if err != nil {
		t.Fatal(err)
	}

	for _, o := range out.Orphans {
		if strings.Contains(o.Symbol, "Page") ||
			strings.Contains(o.Symbol, "GET") ||
			strings.Contains(o.Symbol, "POST") ||
			strings.Contains(o.Symbol, "loader") ||
			strings.Contains(o.Symbol, "action") {
			t.Errorf("framework-managed symbol %q was falsely reported as dead code", o.Symbol)
		}
	}
}
