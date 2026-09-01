package application

import (
	"path/filepath"
	"testing"
	"time"

	"nusashell/domain"
)

func TestEndpointsCacheRoundTripAndPersist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	c := newEndpointsCache(path)

	if _, ok := c.get("p1", "m1"); ok {
		t.Fatal("empty cache must miss")
	}
	c.set("p1", "m1", []domain.ModelRoute{{Slug: "nebius/fp8", Name: "Nebius"}})

	routes, ok := c.get("p1", "m1")
	if !ok || len(routes) != 1 || routes[0].Slug != "nebius/fp8" {
		t.Fatalf("get after set = %+v, %v", routes, ok)
	}

	// A brand-new cache instance loads the persisted file.
	c2 := newEndpointsCache(path)
	routes, ok = c2.get("p1", "m1")
	if !ok || len(routes) != 1 || routes[0].Slug != "nebius/fp8" {
		t.Fatalf("reloaded cache = %+v, %v", routes, ok)
	}

	// Keys are per provider+model: same model on another provider misses.
	if _, ok := c2.get("p2", "m1"); ok {
		t.Fatal("different provider must not share the cache entry")
	}
}

func TestEndpointsCacheExpiry(t *testing.T) {
	c := newEndpointsCache("")
	c.set("p1", "m1", []domain.ModelRoute{{Slug: "a"}})

	c.mu.Lock()
	c.items[c.key("p1", "m1")] = endpointsCacheEntry{
		Routes:    []domain.ModelRoute{{Slug: "stale"}},
		FetchedAt: time.Now().Add(-(endpointCacheTTL + time.Hour)).Unix(),
	}
	c.mu.Unlock()

	if _, ok := c.get("p1", "m1"); ok {
		t.Fatal("expired entry must miss and be dropped")
	}
	if _, ok := c.items[c.key("p1", "m1")]; ok {
		t.Fatal("expired entry must be removed from the map")
	}
}
