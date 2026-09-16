package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"nusashell/domain"
)

// BuildPromptCachePolicy is the provider-neutral cache key + TTL for a
// conversation turn. Prefix constants live in domain. Callers that send a
// system prompt and tools should use BuildPromptCachePolicyForRequest so a
// changed request contract cannot reuse the previous provider cache shard.
func BuildPromptCachePolicy(settings domain.Settings, p *domain.Provider, model, conversationID, prefix string) *PromptCachePolicy {
	return buildPromptCachePolicy(settings, p, model, conversationID, prefix, "", nil, false)
}

// BuildPromptCachePolicyForRequest builds a cache policy whose key is scoped
// to the exact system prompt and top-level tool definitions sent in the
// request. This keeps runtime-only changes (for example enabling or disabling
// an ACP subagent) from sharing a provider prefix/session with the old
// contract.
func BuildPromptCachePolicyForRequest(settings domain.Settings, p *domain.Provider, model, conversationID, prefix, system string, tools []ToolDef) *PromptCachePolicy {
	return buildPromptCachePolicy(settings, p, model, conversationID, prefix, system, tools, true)
}

func buildPromptCachePolicy(settings domain.Settings, p *domain.Provider, model, conversationID, prefix, system string, tools []ToolDef, includeContract bool) *PromptCachePolicy {
	if !settings.PromptCaching || p == nil {
		return nil
	}
	driver := domain.WireCacheDriver(p.Kind, p.EffectiveDriver(), p.BaseURL)
	if len(domain.CacheTTLsFor(p.Kind, driver)) == 0 {
		// Kinds without a wire-level cache control (Gemini implicit caching)
		// need no key, TTL, or block markers.
		return nil
	}
	ttl := domain.NormalizeCacheTTL(p.Kind, driver, p.CacheTTL)
	if ttl == domain.CacheTTLOff {
		return nil
	}
	if prefix == "" {
		prefix = domain.PromptCacheConversationPrefix
	}
	var canonical any = [4]string{prefix, p.ID, model, conversationID}
	if includeContract {
		canonical = struct {
			Prefix         string    `json:"prefix"`
			ProviderID     string    `json:"provider_id"`
			Model          string    `json:"model"`
			ConversationID string    `json:"conversation_id"`
			System         string    `json:"system"`
			Tools          []ToolDef `json:"tools"`
		}{
			Prefix:         prefix,
			ProviderID:     p.ID,
			Model:          model,
			ConversationID: conversationID,
			System:         system,
			Tools:          tools,
		}
	}
	canonicalJSON, err := json.Marshal(canonical)
	if err != nil {
		// Tool schemas are expected to be JSON values. If a malformed runtime
		// value reaches this boundary, disable the cache for this request
		// rather than risk reusing a prefix whose contract we could not hash.
		return nil
	}
	sum := sha256.Sum256(canonicalJSON)
	full := hex.EncodeToString(sum[:])
	suffixLength := domain.PromptCacheKeyLength - len(prefix)
	if suffixLength <= 0 || suffixLength > len(full) {
		return nil
	}
	return &PromptCachePolicy{
		Mode: "auto",
		Key:  prefix + full[:suffixLength],
		TTL:  ttl,
	}
}

// BuildPromptCachePolicyForContext preserves the provider identity needed for
// a stable key after a provider has been reduced to Context. Pass the stored
// driver and BaseURL through so WireCacheDriver selects the same cache enum as
// the OpenRouter compatibility/profile or vanilla Chat policy. OpenCode is a
// deliberate split: vanilla Chat serialization with the OpenRouter 5m/1h
// cache enum required by Console Go.
func BuildPromptCachePolicyForContext(settings domain.Settings, adapter Context, model, conversationID, prefix string) *PromptCachePolicy {
	provider := &domain.Provider{ID: adapter.ProviderID, Kind: adapter.Kind, Driver: adapter.Driver, BaseURL: adapter.BaseURL}
	return BuildPromptCachePolicy(settings, provider, model, conversationID, prefix)
}

// BuildPromptCachePolicyForContextWithContract is the Context equivalent of
// BuildPromptCachePolicyForRequest. It is used by compaction paths that have
// already resolved the adapter but must still key the cache by their own
// system prompt and tool contract.
func BuildPromptCachePolicyForContextWithContract(settings domain.Settings, adapter Context, model, conversationID, prefix, system string, tools []ToolDef) *PromptCachePolicy {
	provider := &domain.Provider{ID: adapter.ProviderID, Kind: adapter.Kind, Driver: adapter.Driver, BaseURL: adapter.BaseURL}
	return BuildPromptCachePolicyForRequest(settings, provider, model, conversationID, prefix, system, tools)
}
