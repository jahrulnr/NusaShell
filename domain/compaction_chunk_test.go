package domain

import "testing"

func TestTakeCompactionChunk(t *testing.T) {
	msgs := []Message{
		{ID: "m1", Role: RoleUser, Content: "short"},
		{ID: "m2", Role: RoleAssistant, Content: "reply"},
		{ID: "m3", Role: RoleUser, Content: "second turn with more content than the first"},
		{ID: "m4", Role: RoleAssistant, Content: "final"},
	}

	t.Run("fits all when budget is large", func(t *testing.T) {
		chunk, rest := TakeCompactionChunk(msgs, 1_000_000)
		if len(chunk) != 4 || len(rest) != 0 {
			t.Fatalf("chunk=%d rest=%d, want 4/0", len(chunk), len(rest))
		}
	})

	t.Run("splits at budget boundary", func(t *testing.T) {
		// m1+m2 fit, m3 would overflow.
		budget := msgs[0].EstimateTokens() + msgs[1].EstimateTokens() + 1
		chunk, rest := TakeCompactionChunk(msgs, budget)
		if len(chunk) != 2 || len(rest) != 2 {
			t.Fatalf("chunk=%d rest=%d, want 2/2", len(chunk), len(rest))
		}
		if chunk[1].ID != "m2" || rest[0].ID != "m3" {
			t.Fatalf("split point wrong: chunk=%v rest=%v", idsOf(chunk), idsOf(rest))
		}
	})

	t.Run("takes single oversized message to avoid stall", func(t *testing.T) {
		// First message alone exceeds the tiny budget — must still be taken.
		chunk, rest := TakeCompactionChunk(msgs, 1)
		if len(chunk) != 1 || len(rest) != 3 {
			t.Fatalf("oversized first: chunk=%d rest=%d, want 1/3", len(chunk), len(rest))
		}
		if chunk[0].ID != "m1" {
			t.Fatalf("first message not taken: %+v", chunk)
		}
	})

	t.Run("empty input returns empty", func(t *testing.T) {
		chunk, rest := TakeCompactionChunk(nil, 1000)
		if len(chunk) != 0 || len(rest) != 0 {
			t.Fatalf("empty: chunk=%d rest=%d, want 0/0", len(chunk), len(rest))
		}
	})
}

func idsOf(msgs []Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.ID
	}
	return out
}
