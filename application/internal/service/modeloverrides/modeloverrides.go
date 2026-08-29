// Package modeloverrides maintains a process-local mirror of the persisted
// manual model-override registry. It is loaded once at startup and mutated
// in-memory as overrides are set or removed; the persisted copy is refreshed
// on each mutation. Extracted from the application root so the model
// resolution hot path depends on a small leaf package instead of the whole
// application package.
package modeloverrides

import (
	"sync"

	"nusashell/domain"
)

// Store is the persistence port for the override registry. The application
// root's ModelOverrideStore satisfies this implicitly.
type Store interface {
	Load() *domain.ModelOverrideRegistry
	Save(r *domain.ModelOverrideRegistry) error
}

// Cache is a process-local mirror of the persisted ModelOverrideRegistry.
type Cache struct {
	mu       sync.RWMutex
	registry *domain.ModelOverrideRegistry
	store    Store
}

// New creates a cache from the given store. When store is nil an empty
// registry is used and mutations are not persisted.
func New(store Store) *Cache {
	if store == nil {
		return &Cache{registry: domain.NewModelOverrideRegistry()}
	}
	return &Cache{
		registry: store.Load(),
		store:    store,
	}
}

// Apply applies the manual override for provider+model to a model's
// metadata in place. Safe to call on a nil cache; returns true when a field
// changed.
func (c *Cache) Apply(m *domain.Model, provider, model string) bool {
	if c == nil || m == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.registry.Apply(m, provider, model)
}

// Get returns the stored override for provider+model, or nil. Safe on nil
// cache. The returned pointer is the live entry; callers must not mutate it.
func (c *Cache) Get(provider, model string) *domain.ModelOverride {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.registry.Get(provider, model)
}

// List returns a snapshot of all stored overrides (for reporting/audit).
// Safe on nil cache.
func (c *Cache) List() []*domain.ModelOverride {
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
func (c *Cache) Set(o *domain.ModelOverride) error {
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
func (c *Cache) Remove(provider, model string) bool {
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
