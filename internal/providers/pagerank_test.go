package providers

import "testing"

// graph topology: Main -> Run, TestRun -> Run, Run -> helper.
// helper is the foundational leaf everything routes to; it should rank highest.
// Run is called by two symbols; it should outrank the entry points Main/TestRun,
// which have no callers.
func rankFixture() *Graph {
	defs := map[string]*Symbol{
		"app.go::helper":       {QName: "app.go::helper", Name: "helper", Kind: "function", File: "app.go", Line: 1},
		"app.go::Run":          {QName: "app.go::Run", Name: "Run", Kind: "function", File: "app.go", Line: 2},
		"app.go::Main":         {QName: "app.go::Main", Name: "Main", Kind: "function", File: "app.go", Line: 3},
		"app_test.go::TestRun": {QName: "app_test.go::TestRun", Name: "TestRun", Kind: "function", File: "app_test.go", Line: 1},
	}
	fwd := map[string][]string{
		"app.go::Run":          {"app.go::helper"},
		"app.go::Main":         {"app.go::Run"},
		"app_test.go::TestRun": {"app.go::Run"},
	}
	rev := map[string][]string{
		"app.go::helper": {"app.go::Run"},
		"app.go::Run":    {"app.go::Main", "app_test.go::TestRun"},
	}
	return &Graph{Provider: "builtin", Defs: defs, Forward: fwd, Reverse: rev}
}

func TestPageRankOrdersByCentrality(t *testing.T) {
	pr := rankFixture().PageRank(nil)

	helper, run := pr["app.go::helper"], pr["app.go::Run"]
	main, test := pr["app.go::Main"], pr["app_test.go::TestRun"]

	if helper <= run {
		t.Errorf("helper (%.4f) should outrank Run (%.4f): it is the foundational leaf", helper, run)
	}
	if run <= main || run <= test {
		t.Errorf("Run (%.4f) should outrank entry points Main (%.4f) / TestRun (%.4f)", run, main, test)
	}

	var sum float64
	for _, v := range pr {
		sum += v
	}
	if sum < 0.99 || sum > 1.01 {
		t.Errorf("PageRank mass should sum to ~1.0, got %.4f", sum)
	}
}

func TestPageRankIsDeterministic(t *testing.T) {
	g := rankFixture()
	a, b := g.PageRank(nil), g.PageRank(nil)
	for q := range a {
		if a[q] != b[q] {
			t.Fatalf("non-deterministic score for %s: %.6f vs %.6f", q, a[q], b[q])
		}
	}
}

func TestPageRankPersonalizationBiasesTowardSeed(t *testing.T) {
	g := rankFixture()
	base := g.PageRank(nil)
	// Seed by file: Main is in app.go, so seeding app.go must not lower app.go symbols
	// relative to the test-file symbol; concretely, seeding TestRun's file should lift it.
	seeded := g.PageRank([]string{"app_test.go"})
	if seeded["app_test.go::TestRun"] <= base["app_test.go::TestRun"] {
		t.Errorf("seeding app_test.go should raise TestRun's score: base=%.4f seeded=%.4f",
			base["app_test.go::TestRun"], seeded["app_test.go::TestRun"])
	}
}

func TestPageRankEmptyGraph(t *testing.T) {
	g := &Graph{Defs: map[string]*Symbol{}, Forward: map[string][]string{}, Reverse: map[string][]string{}}
	if got := g.PageRank(nil); len(got) != 0 {
		t.Errorf("empty graph PageRank should be empty, got %d entries", len(got))
	}
}
