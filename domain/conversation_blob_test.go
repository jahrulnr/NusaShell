package domain

import "testing"

func TestConversationEstimateTokensIncludesCompactionBlob(t *testing.T) {
	c := &Conversation{
		Messages: []Message{
			{Role: RoleUser, Content: "hello world"},
		},
	}
	withoutBlob := c.EstimateTokens()
	// A 300-char opaque blob at ~chars/3 adds 100 tokens.
	c.CompactionBlob = string(make([]byte, 300))
	withBlob := c.EstimateTokens()
	if withBlob <= withoutBlob {
		t.Fatalf("EstimateTokens with blob = %d, want > %d (without blob)", withBlob, withoutBlob)
	}
	if got, want := withBlob-withoutBlob, 100; got != want {
		t.Fatalf("blob token contribution = %d, want %d", got, want)
	}
}

func TestConversationEstimateTokensEmptyBlobUnchanged(t *testing.T) {
	c := &Conversation{
		Messages: []Message{
			{Role: RoleUser, Content: "hello world"},
		},
	}
	base := c.EstimateTokens()
	c.CompactionBlob = ""
	if got := c.EstimateTokens(); got != base {
		t.Fatalf("EstimateTokens with empty blob = %d, want %d", got, base)
	}
}
