package application

import (
	"context"
	"strings"
	"testing"

	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
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

// TestPersistHydrationFallsBackToAppendWhenAnchorMissing inserts after the
// last user when the placeholder ID is not present. With only a user
// message that is equivalent to append-at-end.
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

// TestPersistHydrationInsertsAfterLastUserWhenKeepHasPriorRounds is the
// post-compaction shape: handover user, then retained assistant rounds,
// then the in-flight placeholder. Inserting before the placeholder parks
// the checkpoint in the middle of agent work. It must land immediately
// after the last user so the provider sees user → hydration → assistants.
func TestPersistHydrationInsertsAfterLastUserWhenKeepHasPriorRounds(t *testing.T) {
	app := &App{}
	c := domain.NewConversation("conv_mid", "mid")
	c.Messages = append(c.Messages,
		domain.Message{ID: "handover", Role: domain.RoleUser, Content: "Compacted context handover:\n## Goal"},
		domain.Message{ID: "old1", Role: domain.RoleAssistant, Content: "Now update main.go:"},
		domain.Message{ID: "old2", Role: domain.RoleAssistant, Content: "\n\n"},
		domain.Message{ID: "m_asst", Role: domain.RoleAssistant},
	)

	c = app.persistHydration(c, hydrationPositionMsgs(), "m_asst")

	if len(c.Messages) != 5 {
		t.Fatalf("len(Messages) = %d, want 5", len(c.Messages))
	}
	if c.Messages[0].ID != "handover" {
		t.Fatalf("Messages[0] = %s, want handover", c.Messages[0].ID)
	}
	hyd := c.Messages[1]
	if hyd.Role != domain.RoleAssistant || len(hyd.ToolCalls) != 1 ||
		!strings.HasPrefix(hyd.ToolCalls[0].ID, domain.HydrateToolCallPrefix) {
		t.Fatalf("Messages[1] is not the hydration checkpoint: %+v", hyd)
	}
	if c.Messages[2].ID != "old1" || c.Messages[3].ID != "old2" || c.Messages[4].ID != "m_asst" {
		t.Fatalf("kept assistants shifted wrongly: %+v %+v %+v", c.Messages[2].ID, c.Messages[3].ID, c.Messages[4].ID)
	}

	msgs := chatMessages(c, "m_asst", ModelCapabilities{})
	if len(msgs) < 3 || msgs[0].Role != "user" || msgs[1].Role != "assistant" ||
		len(msgs[1].ToolCalls) == 0 || !domain.IsHydrationCallID(msgs[1].ToolCalls[0].ID) {
		t.Fatalf("provider order must start user → hydration, got %+v", rolesOf(msgs))
	}
}

func rolesOf(msgs []ChatMessage) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Role
	}
	return out
}

type freshTurnStreamAdapter struct {
	first *core.Request
}

func (a *freshTurnStreamAdapter) Name() string { return "fresh-turn" }
func (a *freshTurnStreamAdapter) Chat(context.Context, *core.Request) (*core.Response, error) {
	return &core.Response{Blocks: []core.Block{core.TextBlock{Text: "hello"}}, FinishReason: core.FinishReasonStop}, nil
}
func (a *freshTurnStreamAdapter) Stream(ctx context.Context, req *core.Request) (core.Stream, error) {
	if a.first == nil {
		a.first = req
	}
	return &stubStream{events: coreResponseEvents(&core.Response{
		Blocks:       []core.Block{core.TextBlock{Text: "hello"}},
		FinishReason: core.FinishReasonStop,
	})}, nil
}

// TestFreshTurnHydrationSitsBetweenUserAndAssistant pins the never-compacted
// room: user → hydration checkpoint → first working assistant. conv_f16
// already had this shape; the post-compaction insert-after-last-user change
// must not move the checkpoint past the placeholder on a fresh turn.
func TestFreshTurnHydrationSitsBetweenUserAndAssistant(t *testing.T) {
	conv := &domain.Conversation{
		ID: "c_fresh",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "halo", Status: domain.StatusDone},
			{ID: "a1", Role: domain.RoleAssistant},
		},
	}
	adapter := &freshTurnStreamAdapter{}
	settings := domain.DefaultSettings()
	settings.CompactionEnabled = false
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c_fresh": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Toolbox:       &recordingToolbox{},
		Settings:      &fakeSettingsStore{settings: settings},
		Factory: func(context.Context, *domain.Provider, string) (core.Provider, error) {
			return adapter, nil
		},
		runs: map[string]*TurnRun{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c_fresh", Ctx: ctx, Cancel: cancel}
	app.runTurn(run, &domain.Provider{ID: "p", Kind: domain.ProviderChat}, "key", "model", "", "a1", false, ModelCapabilities{})

	if len(conv.Messages) < 3 {
		t.Fatalf("len(Messages) = %d, want user + hydration + assistant", len(conv.Messages))
	}
	if conv.Messages[0].ID != "u1" || !isHydrationMessage(conv.Messages[1]) || conv.Messages[2].ID != "a1" {
		t.Fatalf("fresh transcript order = %s %v %s, want user, hydration, a1",
			conv.Messages[0].ID, isHydrationMessage(conv.Messages[1]), conv.Messages[2].ID)
	}
	if conv.Messages[2].Content != "hello" {
		t.Fatalf("assistant content = %q", conv.Messages[2].Content)
	}
	if adapter.first == nil || !coreHasHydration(adapter.first.Messages) {
		t.Fatal("first stream must include the hydration checkpoint")
	}
	msgs := adapter.first.Messages
	start := 0
	if len(msgs) > 0 && msgs[0].Role == core.RoleSystem {
		start = 1
	}
	if len(msgs) < start+2 || msgs[start].Role != core.RoleUser || msgs[start+1].Role != core.RoleAssistant {
		t.Fatalf("first stream prefix after system = %+v, want user then hydration assistant", msgs[start:])
	}
	hyd := false
	for _, b := range msgs[start+1].Blocks {
		if tc, ok := b.(core.ToolUseBlock); ok && domain.IsHydrationCallID(tc.ID) {
			hyd = true
			break
		}
	}
	if !hyd {
		t.Fatal("message after the user must be the hydration assistant")
	}
}
