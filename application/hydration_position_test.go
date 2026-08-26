package application

import (
	"strings"
	"testing"

	"nusashell/domain"
)

// hydrationPositionFixture returns a conversation shaped like the moment a
// checkpoint is built: [user, empty assistant placeholder].
func hydrationPositionFixture() *domain.Conversation {
	c := domain.NewConversation("conv_pos", "position")
	c.Messages = append(c.Messages,
		domain.Message{ID: "m_user", Role: domain.RoleUser, Content: "halo"},
		domain.Message{ID: "m_asst", Role: domain.RoleAssistant},
	)
	return c
}

func hydrationPositionMsgs() []ChatMessage {
	id := domain.HydrateToolCallPrefix + "ab12cd34_0"
	return []ChatMessage{
		{Role: "assistant", ToolCalls: []domain.ToolCall{{ID: id, Name: "runtime_context", Args: "{}"}}},
		{Role: "tool", ToolResult: &ToolResult{ToolCallID: id, Name: "runtime_context", Content: "{}"}},
	}
}

// TestPersistHydrationInsertsBeforePendingAssistant pins the transcript
// position invariant: system prompt → user → hydration → assistant. The
// checkpoint must land immediately before the current turn's assistant
// placeholder, not at the end of the conversation.
func TestPersistHydrationInsertsBeforePendingAssistant(t *testing.T) {
	app := &App{}
	c := app.persistHydration(hydrationPositionFixture(), hydrationPositionMsgs(), "m_asst")

	if len(c.Messages) != 3 {
		t.Fatalf("len(Messages) = %d, want 3 (user, hydration, placeholder)", len(c.Messages))
	}
	hyd := c.Messages[1]
	if hyd.Role != domain.RoleAssistant || len(hyd.ToolCalls) != 1 ||
		!strings.HasPrefix(hyd.ToolCalls[0].ID, domain.HydrateToolCallPrefix) {
		t.Fatalf("Messages[1] is not the hydration assistant: %+v", hyd)
	}
	if hyd.ToolCalls[0].Output != "{}" {
		t.Fatalf("hydration tool output not attached: %q", hyd.ToolCalls[0].Output)
	}
	if c.Messages[2].ID != "m_asst" || c.Messages[2].Content != "" {
		t.Fatalf("assistant placeholder must stay last, got: %+v", c.Messages[2])
	}

	// Provider view: user → hydration assistant+result; empty placeholder skipped.
	msgs := chatMessages(c, "m_asst", ModelCapabilities{})
	if len(msgs) != 3 ||
		msgs[0].Role != "user" ||
		msgs[1].Role != "assistant" || len(msgs[1].ToolCalls) == 0 ||
		!domain.IsHydrationCallID(msgs[1].ToolCalls[0].ID) ||
		msgs[2].Role != "tool" {
		t.Fatalf("provider order violates user → hydration → assistant: %+v", msgs)
	}
	// The checkpoint must still be detected so later rounds reuse it.
	if !HasHydration(chatMessages(c, "m_asst", ModelCapabilities{})) {
		t.Fatal("inserted checkpoint not detected by HasHydration")
	}
}

// TestPersistHydrationFallsBackToAppendWhenAnchorMissing keeps the old
// append-at-end behavior as a defensive fallback when the placeholder ID is
// not present in the conversation.
func TestPersistHydrationFallsBackToAppendWhenAnchorMissing(t *testing.T) {
	app := &App{}
	c := domain.NewConversation("conv_pos2", "position")
	c.Messages = append(c.Messages,
		domain.Message{ID: "m_user", Role: domain.RoleUser, Content: "hi"},
	)

	out := app.persistHydration(c, hydrationPositionMsgs(), "missing-id")

	if len(out.Messages) != 2 {
		t.Fatalf("len = %d, want 2", len(out.Messages))
	}
	last := out.Messages[1]
	if last.Role != domain.RoleAssistant || len(last.ToolCalls) == 0 ||
		!strings.HasPrefix(last.ToolCalls[0].ID, domain.HydrateToolCallPrefix) {
		t.Fatalf("fallback did not append the checkpoint: %+v", out.Messages)
	}
}

// TestPersistHydrationKeepsRoundTwoPrefixStable verifies the prompt-cache
// property that motivated the insertion point: once round 1's assistant
// message is filled in place, the provider prefix up to and including the
// checkpoint is identical to round 1's — the history only ever grows.
func TestPersistHydrationKeepsRoundTwoPrefixStable(t *testing.T) {
	app := &App{}
	c := app.persistHydration(hydrationPositionFixture(), hydrationPositionMsgs(), "m_asst")

	round1 := chatMessages(c, "m_asst", ModelCapabilities{})

	// Round 1 completes: the placeholder is filled in place with content.
	a := &c.Messages[2]
	a.Content = "jawaban"
	a.Status = domain.StatusDone

	round2 := chatMessages(c, "m_asst", ModelCapabilities{})

	if len(round2) < len(round1) {
		t.Fatalf("round2 shorter than round1: %d < %d", len(round2), len(round1))
	}
	for i := range round1 {
		got, want := round2[i], round1[i]
		if got.Role != want.Role || got.Content != want.Content || got.ToolResult == nil != (want.ToolResult == nil) {
			t.Fatalf("prefix diverges at %d: %+v vs round1 %+v", i, got, want)
		}
	}
}
