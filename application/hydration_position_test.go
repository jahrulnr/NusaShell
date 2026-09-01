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
	c := app.persistHydration(hydrationPositionFixture(), hydrationPositionMsgs())

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
	// The checkpoint must still be detected so later rounds reuse it
	// (the domain-level predicate pins this; see domain/hydration.go).
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

	out := app.persistHydration(c, hydrationPositionMsgs())

	if len(out.Messages) != 2 {
		t.Fatalf("len = %d, want 2", len(out.Messages))
	}
	last := out.Messages[1]
	if last.Role != domain.RoleAssistant || len(last.ToolCalls) == 0 ||
		!strings.HasPrefix(last.ToolCalls[0].ID, domain.HydrateToolCallPrefix) {
		t.Fatalf("fallback did not append the checkpoint: %+v", out.Messages)
	}
}

// TestPersistHydrationDoesNotInsertWithoutUser pins the OpenAI/Claude
// constraint: an assistant+tool turn cannot sit under the system prompt with
// no user message yet. Workspace pick on an empty room used to persist the
// checkpoint at index 0; the first user was then appended after it.
func TestPersistHydrationDoesNotInsertWithoutUser(t *testing.T) {
	app := &App{}
	c := domain.NewConversation("conv_empty", "empty")

	out := app.persistHydration(c, hydrationPositionMsgs())

	if len(out.Messages) != 0 {
		t.Fatalf("empty room must not persist hydration before a user exists, got %d messages: %+v", len(out.Messages), out.Messages)
	}
}

// TestChatMessagesEmitsUserBeforeLeadingHydration is the wire invariant
// OpenAI Chat Completions and Anthropic Messages require: after the system
// prompt, the first history role is user, then the hydration assistant+tools.
// A checkpoint persisted at index 0 (empty-room workspace pick) must not be
// replayed in that leading position.
func TestChatMessagesEmitsUserBeforeLeadingHydration(t *testing.T) {
	c := &domain.Conversation{
		ID: "c_lead",
		Messages: []domain.Message{
			hydrationCheckpointMessage(),
			{ID: "u1", Role: domain.RoleUser, Content: "halo", Status: domain.StatusDone},
			{ID: "a1", Role: domain.RoleAssistant, Content: "work", Status: domain.StatusDone},
		},
	}
	msgs := chatMessages(c, "", ModelCapabilities{})
	if len(msgs) < 2 {
		t.Fatalf("got %d provider messages, want at least user + hydration", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Fatalf("provider messages[0] role = %q, want user (got %+v)", msgs[0].Role, rolesOf(msgs))
	}
	if msgs[1].Role != "assistant" || len(msgs[1].ToolCalls) == 0 ||
		!domain.IsHydrationCallID(msgs[1].ToolCalls[0].ID) {
		t.Fatalf("provider messages[1] must be the hydration assistant, got %+v", msgs[1])
	}
}

// TestRelocateHydrationMovesCheckpointAfterFirstUser pins the stored
// transcript repair: [hydration, user, work] becomes [user, hydration, work]
// without rebuilding the checkpoint (prompt-cache IDs stay put).
func TestRelocateHydrationMovesCheckpointAfterFirstUser(t *testing.T) {
	hyd := hydrationCheckpointMessage()
	msgs := []domain.Message{
		hyd,
		{ID: "u1", Role: domain.RoleUser, Content: "halo", Status: domain.StatusDone},
		{ID: "a1", Role: domain.RoleAssistant, Content: "work", Status: domain.StatusDone},
	}
	got := domain.RelocateHydrationAfterFirstUser(msgs)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].ID != "u1" || !domain.IsHydrationMessage(got[1]) || got[2].ID != "a1" {
		t.Fatalf("order = %s hyd=%v %s, want user, hydration, work", got[0].ID, domain.IsHydrationMessage(got[1]), got[2].ID)
	}
	if got[1].ID != hyd.ID {
		t.Fatalf("checkpoint was rebuilt (id %s → %s); repair must move the existing message", hyd.ID, got[1].ID)
	}
}

// TestRelocateHydrationIsNoOpWhenAlreadyAfterUser keeps a correctly parked
// checkpoint from bouncing around (which would bust the prompt-cache prefix).
func TestRelocateHydrationIsNoOpWhenAlreadyAfterUser(t *testing.T) {
	msgs := []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "halo", Status: domain.StatusDone},
		hydrationCheckpointMessage(),
		{ID: "a1", Role: domain.RoleAssistant, Content: "work", Status: domain.StatusDone},
	}
	got := domain.RelocateHydrationAfterFirstUser(msgs)
	if len(got) != 3 || got[0].ID != "u1" || !domain.IsHydrationMessage(got[1]) || got[2].ID != "a1" {
		t.Fatalf("correct order must stay put: %+v", got)
	}
}

// TestPersistHydrationKeepsRoundTwoPrefixStable verifies the prompt-cache
// property that motivated the insertion point: once round 1's assistant
// message is filled in place, the provider prefix up to and including the
// checkpoint is identical to round 1's — the history only ever grows.
func TestPersistHydrationKeepsRoundTwoPrefixStable(t *testing.T) {
	app := &App{}
	c := app.persistHydration(hydrationPositionFixture(), hydrationPositionMsgs())

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
		domain.Message{ID: "handover", Role: domain.RoleUser, Content: "[COMPACTION CHECKPOINT]\n## Goal"},
		domain.Message{ID: "old1", Role: domain.RoleAssistant, Content: "Now update main.go:"},
		domain.Message{ID: "old2", Role: domain.RoleAssistant, Content: "\n\n"},
		domain.Message{ID: "m_asst", Role: domain.RoleAssistant},
	)

	c = app.persistHydration(c, hydrationPositionMsgs())

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
// room: user → hydration checkpoint → first working assistant. Hydration is
// appended in addTurnMessages before the placeholder so Save stays
// append-only; runTurn must not move it past the assistant.
func TestFreshTurnHydrationSitsBetweenUserAndAssistant(t *testing.T) {
	conv := &domain.Conversation{ID: "c_fresh"}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c_fresh": conv}}
	adapter := &freshTurnStreamAdapter{}
	settings := domain.DefaultSettings()
	settings.CompactionEnabled = false
	app := &App{
		Conversations: store,
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Toolbox:       &recordingToolbox{},
		Settings:      &fakeSettingsStore{settings: settings},
		Factory: func(context.Context, *domain.Provider, string) (core.Provider, error) {
			return adapter, nil
		},
		runs: map[string]*TurnRun{},
	}
	app.addTurnMessages(conv,
		domain.Message{ID: "u1", Role: domain.RoleUser, Content: "halo", Status: domain.StatusDone},
		domain.Message{ID: "a1", Role: domain.RoleAssistant},
	)
	if err := bindConversation(store, conv).Save(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := &TurnRun{ID: "r1", ConversationID: "c_fresh", Ctx: ctx, Cancel: cancel}
	app.runTurn(run, &domain.Provider{ID: "p", Kind: domain.ProviderChat}, "key", "model", "", "a1", false, ModelCapabilities{})

	saved, err := app.Conversations.Get("c_fresh")
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Messages) < 3 {
		t.Fatalf("len(Messages) = %d, want user + hydration + assistant", len(saved.Messages))
	}
	if saved.Messages[0].ID != "u1" || !domain.IsHydrationMessage(saved.Messages[1]) || saved.Messages[2].ID != "a1" {
		t.Fatalf("fresh transcript order = %s %v %s, want user, hydration, a1",
			saved.Messages[0].ID, domain.IsHydrationMessage(saved.Messages[1]), saved.Messages[2].ID)
	}
	if saved.Messages[2].Content != "hello" {
		t.Fatalf("assistant content = %q", saved.Messages[2].Content)
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

// TestExecuteTurnToolsKeepsHydrationOnBriefChange pins the cache-poison fix:
// a todo tool call that changes the brief must NOT strip the persisted
// hydration checkpoint. The checkpoint's todo_list brief is frozen until the
// next compaction epoch; the agent can call todo/todo_list live. Stripping +
// rebuilding relocated the checkpoint after whatever user was last (the
// poison dump's "." follow-up), breaking the prompt-cache prefix.
func TestExecuteTurnToolsKeepsHydrationOnBriefChange(t *testing.T) {
	todos := &fakeTodoPort{briefs: map[string]string{"c1": "old brief"}}
	box := &briefMutatingToolbox{todos: todos, newBrief: "## Objective\nnew\n## Done when\nnew"}
	conv := &domain.Conversation{
		ID: "c1",
		Messages: []domain.Message{
			hydrationCheckpointMessage(),
			{ID: "m1", Role: domain.RoleAssistant, ToolCalls: []domain.ToolCall{{ID: "t1", Name: "todo", Args: `{}`}}},
		},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Toolbox:       box,
		Todos:         todos,
	}
	run := &TurnRun{ID: "r1", ConversationID: "c1", Ctx: WithConversationID(context.Background(), "c1"), Cancel: func() {}}

	if err := app.executeTurnTools(run, "m1", conv.Messages[1].ToolCalls, ModelCapabilities{Vision: true}, domain.Settings{}, 1); err != nil {
		t.Fatalf("executeTurnTools: %v", err)
	}
	// The hydration checkpoint must still be present.
	found := false
	for _, m := range conv.Messages {
		for _, tc := range m.ToolCalls {
			if domain.IsHydrationCallID(tc.ID) {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("hydration checkpoint was stripped on brief change; it must stay frozen until compaction")
	}
}

// TestPersistCompactedConversationInsertsHydrationAfterHandover pins the
// post-compaction epoch: persistCompactedConversation rebuilds the checkpoint
// in the SAME Save as Compact, immediately after the handover user — not after
// a later steer user that may live in the retained suffix. The old
// last-user-before-placeholder index parked the checkpoint after the steer,
// relocating a volatile prefix into the middle of the cached transcript.
func TestPersistCompactedConversationInsertsHydrationAfterHandover(t *testing.T) {
	// Retained suffix contains a steer user after prior assistant work. The
	// handover is the epoch anchor; hydration must follow it, not the steer.
	conv := &domain.Conversation{
		ID: "c_compact",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "first question", Status: domain.StatusDone},
			{ID: "a1", Role: domain.RoleAssistant, Content: "doing work", Status: domain.StatusDone},
			{ID: "steer", Role: domain.RoleUser, Content: "now do X", Status: domain.StatusDone},
			{ID: "a2", Role: domain.RoleAssistant, Content: "ok", Status: domain.StatusDone},
			{ID: "pending", Role: domain.RoleAssistant},
		},
	}
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c_compact": conv}},
		Logs:          &fakeLogStore{},
		Bus:           NewBus(),
		Toolbox:       &recordingToolbox{},
	}

	if err := app.persistCompactedConversation(conv, "summary text", 1_000_000); err != nil {
		t.Fatalf("persistCompactedConversation: %v", err)
	}

	// Compact put the handover user at messages[0]; hydration must be at [1].
	if len(conv.Messages) < 3 {
		t.Fatalf("len(Messages) = %d, want handover + hydration + retained", len(conv.Messages))
	}
	if conv.Messages[0].Role != domain.RoleUser {
		t.Fatalf("Messages[0] role = %s, want user (handover)", conv.Messages[0].Role)
	}
	if !domain.IsHydrationMessage(conv.Messages[1]) {
		t.Fatalf("Messages[1] must be the hydration checkpoint, got %+v", conv.Messages[1])
	}
	// Exactly one checkpoint — no second one parked after the steer user.
	count := 0
	hydIdx := -1
	steerIdx := -1
	for i, m := range conv.Messages {
		if domain.IsHydrationMessage(m) {
			count++
			hydIdx = i
		}
		if m.ID == "steer" {
			steerIdx = i
		}
	}
	if count != 1 {
		t.Fatalf("want exactly 1 hydration checkpoint, got %d", count)
	}
	if steerIdx >= 0 && steerIdx < hydIdx {
		t.Fatalf("steer user at %d must not precede hydration at %d (would park checkpoint after the steer)", steerIdx, hydIdx)
	}
}

// TestFollowUpUserTurnDoesNotRelocateHydration pins the prompt-cache invariant
// on a follow-up turn: a transcript that already has a checkpoint after user1
// (user1 → hydration → work → user2 → placeholder) must keep it there. The
// turn loop no longer touches hydration, so the first Stream reads the frozen
// transcript and the cache prefix up to the checkpoint stays intact.
func TestFollowUpUserTurnDoesNotRelocateHydration(t *testing.T) {
	conv := &domain.Conversation{
		ID: "c_followup",
		Messages: []domain.Message{
			{ID: "u1", Role: domain.RoleUser, Content: "halo", Status: domain.StatusDone},
			hydrationCheckpointMessage(),
			{ID: "a1", Role: domain.RoleAssistant, Content: "work", Status: domain.StatusDone},
			{ID: "u2", Role: domain.RoleUser, Content: "follow up", Status: domain.StatusDone},
			{ID: "a2", Role: domain.RoleAssistant},
		},
	}
	adapter := &freshTurnStreamAdapter{}
	settings := domain.DefaultSettings()
	settings.CompactionEnabled = false
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c_followup": conv}},
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
	run := &TurnRun{ID: "r1", ConversationID: "c_followup", Ctx: ctx, Cancel: cancel}
	app.runTurn(run, &domain.Provider{ID: "p", Kind: domain.ProviderChat}, "key", "model", "", "a2", false, ModelCapabilities{})

	// The checkpoint must still sit immediately after u1, not after u2.
	count := 0
	hydIdx := -1
	u2Idx := -1
	for i, m := range conv.Messages {
		if domain.IsHydrationMessage(m) {
			count++
			hydIdx = i
		}
		if m.ID == "u2" {
			u2Idx = i
		}
	}
	if count != 1 {
		t.Fatalf("want exactly 1 hydration checkpoint, got %d (follow-up must not add another)", count)
	}
	if u2Idx >= 0 && hydIdx > u2Idx {
		t.Fatalf("hydration at %d relocated after u2 at %d; it must stay after u1", hydIdx, u2Idx)
	}
}

// TestFreshTurnRepairsHydrationLeadingTheUser is the live shape from an
// older empty-room workspace pick: checkpoint at index 0, then the first
// user. Formed IDs stay put (append-only). chatMessages relocates the
// checkpoint so the first Stream still sends user → hydration.
func TestFreshTurnRepairsHydrationLeadingTheUser(t *testing.T) {
	conv := &domain.Conversation{
		ID: "c_lead_turn",
		Messages: []domain.Message{
			hydrationCheckpointMessage(),
			{ID: "u1", Role: domain.RoleUser, Content: "halo", Status: domain.StatusDone},
			{ID: "a1", Role: domain.RoleAssistant},
		},
	}
	adapter := &freshTurnStreamAdapter{}
	settings := domain.DefaultSettings()
	settings.CompactionEnabled = false
	app := &App{
		Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c_lead_turn": conv}},
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
	run := &TurnRun{ID: "r1", ConversationID: "c_lead_turn", Ctx: ctx, Cancel: cancel}
	app.runTurn(run, &domain.Provider{ID: "p", Kind: domain.ProviderChat}, "key", "model", "", "a1", false, ModelCapabilities{})

	saved, err := app.Conversations.Get("c_lead_turn")
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Messages) < 3 {
		t.Fatalf("len(Messages) = %d, want hydration + user + assistant", len(saved.Messages))
	}
	if !domain.IsHydrationMessage(saved.Messages[0]) || saved.Messages[1].ID != "u1" || saved.Messages[2].ID != "a1" {
		t.Fatalf("persisted order = hyd=%v %s %s, want leading hydration, u1, a1",
			domain.IsHydrationMessage(saved.Messages[0]), saved.Messages[1].ID, saved.Messages[2].ID)
	}
	if saved.Messages[2].Content != "hello" {
		t.Fatalf("assistant content = %q", saved.Messages[2].Content)
	}
	if adapter.first == nil {
		t.Fatal("first stream was not captured")
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
