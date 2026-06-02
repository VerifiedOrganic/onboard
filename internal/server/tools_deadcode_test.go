package server

import (
	"context"
	"os"
	"path/filepath"
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
