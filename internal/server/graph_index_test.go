package server

import (
	"testing"

	"github.com/VerifiedOrganic/onboard/internal/providers"
)

func TestGoPrecisionHint(t *testing.T) {
	cases := []struct {
		name      string
		g         *providers.Graph
		requested bool
		wantHint  bool
	}{
		{"go with unresolved", &providers.Graph{Langs: []string{"go"}, Unresolved: 3}, false, true},
		{"already requested precise", &providers.Graph{Langs: []string{"go"}, Unresolved: 3}, true, false},
		{"already precise", &providers.Graph{Langs: []string{"go"}, Unresolved: 3, Precise: true}, false, false},
		{"nothing unresolved", &providers.Graph{Langs: []string{"go"}, Unresolved: 0}, false, false},
		{"non-go graph", &providers.Graph{Langs: []string{"python"}, Unresolved: 5}, false, false},
		{"go mixed-case lang", &providers.Graph{Langs: []string{"Go"}, Unresolved: 1}, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := goPrecisionHint(c.g, c.requested) != ""
			if got != c.wantHint {
				t.Errorf("goPrecisionHint hint=%v, want %v", got, c.wantHint)
			}
		})
	}
}

func TestIsTestSymbolIncludesRustInlineTests(t *testing.T) {
	if !isTestSymbol(&providers.Symbol{File: "src/lib.rs", Lang: "rust", Test: true}) {
		t.Error("Rust #[test] symbols in src/*.rs should count as tests")
	}
	if !isTestSymbol(&providers.Symbol{File: "tests/integration.rs", Lang: "rust"}) {
		t.Error("Rust integration tests should count as tests by path")
	}
	if isTestSymbol(&providers.Symbol{File: "src/lib.rs", Lang: "rust"}) {
		t.Error("plain Rust library symbols should not count as tests")
	}
}
