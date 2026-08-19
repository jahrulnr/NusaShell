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
	policy := buildPromptCachePolicy(settings, "prov1", "gpt-5", "conv_abc")
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
	if p := buildPromptCachePolicy(settings, "prov1", "gpt-5", "conv_abc"); p != nil {
		t.Errorf("expected nil policy when PromptCaching is false, got %+v", p)
	}
}

func TestBuildPromptCachePolicyStableForSameInputs(t *testing.T) {
	settings := domain.Settings{PromptCaching: true}
	a := buildPromptCachePolicy(settings, "prov1", "gpt-5", "conv_abc")
	b := buildPromptCachePolicy(settings, "prov1", "gpt-5", "conv_abc")
	if a.Key != b.Key {
		t.Errorf("cache key should be stable for same inputs: %q vs %q", a.Key, b.Key)
	}
	c := buildPromptCachePolicy(settings, "prov1", "gpt-5", "conv_xyz")
	if a.Key == c.Key {
		t.Errorf("cache key should differ for different conversation: %q vs %q", a.Key, c.Key)
	}
}
