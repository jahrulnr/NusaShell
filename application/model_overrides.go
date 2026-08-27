package application

import (
	"sync"

	"nusashell/domain"
)

// modelOverridesCache is a process-local mirror of the persisted
// ModelOverrideRegistry. It is loaded once at startup and mutated in-memory
// as overrides are set or removed; the persisted copy is refreshed on each
// mutation. This avoids a file read on every request (the registry is
// consulted on the model-resolution hot path) while keeping persistence for
// restart-safety. Mirrors learnedParamsCache.
type modelOverridesCache struct {
	mu       sync.RWMutex
	registry *domain.ModelOverrideRegistry
	store    ModelOverrideStore
}

func newModelOverridesCache(store ModelOverrideStore) *modelOverridesCache {
	if store == nil {
		return &modelOverridesCache{registry: domain.NewModelOverrideRegistry()}
	}
	return &modelOverridesCache{
		registry: store.Load(),
		store:    store,
	}
}

// Apply applies the manual override for provider+model to a model's
// metadata in place. Safe to call on a nil cache; returns true when a field
// changed.
func (c *modelOverridesCache) Apply(m *domain.Model, provider, model string) bool {
	if c == nil || m == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.registry.Apply(m, provider, model)
}

// Get returns the stored override for provider+model, or nil. Safe on nil
// cache. The returned pointer is the live entry; callers must not mutate it.
func (c *modelOverridesCache) Get(provider, model string) *domain.ModelOverride {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.registry.Get(provider, model)
}

// List returns a snapshot of all stored overrides (for reporting/audit).
// Safe on nil cache.
func (c *modelOverridesCache) List() []*domain.ModelOverride {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]*domain.ModelOverride, 0, len(c.registry.Entries))
	for _, o := range c.registry.Entries {
		cp := *o
		out = append(out, &cp)
	}
	return out
}

// Set validates and stores (or merges) an override, then persists the
// registry. Returns the validation error when rejected. Safe on nil cache
// (no-op, returns nil) so callers without a configured store do not fail.
func (c *modelOverridesCache) Set(o *domain.ModelOverride) error {
	if c == nil {
		return nil
	}
	if err := domain.ValidateModelOverride(o); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.registry.Set(o); err != nil {
		return err
	}
	if c.store != nil {
		_ = c.store.Save(c.registry)
	}
	return nil
}

// Remove deletes the override for provider+model and persists the registry.
// Returns true when an entry was removed. Safe on nil cache.
func (c *modelOverridesCache) Remove(provider, model string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.registry.Remove(provider, model) {
		return false
	}
	if c.store != nil {
		_ = c.store.Save(c.registry)
	}
	return true
}
