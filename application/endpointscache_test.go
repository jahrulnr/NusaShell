package application

import (
	"os"
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

func TestEndpointsCacheRejectsLegacyEntriesWithoutPricingSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	legacy := `{"p1|m1":{"routes":[{"Slug":"nebius/fp8","Name":"Nebius"}],"fetched_at":4102444800}}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	c := newEndpointsCache(path)
	if _, ok := c.get("p1", "m1"); ok {
		t.Fatal("legacy route cache must miss so pricing is fetched")
	}
}

func TestEndpointsCacheRejectsPriorSchemaVersion(t *testing.T) {
	c := newEndpointsCache("")
	c.mu.Lock()
	c.items[c.key("p1", "z-ai/glm-5.2:free")] = endpointsCacheEntry{
		Version:   endpointCacheSchemaVersion - 1,
		Routes:    []domain.ModelRoute{{Slug: "baidu/fp8", Name: "Baidu"}},
		FetchedAt: time.Now().Unix(),
	}
	c.mu.Unlock()
	if _, ok := c.get("p1", "z-ai/glm-5.2:free"); ok {
		t.Fatal("prior schema must miss so :free variants refetch their own routes")
	}
}

func TestEndpointsCacheExpiry(t *testing.T) {
	c := newEndpointsCache("")
	c.set("p1", "m1", []domain.ModelRoute{{Slug: "a"}})

	c.mu.Lock()
	c.items[c.key("p1", "m1")] = endpointsCacheEntry{
		Version:   endpointCacheSchemaVersion,
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
