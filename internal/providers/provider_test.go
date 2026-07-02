package providers_test

import (
	"slices"
	"testing"

	"github.com/VerifiedOrganic/onboard/internal/providers"
)

func TestFindSymbolsExactMatchFirst(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		query      string
		defs       map[string]*providers.Symbol
		wantQNames []string
	}{
		{
			name:  "exact match precedes substring match",
			query: "BuildTagger",
			defs: map[string]*providers.Symbol{
				"a/javascript.go::getOrBuildTagger": {
					Name:  "getOrBuildTagger",
					QName: "a/javascript.go::getOrBuildTagger",
				},
				"z/tagger.go::BuildTagger": {
					Name:  "BuildTagger",
					QName: "z/tagger.go::BuildTagger",
				},
			},
			wantQNames: []string{
				"z/tagger.go::BuildTagger",
				"a/javascript.go::getOrBuildTagger",
			},
		},
		{
			name:  "substring matches stay sorted by qname",
			query: "Tagger",
			defs: map[string]*providers.Symbol{
				"b/tagger.go::makeTagger": {
					Name:  "makeTagger",
					QName: "b/tagger.go::makeTagger",
				},
				"a/tagger.go::getTagger": {
					Name:  "getTagger",
					QName: "a/tagger.go::getTagger",
				},
			},
			wantQNames: []string{
				"a/tagger.go::getTagger",
				"b/tagger.go::makeTagger",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := &providers.Graph{Defs: tt.defs}
			got := g.FindSymbols(tt.query)
			var gotQNames []string
			for _, sym := range got {
				gotQNames = append(gotQNames, sym.QName)
			}
			if !slices.Equal(gotQNames, tt.wantQNames) {
				t.Fatalf("FindSymbols(%q) qnames = %v, want %v", tt.query, gotQNames, tt.wantQNames)
			}
		})
	}
}
