package application

import (
	"strings"
	"testing"

	"nusashell/domain"
)

func TestEstimateRequestTokensIncludesSystemAndTools(t *testing.T) {
	system := "system prompt content"
	messages := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}
	tools := []ToolDef{{Name: "exec", Description: "run a command"}}
	got := estimateRequestTokens(system, messages, tools)
	if got <= int64(0) {
		t.Fatalf("expected positive estimate, got %d", got)
	}
	if got < int64(10) {
		t.Fatalf("estimate too small for payload: %d", got)
	}
}

func TestBuildPromptCachePolicyKeyLength(t *testing.T) {
	settings := domain.Settings{PromptCaching: true}
	policy := buildPromptCachePolicy(settings, &domain.Provider{ID: "prov1", Kind: domain.ProviderChat}, "gpt-5", "conv_abc")
	if policy == nil {
		t.Fatal("expected non-nil policy when PromptCaching is true")
	}
	if len(policy.Key) != 32 {
		t.Errorf("cache key length = %d, want 32 (key=%q)", len(policy.Key), policy.Key)
	}
	if !strings.HasPrefix(policy.Key, "pc_") {
		t.Errorf("cache key should start with pc_, got %q", policy.Key)
	}
}

func TestBuildPromptCachePolicyNilWhenDisabled(t *testing.T) {
	settings := domain.Settings{PromptCaching: false}
	if p := buildPromptCachePolicy(settings, &domain.Provider{ID: "prov1"}, "gpt-5", "conv_abc"); p != nil {
		t.Errorf("expected nil policy when PromptCaching is false, got %+v", p)
	}
}

func TestBuildPromptCachePolicyStableForSameInputs(t *testing.T) {
	settings := domain.Settings{PromptCaching: true}
	p := &domain.Provider{ID: "prov1", Kind: domain.ProviderChat}
	a := buildPromptCachePolicy(settings, p, "gpt-5", "conv_abc")
	b := buildPromptCachePolicy(settings, p, "gpt-5", "conv_abc")
	if a.Key != b.Key {
		t.Errorf("cache key should be stable for same inputs: %q vs %q", a.Key, b.Key)
	}
	c := buildPromptCachePolicy(settings, p, "gpt-5", "conv_xyz")
	if a.Key == c.Key {
		t.Errorf("cache key should differ for different conversation: %q vs %q", a.Key, c.Key)
	}
}

func TestBuildPromptCachePolicyTTLFromProvider(t *testing.T) {
	settings := domain.Settings{PromptCaching: true}
	anthropic := &domain.Provider{ID: "anthropic", Driver: domain.ProviderDriverAnthropic, Kind: domain.ProviderMessages, CacheTTL: "1h"}
	policy := buildPromptCachePolicy(settings, anthropic, "claude-sonnet-4-6", "conv_abc")
	if policy == nil || policy.TTL != "1h" {
		t.Fatalf("anthropic TTL = %+v, want 1h", policy)
	}

	openai := &domain.Provider{ID: "openai", Driver: domain.ProviderDriverOpenAI, Kind: domain.ProviderResponses}
	policy = buildPromptCachePolicy(settings, openai, "gpt-5", "conv_abc")
	if policy == nil || policy.TTL != "30m" {
		t.Fatalf("openai default TTL = %+v, want 30m", policy)
	}

	openrouter := &domain.Provider{ID: "openrouter", Driver: domain.ProviderDriverOpenRouter, Kind: domain.ProviderChat, CacheTTL: "1h"}
	policy = buildPromptCachePolicy(settings, openrouter, "anthropic/claude-sonnet-4", "conv_abc")
	if policy == nil || policy.TTL != "1h" {
		t.Fatalf("openrouter TTL = %+v, want 1h", policy)
	}
}

func TestTruncateToolErrorShortMessage(t *testing.T) {
	msg := "error: something went wrong"
	if got := truncateToolError(msg); got != msg {
		t.Errorf("short error should be unchanged, got %q", got)
	}
}

func TestTruncateToolErrorLongMessage(t *testing.T) {
	// Simulate a provider error that embeds base64 audio data (1.5MB+)
	msg := "error: Failed to load image from data:audio/mpeg;base64,//Nkx" + strings.Repeat("A", 2000000)
	got := truncateToolError(msg)
	if len(got) > maxToolErrorLen+100 {
		t.Errorf("truncated error should be ~%d chars, got %d", maxToolErrorLen+100, len(got))
	}
	if !strings.HasPrefix(got, "error: Failed to load image") {
		t.Errorf("truncated error should preserve diagnostic prefix, got %q", got[:100])
	}
	if !strings.Contains(got, "[truncated:") {
		t.Errorf("truncated error should note truncation, got %q", got[len(got)-100:])
	}
}

func TestTruncateToolErrorExactLimit(t *testing.T) {
	msg := strings.Repeat("x", maxToolErrorLen)
	if got := truncateToolError(msg); got != msg {
		t.Errorf("error at exact limit should be unchanged, got len %d", len(got))
	}
}
