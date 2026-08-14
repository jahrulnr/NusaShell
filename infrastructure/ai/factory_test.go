package ai

import (
	"testing"
)

func TestNewProviderHTTPClientHasNoBodyTimeout(t *testing.T) {
	client := newProviderHTTPClient()
	if client.Timeout != 0 {
		t.Fatalf("client timeout = %s, want 0 so SSE bodies can outlive 60s", client.Timeout)
	}
}

func TestMergeAnthropicUsageKeepsMessageStartInput(t *testing.T) {
	start := anthropicUsageToChat(anthropicUsage{InputTokens: 120, CacheReadInputTokens: 80})
	got := mergeAnthropicUsage(start, anthropicUsage{OutputTokens: 15})
	if got.InputTokens != 120 || got.OutputTokens != 15 || got.CacheRead != 80 {
		t.Fatalf("merged usage = %+v", got)
	}
}
