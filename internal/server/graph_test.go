package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func graphFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"app.go": "package app\n\n" +
			"func helper() int { return 1 }\n\n" +
			"func Run() int { return helper() }\n\n" +
			"func Main() int { return Run() }\n",
		"app_test.go": "package app\n\nimport \"testing\"\n\n" +
			"func TestRun(t *testing.T) { _ = Run() }\n",
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestTraceFlow(t *testing.T) {
	root := graphFixture(t)
	out, err := traceFlow(context.Background(), traceFlowInput{Root: root, Entry: "Main", Depth: 4})
	if err != nil {
		t.Fatal(err)
	}
	if out.Provider != "builtin" {
		t.Fatalf("provider = %q, want builtin (note: %s)", out.Provider, out.Note)
	}
	if !strings.Contains(out.Matched, "Main") {
		t.Errorf("matched = %q, want it to contain Main", out.Matched)
	}
	var reached []string
	for _, n := range out.Nodes {
		reached = append(reached, n.QName)
	}
	joined := strings.Join(reached, " ")
	if !strings.Contains(joined, "Run") || !strings.Contains(joined, "helper") {
		t.Errorf("trace from Main should reach Run and helper; got %v", reached)
	}
}

func TestTraceFlowSequenceDiagram(t *testing.T) {
	root := graphFixture(t)
	out, err := traceFlow(context.Background(), traceFlowInput{Root: root, Entry: "Main", Depth: 4, Format: "mermaid"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.Mermaid, "sequenceDiagram") {
		t.Fatalf("expected a sequenceDiagram, got:\n%s", out.Mermaid)
	}
	// Main calls Run, Run calls helper — both edges should appear as messages.
	if !strings.Contains(out.Mermaid, "Main->>Run") {
		t.Errorf("sequence should show Main->>Run:\n%s", out.Mermaid)
	}
	if !strings.Contains(out.Mermaid, "Run->>helper") {
		t.Errorf("sequence should show Run->>helper:\n%s", out.Mermaid)
	}
	// Without the format flag, no diagram is produced.
	plain, _ := traceFlow(context.Background(), traceFlowInput{Root: root, Entry: "Main"})
	if plain.Mermaid != "" {
		t.Errorf("mermaid should be empty without format=mermaid, got:\n%s", plain.Mermaid)
	}
}

func TestImpact(t *testing.T) {
	root := graphFixture(t)
	out, err := impactAnalysis(context.Background(), impactInput{Root: root, Symbol: "helper"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Provider != "builtin" {
		t.Fatalf("provider = %q, want builtin", out.Provider)
	}
	joined := strings.Join(out.TransitiveCallers, " ")
	if !strings.Contains(joined, "Run") {
		t.Errorf("transitive callers of helper should include Run; got %v", out.TransitiveCallers)
	}
	if !strings.Contains(joined, "Main") {
		t.Errorf("transitive callers of helper should include Main (Main->Run->helper); got %v", out.TransitiveCallers)
	}
	if len(out.AtRiskTests) == 0 {
		t.Errorf("expected TestRun flagged as an at-risk test; transitive=%v", out.TransitiveCallers)
	}
}

func TestImpactUnknownSymbol(t *testing.T) {
	root := graphFixture(t)
	out, err := impactAnalysis(context.Background(), impactInput{Root: root, Symbol: "NoSuchThing"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Note == "" {
		t.Error("expected a note for an unmatched symbol")
	}
	if out.ImpactedCount != 0 {
		t.Errorf("unmatched symbol should have 0 impact, got %d", out.ImpactedCount)
	}
}

func TestTraceFlowRejectsMissingRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := traceFlow(context.Background(), traceFlowInput{Root: missing, Entry: "Main"}); err == nil {
		t.Fatal("expected an error for a missing root")
	}
}

// a->b, a->c, b->c: at depth 1, b's only callee (c) is also shown, so nothing is
// omitted and Truncated must be false (the bug reported it true).
func TestTraceFlowTruncationFalsePositiveFixed(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "p.go"),
		[]byte("package p\nfunc c() {}\nfunc b() { c() }\nfunc a() { b(); c() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := traceFlow(context.Background(), traceFlowInput{Root: root, Entry: "a", Depth: 1, Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Truncated {
		t.Errorf("Truncated should be false when every callee is shown; nodes=%d", len(out.Nodes))
	}
}

// a->b->c->d: at depth 1 only a and b are shown; b's callee c is omitted, so this
// IS genuine truncation.
func TestTraceFlowTruncationReal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "p.go"),
		[]byte("package p\nfunc d() {}\nfunc c() { d() }\nfunc b() { c() }\nfunc a() { b() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := traceFlow(context.Background(), traceFlowInput{Root: root, Entry: "a", Depth: 1, Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Truncated {
		t.Errorf("expected truncation (b->c omitted at depth 1); nodes=%d", len(out.Nodes))
	}
}

func TestImpactEmptyRepoDegrades(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("just text\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := impactAnalysis(context.Background(), impactInput{Root: root, Symbol: "anything", Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.ImpactedCount != 0 {
		t.Errorf("empty repo impact = %d, want 0", out.ImpactedCount)
	}
	if out.Note == "" {
		t.Error("expected a note when no symbol matches")
	}
}
