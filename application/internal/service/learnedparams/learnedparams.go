// Package learnedparams maintains a process-local mirror of the persisted
// LearnedParamRegistry. It is loaded once at startup and mutated in-memory
// as 400 errors are classified; the persisted copy is refreshed on each
// mutation. Extracted from the application root so the agent hot path
// depends on a small leaf package instead of the whole application package.
package learnedparams

import (
	"strings"
	"sync"

	"nusashell/domain"
)

// Store is the persistence port for the learned-param registry. The
// application root's LearnedParamStore satisfies this implicitly.
type Store interface {
	Load() *domain.LearnedParamRegistry
	Save(r *domain.LearnedParamRegistry) error
}

// StripReasoningContentParam is the param name we inject/strip for reasoning
// replay learning. Kept as a constant so callers can check it without
// importing domain action types.
const StripReasoningContentParam = "reasoning_content"

// Cache is a process-local mirror of the persisted LearnedParamRegistry.
type Cache struct {
	mu       sync.RWMutex
	registry *domain.LearnedParamRegistry
	store    Store
}

// New creates a cache from the given store. When store is nil an empty
// registry is used and mutations are not persisted. When a store is
// provided, garbage entries from older classifier versions are sanitized
// on load and the cleaned registry is persisted back.
func New(store Store) *Cache {
	if store == nil {
		return &Cache{registry: domain.NewLearnedParamRegistry()}
	}
	registry := store.Load()
	// Self-heal garbage entries learned by older classifier versions
	// (e.g. param="this" from Gemini's "This is required" phrasing). The
	// sanitized registry is persisted back so the cleanup survives the
	// next restart and does not repeat.
	if removed := registry.Sanitize(); removed > 0 && store != nil {
		_ = store.Save(registry)
	}
	return &Cache{
		registry: registry,
		store:    store,
	}
}

// StripParams returns params that should be stripped for provider+model.
// Safe to call on a nil cache.
func (c *Cache) StripParams(provider, model string) []string {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.registry.StripParams(provider, model)
}

// InjectParams returns params that should be injected for provider+model.
// Safe to call on a nil cache.
func (c *Cache) InjectParams(provider, model string) []string {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.registry.InjectParams(provider, model)
}

// DisabledModalities returns modality names ("vision", "audio", "video")
// that should be disabled for provider+model (learned from 400 errors
// where the model rejected non-text content). Safe to call on a nil cache.
func (c *Cache) DisabledModalities(provider, model string) []string {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.registry.DisabledModalities(provider, model)
}

// NeedsUserNudge reports whether provider+model has learned that requests
// must contain at least one user message. Safe to call on a nil cache.
func (c *Cache) NeedsUserNudge(provider, model string) bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.registry.NeedsUserNudge(provider, model)
}

// OverrideModel applies learned 400-adaptations to a model's metadata in
// place. Safe to call on a nil cache; returns true when a field changed.
func (c *Cache) OverrideModel(m *domain.Model, provider, model string) bool {
	if c == nil || m == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.registry.OverrideModel(m, provider, model)
}

// ContextCap returns the smallest learned context-window cap for the
// provider+model, or 0 if none has been learned.
func (c *Cache) ContextCap(provider, model string) int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.registry.ContextCap(provider, model)
}

// HasInjectFor reports whether the provider+model has any learned inject
// rule (e.g. reasoning_content). Used to upgrade ReasoningReplay from
// "catalog-suspected" to "learned-required" for models not in the catalog.
// Safe to call on a nil cache.
func (c *Cache) HasInjectFor(provider, model string) bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.registry.InjectParams(provider, model)) > 0
}

// LearnFrom400 classifies an upstream 400 error body and, when it matches
// a known pattern, records the learned param and persists the registry.
// Returns the action + param when learned, or ("", "") when the body did
// not match.
//
// Safe to call with a nil cache (no-op).
func (c *Cache) LearnFrom400(provider, model, errBody string) (domain.LearnedParamAction, string) {
	if c == nil {
		return "", ""
	}
	action, param := domain.Classify400Error(errBody)
	if action == "" || param == "" {
		return "", ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch action {
	case domain.LearnedActionStrip:
		c.registry.RecordStrip(provider, model, param, errBody)
	case domain.LearnedActionInject:
		c.registry.RecordInject(provider, model, param, errBody)
	case domain.LearnedActionDisableModality:
		c.registry.RecordDisableModality(provider, model, param, errBody)
	case domain.LearnedActionCapContext:
		c.registry.RecordCapContext(provider, model, param, errBody)
	case domain.LearnedActionNudgeUser:
		c.registry.RecordNudgeUser(provider, model, param, errBody)
	}
	if c.store != nil {
		_ = c.store.Save(c.registry)
	}
	return action, param
}

// BumpHit records that a learned rule fired for provider+model+param.
// Safe to call on a nil cache.
func (c *Cache) BumpHit(provider, model, param string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.registry.BumpHit(provider, model, param)
	if c.store != nil {
		_ = c.store.Save(c.registry)
	}
}

// ApplyStrippedParams deletes learned-strip params from a request body
// map (in place). Returns the list of stripped param names for logging.
func ApplyStrippedParams(body map[string]any, provider, model string, cache *Cache) []string {
	if cache == nil || body == nil {
		return nil
	}
	strip := cache.StripParams(provider, model)
	if len(strip) == 0 {
		return nil
	}
	var stripped []string
	for _, p := range strip {
		if _, ok := body[p]; ok {
			delete(body, p)
			stripped = append(stripped, p)
			cache.BumpHit(provider, model, p)
		}
	}
	return stripped
}

// HasReasoningReplay reports whether the cache has learned that
// provider+model requires reasoning_content injection. This complements
// the catalog signal: a model not in models.dev (e.g. stealth/ox-alpha)
// that 400s with "reasoning_content must be passed back" gets learned
// here, and future turns inject it without waiting for the catalog to
// catch up.
func HasReasoningReplay(cache *Cache, provider, model string) bool {
	if cache == nil {
		return false
	}
	for _, p := range cache.InjectParams(provider, model) {
		if strings.EqualFold(p, StripReasoningContentParam) {
			return true
		}
	}
	return false
}
