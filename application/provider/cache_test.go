package provider

import (
	"testing"

	"nusashell/domain"
)

func TestBuildPromptCachePolicyForRequestKeysCurrentPromptAndTools(t *testing.T) {
	settings := domain.DefaultSettings()
	p := &domain.Provider{
		ID:       "openai",
		Kind:     domain.ProviderChat,
		Driver:   domain.ProviderDriverOpenAI,
		BaseURL:  "https://api.openai.com/v1",
		CacheTTL: "30m",
	}
	tools := []ToolDef{{
		Name:        "subagent",
		Description: "delegate to enabled subagents",
		InputSchema: map[string]any{"type": "object"},
	}}

	first := BuildPromptCachePolicyForRequest(settings, p, "gpt-5", "conv_1", domain.PromptCacheConversationPrefix, "system-v1", tools)
	same := BuildPromptCachePolicyForRequest(settings, p, "gpt-5", "conv_1", domain.PromptCacheConversationPrefix, "system-v1", tools)
	changedPrompt := BuildPromptCachePolicyForRequest(settings, p, "gpt-5", "conv_1", domain.PromptCacheConversationPrefix, "system-v2", tools)
	changedTools := BuildPromptCachePolicyForRequest(settings, p, "gpt-5", "conv_1", domain.PromptCacheConversationPrefix, "system-v1", []ToolDef{{
		Name:        "subagent",
		Description: "delegate to a different enabled subagent",
		InputSchema: map[string]any{"type": "object"},
	}})

	for name, policy := range map[string]*PromptCachePolicy{
		"first":          first,
		"same":           same,
		"changed prompt": changedPrompt,
		"changed tools":  changedTools,
	} {
		if policy == nil {
			t.Fatalf("%s policy = nil, want prompt cache policy", name)
		}
	}
	if first.Key != same.Key {
		t.Fatalf("same request contract changed cache key: %q != %q", first.Key, same.Key)
	}
	if len(first.Key) != domain.PromptCacheKeyLength {
		t.Fatalf("cache key length = %d, want %d", len(first.Key), domain.PromptCacheKeyLength)
	}
	if first.Key == changedPrompt.Key {
		t.Fatalf("changed system prompt reused cache key %q", first.Key)
	}
	if first.Key == changedTools.Key {
		t.Fatalf("changed tool definitions reused cache key %q", first.Key)
	}
}
