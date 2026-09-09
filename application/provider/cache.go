package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"nusashell/domain"
)

// BuildPromptCachePolicy is the provider-neutral cache key + TTL for a
// conversation turn. Prefix constants live in domain.
func BuildPromptCachePolicy(settings domain.Settings, p *domain.Provider, model, conversationID, prefix string) *PromptCachePolicy {
	if !settings.PromptCaching || p == nil {
		return nil
	}
	ttl := domain.NormalizeCacheTTL(p.Kind, domain.WireCacheDriver(p.Kind, p.EffectiveDriver(), p.BaseURL), p.CacheTTL)
	if ttl == domain.CacheTTLOff {
		return nil
	}
	if prefix == "" {
		prefix = domain.PromptCacheConversationPrefix
	}
	canonical, _ := json.Marshal([4]string{prefix, p.ID, model, conversationID})
	sum := sha256.Sum256(canonical)
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
// driver and BaseURL through so WireCacheDriver selects the same cache enum
// as the OpenRouter compatibility/profile or vanilla Chat wire.
func BuildPromptCachePolicyForContext(settings domain.Settings, adapter Context, model, conversationID, prefix string) *PromptCachePolicy {
	provider := &domain.Provider{ID: adapter.ProviderID, Kind: adapter.Kind, Driver: adapter.Driver, BaseURL: adapter.BaseURL}
	return BuildPromptCachePolicy(settings, provider, model, conversationID, prefix)
}
