package server

import (
	"context"
	"strings"
	"testing"
)

func itemsByName(items []contextItem) map[string]contextItem {
	m := map[string]contextItem{}
	for _, it := range items {
		m[it.Name] = it
	}
	return m
}

func TestContextPackProximity(t *testing.T) {
	root := graphFixture(t) // Main -> Run -> helper; TestRun -> Run
	out, err := contextPack(context.Background(), contextPackInput{Root: root, Seed: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Provider != "builtin" {
		t.Fatalf("provider = %q, want builtin (note: %s)", out.Provider, out.Note)
	}
	by := itemsByName(out.Items)
	for _, name := range []string{"Main", "Run", "helper"} {
		if _, ok := by[name]; !ok {
			t.Fatalf("expected %s in the pack; got %v", name, out.Items)
		}
	}
	// Distance grows with call-graph hops from the seed.
	if by["Main"].Distance != 0 {
		t.Errorf("seed Main should be distance 0, got %d", by["Main"].Distance)
	}
	if by["Run"].Distance != 1 {
		t.Errorf("Run is a direct callee of Main, want distance 1, got %d", by["Run"].Distance)
	}
	if by["helper"].Distance != 2 {
		t.Errorf("helper is two hops from Main, want distance 2, got %d", by["helper"].Distance)
	}
	// The seed ranks first (distance 0, decay 1.0).
	if out.Items[0].Name != "Main" {
		t.Errorf("seed should rank first, got %q", out.Items[0].Name)
	}
	if !strings.Contains(out.Pack, "func Main") || !strings.Contains(out.Pack, "func Run") {
		t.Errorf("pack should contain the seed and neighbor source:\n%s", out.Pack)
	}
}

func TestContextPackSnippetRespectsNextDefBoundary(t *testing.T) {
	root := graphFixture(t)
	out, err := contextPack(context.Background(), contextPackInput{Root: root, Seed: "helper"})
	if err != nil {
		t.Fatal(err)
	}
	h := itemsByName(out.Items)["helper"]
	if !strings.Contains(h.Snippet, "func helper") {
		t.Errorf("helper snippet missing its definition: %q", h.Snippet)
	}
	// helper's window must stop before the next definition (Run), never bleeding into it.
	if strings.Contains(h.Snippet, "func Run") {
		t.Errorf("helper snippet bled into the next definition:\n%s", h.Snippet)
	}
	if h.EndLine >= 5 { // Run is defined on line 5 of the fixture
		t.Errorf("helper end line %d should be before Run's line 5", h.EndLine)
	}
}

func TestContextPackFileSeed(t *testing.T) {
	root := graphFixture(t)
	// A file seed expands to every symbol in that file, all at distance 0.
	out, err := contextPack(context.Background(), contextPackInput{Root: root, Seed: "app.go"})
	if err != nil {
		t.Fatal(err)
	}
	by := itemsByName(out.Items)
	for _, name := range []string{"helper", "Run", "Main"} {
		it, ok := by[name]
		if !ok {
			t.Fatalf("file seed should include %s; got %v", name, out.Items)
		}
		if it.Distance != 0 {
			t.Errorf("%s came from the seed file, want distance 0, got %d", name, it.Distance)
		}
	}
}

func TestContextPackTokenBudget(t *testing.T) {
	root := graphFixture(t)
	full, err := contextPack(context.Background(), contextPackInput{Root: root, Seed: "Main", MaxTokens: 100000})
	if err != nil {
		t.Fatal(err)
	}
	tiny, err := contextPack(context.Background(), contextPackInput{Root: root, Seed: "Main", MaxTokens: 1})
	if err != nil {
		t.Fatal(err)
	}
	if tiny.Included < 1 {
		t.Error("a tiny budget should still include the seed")
	}
	if tiny.Included >= full.Included {
		t.Errorf("tiny budget (%d) should include fewer items than a large one (%d)", tiny.Included, full.Included)
	}
	if !tiny.Truncated {
		t.Error("a tiny budget should report Truncated")
	}
}

func TestContextPackChurnFromGit(t *testing.T) {
	root := gitGraphFixture(t) // util.go committed 6×, app.go once
	out, err := contextPack(context.Background(), contextPackInput{Root: root, Seed: "util.go"})
	if err != nil {
		t.Fatal(err)
	}
	tw, ok := itemsByName(out.Items)["Tweak"]
	if !ok {
		t.Fatalf("seed util.go should include Tweak; got %v", out.Items)
	}
	if tw.Churn != 6 {
		t.Errorf("Tweak churn = %d, want 6 (commits touching util.go)", tw.Churn)
	}
	if !strings.Contains(out.Pack, "func Tweak") {
		t.Errorf("pack should contain Tweak source:\n%s", out.Pack)
	}
}

func TestContextPackNoMatch(t *testing.T) {
	root := graphFixture(t)
	out, err := contextPack(context.Background(), contextPackInput{Root: root, Seed: "DoesNotExist"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Included != 0 || len(out.Matched) != 0 {
		t.Errorf("unknown seed should match nothing, got included=%d matched=%v", out.Included, out.Matched)
	}
	if out.Note == "" {
		t.Error("expected a note explaining the unmatched seed")
	}
}

func TestContextPackEmptySeed(t *testing.T) {
	root := graphFixture(t)
	out, err := contextPack(context.Background(), contextPackInput{Root: root, Seed: "  "})
	if err != nil {
		t.Fatal(err)
	}
	if out.Included != 0 || out.Note == "" {
		t.Errorf("blank seed should return nothing with a note; got included=%d note=%q", out.Included, out.Note)
	}
}
