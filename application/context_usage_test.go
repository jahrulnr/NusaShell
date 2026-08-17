package application

import "testing"

// ContextTokens is the authoritative per-round context fill. For OpenAI-style
// usage the prompt already includes cached tokens; for Anthropic the cache
// fields are separate and must be summed back in.
func TestChatUsageContextTokens(t *testing.T) {
	cases := []struct {
		name string
		u    ChatUsage
		want int
	}{
		{"openai style", ChatUsage{InputTokens: 1200, OutputTokens: 300}, 1500},
		{"anthropic with cache", ChatUsage{InputTokens: 200, CacheRead: 800, CacheWrite: 100, OutputTokens: 150}, 1250},
		{"empty", ChatUsage{}, 0},
	}
	for _, tc := range cases {
		if got := tc.u.ContextTokens(); got != tc.want {
			t.Errorf("%s: ContextTokens()=%d, want %d", tc.name, got, tc.want)
		}
	}
}

// The last round's ContextTokens must be used for the badge, not the sum of
// per-round usage: summing InputTokens across tool rounds double counts the
// prompt (each round re-sends the growing history) and can exceed the window.
func TestContextTokensUsesLastRoundNotSum(t *testing.T) {
	round1 := ChatUsage{InputTokens: 1000, OutputTokens: 50}  // prompt + first answer
	round2 := ChatUsage{InputTokens: 1200, OutputTokens: 120} // prompt grew (tool result), final answer

	summed := mergeUsage(round1, round2)
	if summed.ContextTokens() != 2370 {
		t.Fatalf("sanity: merged sum = %d", summed.ContextTokens())
	}
	// Authoritative context fill is the last round only.
	if got := round2.ContextTokens(); got != 1320 {
		t.Fatalf("last-round context tokens = %d, want 1320", got)
	}
	if round2.ContextTokens() >= summed.ContextTokens() {
		t.Fatal("last-round context must be smaller than the summed usage (no double counting)")
	}
}
