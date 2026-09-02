package application

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"nusashell/domain"
)

// endpoints returns the lazily-initialized route cache persisted under
// the app data dir. Thread-safe via sync.Once.
func (a *App) endpoints() *endpointsCache {
	a.endpointsCacheOnce.Do(func() {
		path := ""
		if a.DataDir != "" {
			path = filepath.Join(a.DataDir, "endpoints_cache.json")
		}
		a.endpointsCacheVal = newEndpointsCache(path)
	})
	return a.endpointsCacheVal
}

// endpointCacheTTL is how long a model's route list is reused before a
// re-fetch. Routes change rarely (new upstream hosts appear over days),
// so 24h keeps the picker instant without hammering the gateway. The
// cache key is provider_id + model_id because each gateway may serve the
// same model with a different set of upstreams.
const endpointCacheTTL = 24 * time.Hour

// endpointCacheSchemaVersion forces a refresh after the route shape changes
// so old entries cannot hide newly available per-provider pricing.
const endpointCacheSchemaVersion = 2

// endpointsCacheEntry is one cached route list.
type endpointsCacheEntry struct {
	Version   int                 `json:"version"`
	Routes    []domain.ModelRoute `json:"routes"`
	FetchedAt int64               `json:"fetched_at"` // unix seconds
}

// endpointsCache persists route lists to disk so restarts keep the
// picker instant for previously viewed models. Safe for concurrent use.
type endpointsCache struct {
	mu    sync.Mutex
	path  string
	items map[string]endpointsCacheEntry
}

func newEndpointsCache(path string) *endpointsCache {
	c := &endpointsCache{path: path, items: map[string]endpointsCacheEntry{}}
	if path == "" {
		return c
	}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &c.items); err == nil && c.items == nil {
			c.items = map[string]endpointsCacheEntry{}
		}
	}
	return c
}

func (c *endpointsCache) key(providerID, modelID string) string {
	return providerID + "|" + modelID
}

// get returns the routes when a fresh-enough entry exists. Expired
// entries are dropped so a later fetch re-populates them.
func (c *endpointsCache) get(providerID, modelID string) ([]domain.ModelRoute, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := c.key(providerID, modelID)
	e, ok := c.items[k]
	if !ok {
		return nil, false
	}
	if e.Version != endpointCacheSchemaVersion {
		delete(c.items, k)
		return nil, false
	}
	if time.Since(time.Unix(e.FetchedAt, 0)) > endpointCacheTTL {
		delete(c.items, k)
		return nil, false
	}
	return e.Routes, true
}

// set stores routes and persists to disk (best-effort; a failed write is
// logged nowhere — the in-memory entry still serves this process).
func (c *endpointsCache) set(providerID, modelID string, routes []domain.ModelRoute) {
	c.mu.Lock()
	c.items[c.key(providerID, modelID)] = endpointsCacheEntry{
		Version:   endpointCacheSchemaVersion,
		Routes:    routes,
		FetchedAt: time.Now().Unix(),
	}
	b, err := json.Marshal(c.items)
	c.mu.Unlock()
	if err != nil || c.path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(c.path, b, 0o644)
}
