package application

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

// TestMidToolCompactionRunsAtToolRequestBoundary covers the tool-spam edge
// case: the model requests a tool round, the round is persisted, and the
// estimated context (requested calls included, tool outputs not yet) already
// crosses the trigger. Compaction must run at the tool-request boundary and
// preserve the in-flight assistant message verbatim so the pending outputs
// are patched into the live tail instead of being lost by strip retention.
func TestMidToolCompactionRunsAtToolRequestBoundary(t *testing.T) {
	body := strings.Repeat("abcdefghij", 40) // ~400 chars ≈ 100 tokens
	msgs := make([]domain.Message, 0, 42)
	for i := 0; i < 40; i++ {
		msgs = append(msgs, domain.Message{ID: fmt.Sprintf("u%d", i), Role: domain.RoleUser, Content: body, Status: domain.StatusDone})
	}
	const inFlightID = "msg-inflight"
	msgs = append(msgs, domain.Message{
		ID:        inFlightID,
		Role:      domain.RoleAssistant,
		Content:   "reading now",
		Status:    domain.StatusDone,
		ToolCalls: []domain.ToolCall{{ID: "call-1", Name: "read", Args: `{"path":"/x"}`}},
	})
	conv := &domain.Conversation{ID: "c1", Messages: msgs}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c1": conv}}
	adapter := &recordingCompleteAdapter{toolCallSummaries: []string{validTestSummary}}
	bus := NewBus()
	_, events, unsubscribe := bus.Subscribe()
	defer unsubscribe()
	app := &App{Conversations: store, Logs: &fakeLogStore{}, Bus: bus}
	settings := domain.DefaultSettings()
	settings.CompactionEnabled = true
	provider := &domain.Provider{Models: []domain.Model{{ID: "model", Context: 4000}}}
	run := &TurnRun{ID: "run1", ConversationID: "c1", Ctx: context.Background()}
	p := &conversationRules{
		a:            app,
		run:          run,
		adapter:      stubProviderContext(adapter),
		conv:         conv,
		settings:     settings,
		provider:     provider,
		model:        "model",
		currentMsgID: inFlightID,
		round:        2,
	}

	if !p.tryMidToolCompaction() {
		t.Fatal("mid-tool compaction did not run at the tool-request boundary")
	}

	// The compaction lifecycle events fired (started then compacted). The
	// bus carries unrelated events (logs.append per log line), so scan the
	// channel until the relevant event arrives.
	gotCompacted := false
	deadline := time.After(3 * time.Second)
	for !gotCompacted {
		select {
		case ev := <-events:
			if ev.Type == contracts.EventCompacted {
				gotCompacted = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for agent.compacted event")
		}
	}

	// The store now carries the compaction handover as the first message.
	saved, err := app.Conversations.Get("c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Messages) == 0 || !domain.IsCompactionSummary(saved.Messages[0].Content) {
		t.Fatalf("compaction handover not persisted first: %+v", saved.Messages[:1])
	}

	// The in-flight round survived verbatim: its tool calls are still present
	// so executeTurnTools can patch the outputs in afterwards.
	var kept *domain.Message
	for i := range saved.Messages {
		if saved.Messages[i].ID == inFlightID {
			kept = &saved.Messages[i]
		}
	}
	if kept == nil {
		t.Fatal("in-flight assistant message was dropped by compaction")
	}
	if len(kept.ToolCalls) != 1 || kept.ToolCalls[0].ID != "call-1" {
		t.Fatalf("in-flight tool calls not preserved verbatim: %+v", kept.ToolCalls)
	}

	// The in-memory conversation was refreshed (the hook's estimate now sees
	// the compacted state, not the stale one).
	if p.conv == conv || !domain.IsCompactionSummary(p.conv.Messages[0].Content) {
		t.Fatal("p.conv was not refreshed to the compacted conversation")
	}
	if got, want := p.compactionAttempts, 1; got != want {
		t.Fatalf("compactionAttempts = %d, want %d", got, want)
	}
}

// TestMidToolCompactionSkipsBelowTrigger: when the estimate is still below
// the trigger at the tool-request boundary, compaction must not run — the
// proactive/emergency hooks stay the only safety nets.
func TestMidToolCompactionSkipsBelowTrigger(t *testing.T) {
	conv := &domain.Conversation{ID: "c2", Messages: []domain.Message{
		{ID: "u1", Role: domain.RoleUser, Content: "small", Status: domain.StatusDone},
	}}
	store := &fakeConvStore{convs: map[string]*domain.Conversation{"c2": conv}}
	app := &App{Conversations: store, Logs: &fakeLogStore{}, Bus: NewBus()}
	settings := domain.DefaultSettings()
	settings.CompactionEnabled = true
	p := &conversationRules{
		a:        app,
		run:      &TurnRun{ID: "run2", ConversationID: "c2", Ctx: context.Background()},
		conv:     conv,
		settings: settings,
		provider: &domain.Provider{Models: []domain.Model{{ID: "model", Context: 4000}}},
		model:    "model",
		round:    2,
	}
	if p.tryMidToolCompaction() {
		t.Fatal("mid-tool compaction ran below the trigger")
	}
	if got := len(conv.Messages); got != 1 {
		t.Fatalf("messages = %d, want 1 (conversation untouched)", got)
	}
}

// TestMidToolCompactionSkipsFirstRound keeps the tool-request hook inert on
// the first round (round 1 = turn start, already covered by initializeTurn).
func TestMidToolCompactionSkipsFirstRound(t *testing.T) {
	body := strings.Repeat("abcdefghij", 40)
	msgs := make([]domain.Message, 0, 42)
	for i := 0; i < 40; i++ {
		msgs = append(msgs, domain.Message{ID: fmt.Sprintf("u%d", i), Role: domain.RoleUser, Content: body, Status: domain.StatusDone})
	}
	conv := &domain.Conversation{ID: "c3", Messages: msgs}
	app := &App{Conversations: &fakeConvStore{convs: map[string]*domain.Conversation{"c3": conv}}, Logs: &fakeLogStore{}, Bus: NewBus()}
	settings := domain.DefaultSettings()
	settings.CompactionEnabled = true
	p := &conversationRules{
		a:        app,
		run:      &TurnRun{ID: "run3", ConversationID: "c3", Ctx: context.Background()},
		conv:     conv,
		settings: settings,
		provider: &domain.Provider{Models: []domain.Model{{ID: "model", Context: 4000}}},
		model:    "model",
		round:    1,
	}
	if p.tryMidToolCompaction() {
		t.Fatal("mid-tool compaction ran on the first round")
	}
}
