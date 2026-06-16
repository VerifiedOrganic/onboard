// Package graph indexes repositories with in-memory caching and optional enrichment.
package graph

import (
	"sync"
	"time"

	"github.com/VerifiedOrganic/onboard/internal/providers"
)

const (
	maxEntries = 32
	ttl        = 30 * time.Minute
)

type entry struct {
	graph    *providers.Graph
	lastUsed time.Time
}

// Cache holds recently indexed graphs keyed by repo root (and optional precise suffix).
type Cache struct {
	mu      sync.Mutex
	entries map[string]entry
}

// NewCache returns an empty in-memory graph cache.
func NewCache() *Cache {
	return &Cache{entries: map[string]entry{}}
}

// Get returns a cached graph when key is present and not expired.
func (c *Cache) Get(key string) (*providers.Graph, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	now := time.Now()
	if now.Sub(e.lastUsed) > ttl {
		delete(c.entries, key)
		return nil, false
	}
	e.lastUsed = now
	c.entries[key] = e
	return e.graph, true
}

// Store saves g under key and evicts expired or oldest entries.
func (c *Cache) Store(key string, g *providers.Graph) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	c.entries[key] = entry{graph: g, lastUsed: now}
	for k, e := range c.entries {
		if now.Sub(e.lastUsed) > ttl {
			delete(c.entries, k)
		}
	}
	for len(c.entries) > maxEntries {
		var oldestKey string
		var oldest time.Time
		for k, e := range c.entries {
			if oldestKey == "" || e.lastUsed.Before(oldest) {
				oldestKey = k
				oldest = e.lastUsed
			}
		}
		delete(c.entries, oldestKey)
	}
}
