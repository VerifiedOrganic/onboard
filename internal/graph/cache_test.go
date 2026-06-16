package graph

import (
	"fmt"
	"testing"
	"time"

	"github.com/VerifiedOrganic/onboard/internal/providers"
)

func TestCacheEvictsOldest(t *testing.T) {
	c := NewCache()
	for i := range maxEntries + 2 {
		c.Store(fmt.Sprintf("root-%02d", i), &providers.Graph{})
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) != maxEntries {
		t.Fatalf("entries = %d, want %d", len(c.entries), maxEntries)
	}
	if _, ok := c.entries["root-00"]; ok {
		t.Error("oldest entry was not evicted")
	}
}

func TestCacheExpiresByTTL(t *testing.T) {
	c := NewCache()
	c.mu.Lock()
	c.entries["stale"] = entry{graph: &providers.Graph{}, lastUsed: time.Now().Add(-ttl - time.Second)}
	c.mu.Unlock()

	if _, ok := c.Get("stale"); ok {
		t.Fatal("stale entry should miss")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries["stale"]; ok {
		t.Error("stale entry was not removed")
	}
}
