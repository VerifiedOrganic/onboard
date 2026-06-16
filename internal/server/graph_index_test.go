package server

import (
	"fmt"
	"sync"
	"testing"
	"time"

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

func TestGraphCacheEvictsOldest(t *testing.T) {
	withEmptyGraphCache(t)
	for i := 0; i < graphCacheMaxEntries+2; i++ {
		storeGraph(fmt.Sprintf("root-%02d", i), &providers.Graph{})
	}
	graphCacheMu.Lock()
	defer graphCacheMu.Unlock()
	if len(graphCache) != graphCacheMaxEntries {
		t.Fatalf("graphCache entries = %d, want %d", len(graphCache), graphCacheMaxEntries)
	}
	if _, ok := graphCache["root-00"]; ok {
		t.Error("oldest graph cache entry was not evicted")
	}
}

func TestCachedGraphExpiresByTTL(t *testing.T) {
	withEmptyGraphCache(t)
	graphCacheMu.Lock()
	graphCache["stale"] = graphCacheEntry{graph: &providers.Graph{}, lastUsed: time.Now().Add(-graphCacheTTL - time.Second)}
	graphLocks["stale"] = &sync.Mutex{}
	graphCacheMu.Unlock()

	if _, ok := cachedGraph("stale"); ok {
		t.Fatal("stale graph cache entry should miss")
	}
	graphCacheMu.Lock()
	defer graphCacheMu.Unlock()
	if _, ok := graphCache["stale"]; ok {
		t.Error("stale graph cache entry was not removed")
	}
	if _, ok := graphLocks["stale"]; ok {
		t.Error("stale graph lock was not removed")
	}
}

func withEmptyGraphCache(t *testing.T) {
	t.Helper()
	graphCacheMu.Lock()
	oldCache := graphCache
	oldLocks := graphLocks
	graphCache = map[string]graphCacheEntry{}
	graphLocks = map[string]*sync.Mutex{}
	graphCacheMu.Unlock()
	t.Cleanup(func() {
		graphCacheMu.Lock()
		graphCache = oldCache
		graphLocks = oldLocks
		graphCacheMu.Unlock()
	})
}
