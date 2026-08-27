package application

import (
	"errors"
	"strings"
	"sync"

	"nusashell/domain"
)

// learnedParamsCache is a process-local mirror of the persisted
// LearnedParamRegistry. It is loaded once at startup and mutated in-memory
// as 400 errors are classified; the persisted copy is refreshed on each
// mutation. This avoids a file read on every request (the registry is
// consulted on the hot path) while keeping persistence for restart-safety.
type learnedParamsCache struct {
	mu       sync.RWMutex
	registry *domain.LearnedParamRegistry
	store    LearnedParamStore
}

func newLearnedParamsCache(store LearnedParamStore) *learnedParamsCache {
	if store == nil {
		return &learnedParamsCache{registry: domain.NewLearnedParamRegistry()}
	}
	return &learnedParamsCache{
		registry: store.Load(),
		store:    store,
	}
}

// StripParams returns params that should be stripped for provider+model.
// Safe to call on a nil cache.
func (c *learnedParamsCache) StripParams(provider, model string) []string {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.registry.StripParams(provider, model)
}

// InjectParams returns params that should be injected for provider+model.
// Safe to call on a nil cache.
func (c *learnedParamsCache) InjectParams(provider, model string) []string {
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
func (c *learnedParamsCache) DisabledModalities(provider, model string) []string {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.registry.DisabledModalities(provider, model)
}

// ContextCap returns the smallest learned context-window cap for the
// provider+model, or 0 if none has been learned.
func (c *learnedParamsCache) ContextCap(provider, model string) int {
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
func (c *learnedParamsCache) HasInjectFor(provider, model string) bool {
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
func (c *learnedParamsCache) LearnFrom400(provider, model, errBody string) (domain.LearnedParamAction, string) {
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
	}
	if c.store != nil {
		_ = c.store.Save(c.registry)
	}
	return action, param
}

// BumpHit records that a learned rule fired for provider+model+param.
// Safe to call on a nil cache.
func (c *learnedParamsCache) BumpHit(provider, model, param string) {
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

// applyStrippedParams deletes learned-strip params from a request body
// map (in place). Returns the list of stripped param names for logging.
func applyStrippedParams(body map[string]any, provider, model string, cache *learnedParamsCache) []string {
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

// extractErrBody returns the upstream error message string from an
// UpstreamError, or the plain error string when not wrapped.
func extractErrBody(err error) string {
	if err == nil {
		return ""
	}
	var upstream *UpstreamError
	if errors.As(err, &upstream) && upstream.Err != nil {
		return upstream.Err.Error()
	}
	return err.Error()
}

// isLearnable400 reports whether err is an HTTP 400 that the learning
// classifier should inspect. We only learn from 400s (not 429/5xx — those
// are transient and handled by the retry loop).
func isLearnable400(err error) bool {
	var upstream *UpstreamError
	if !errors.As(err, &upstream) {
		return false
	}
	return upstream.StatusCode == 400
}

// stripReasoningContentParam is the param name we inject/strip for
// reasoning replay learning. Kept as a constant so the chatcompletion and
// responses handlers can check it without importing domain action types.
const stripReasoningContentParam = "reasoning_content"

// hasLearnedReasoningReplay reports whether the cache has learned that
// provider+model requires reasoning_content injection. This complements
// the catalog signal: a model not in models.dev (e.g. stealth/ox-alpha)
// that 400s with "reasoning_content must be passed back" gets learned
// here, and future turns inject it without waiting for the catalog to
// catch up.
func hasLearnedReasoningReplay(cache *learnedParamsCache, provider, model string) bool {
	if cache == nil {
		return false
	}
	for _, p := range cache.InjectParams(provider, model) {
		if strings.EqualFold(p, stripReasoningContentParam) {
			return true
		}
	}
	return false
}
