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
