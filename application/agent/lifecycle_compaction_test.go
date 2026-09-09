package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"nusashell/application/tools"
	"nusashell/domain"
	"nusashell/infrastructure/ai/core"
)

// compactionTestSummary is long enough to pass CompactionSummaryMinChars.
var compactionTestSummary = strings.Repeat("handoff checkpoint with enough detail to pass the guard. ", 6)

type compactionSettings struct {
	s domain.Settings
}

func (c compactionSettings) Get() domain.Settings { return c.s }

type compactionChatAdapter struct {
	mu                sync.Mutex
	toolCallSummaries []string
	err               error
	calls             int
}

func (a *compactionChatAdapter) Name() string { return "compaction-chat" }
func (a *compactionChatAdapter) Stream(context.Context, *core.Request) (core.Stream, error) {
	return nil, fmt.Errorf("stream not used")
}
func (a *compactionChatAdapter) Chat(_ context.Context, _ *core.Request) (*core.Response, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	if a.err != nil {
		return nil, a.err
	}
	idx := a.calls - 1
	summary := compactionTestSummary
	if len(a.toolCallSummaries) > 0 {
		summary = a.toolCallSummaries[min(idx, len(a.toolCallSummaries)-1)]
	}
	args, _ := json.Marshal(map[string]string{"text": summary})
	return &core.Response{
		Blocks: []core.Block{core.ToolUseBlock{
			ID:        fmt.Sprintf("summary_%d", idx),
			Name:      compactionSummaryToolName,
			Arguments: args,
		}},
		FinishReason: core.FinishReasonToolCall,
	}, nil
}

type noopEmitter struct{}

func (noopEmitter) Emit(string, any) {}

// TestLifecycleHeadlessMidToolCompactionObserverCoherent drives a headless
// automation tool-round that crosses the mid-tool compaction trigger and
// asserts the observer still sees a coherent lifecycle stream: run+step pre,
// per-round reasoning/text, tool_call pre/post pairs with CallIDs (no
// duplicates from compaction), and step+run post whose Detail equals the
// post-compaction final assistant content.
func TestLifecycleHeadlessMidToolCompactionObserverCoherent(t *testing.T) {
	body := strings.Repeat("abcdefghij", 40) // ~400 chars ≈ 100 tokens
	const inFlightID = "msg-inflight-life"
	const convID = "conv-life-compact"
	const finalOut = `{"output":"post-compaction final answer"}`

	msgs := make([]domain.Message, 0, 42)
	for i := 0; i < 40; i++ {
		msgs = append(msgs, domain.Message{
			ID: fmt.Sprintf("u%d", i), Role: domain.RoleUser, Content: body, Status: domain.StatusDone,
		})
	}
	msgs = append(msgs, domain.Message{
		ID: inFlightID, Role: domain.RoleAssistant, Content: "reading now", Status: domain.StatusDone,
		ToolCalls: []domain.ToolCall{{ID: "call-life-1", Name: "alpha", Args: `{"path":"/x"}`}},
	})
	conv := &domain.Conversation{ID: convID, Status: "running", Type: domain.ConversationTypeAutomation, Messages: msgs}
	store := &lifecycleConvStore{byID: map[string]*domain.Conversation{convID: conv}}

	settings := domain.DefaultSettings()
	settings.CompactionEnabled = true
	settings.MaxParallelTools = 2

	box := &stubToolbox{}
	adapter := &compactionChatAdapter{toolCallSummaries: []string{compactionTestSummary}}
	svc := New(Deps{
		Conversations: store,
		Toolbox:       box,
		Settings:      compactionSettings{s: settings},
		Bus:           noopEmitter{},
	})
	obs := &recordingObserver{}
	svc.RegisterAgentObserver(obs)

	run := &TurnRun{
		ID: "run-life-compact", ConversationID: convID,
		Headless: true, ToolKind: tools.AgentAutomation,
		Ctx: context.Background(),
	}
	svc.runsMu.Lock()
	svc.runs[run.ID] = run
	svc.runsMu.Unlock()

	provider := &domain.Provider{Models: []domain.Model{{ID: "model", Context: 4000}}}
	pc := NewProviderContext(provider, adapter)
	// ProviderChat Kind is set by NewProviderContext from empty Kind — force chat.
	pc.Kind = domain.ProviderChat
	p := svc.ConversationRulesForTest(run, pc, conv, settings, provider, "model", inFlightID, 2)

	// Headless turn boundary + round content (as Stream would emit before Execute).
	svc.emitRunStepPre(run)
	svc.emitRoundContentObservers(run, 2, "planning the read", "reading now")

	calls := []domain.ToolCall{{ID: "call-life-1", Name: "alpha", Args: `{"path":"/x"}`}}
	if _, err := p.Rules().Execute(&RoundState{}, ChatResponse{
		Content: "reading now", Reasoning: "planning the read", ToolCalls: calls,
	}, calls); err != nil {
		t.Fatalf("Execute after mid-tool compaction: %v", err)
	}
	if got := p.CompactionAttempts(); got != 1 {
		t.Fatalf("compactionAttempts = %d, want 1 (mid-tool must have run)", got)
	}
	if !domain.IsCompactionSummary(p.Conv().Messages[0].Content) {
		t.Fatalf("expected compaction handover first after mid-tool, got %+v", p.Conv().Messages[0])
	}

	// Terminal round content + headless post with the final validated output.
	svc.emitRoundContentObservers(run, 3, "wrapping up", finalOut)
	svc.emitRunStepPost(run, AgentStatusOK, "", finalOut)

	obs.mu.Lock()
	defer obs.mu.Unlock()
	events := obs.events

	type key struct{ kind, phase, callID string }
	seen := map[key]int{}
	for _, e := range events {
		if e.ConversationID != convID || e.RunID != run.ID {
			t.Fatalf("identity missing on %+v", e)
		}
		seen[key{e.Kind, e.Phase, e.CallID}]++
	}
	want := []key{
		{AgentEventRun, AgentPhasePre, ""},
		{AgentEventStep, AgentPhasePre, ""},
		{AgentEventReasoning, AgentPhasePost, ""}, // round 2
		{AgentEventText, AgentPhasePost, ""},
		{AgentEventToolCall, AgentPhasePre, "call-life-1"},
		{AgentEventToolCall, AgentPhasePost, "call-life-1"},
		{AgentEventReasoning, AgentPhasePost, ""}, // round 3
		{AgentEventText, AgentPhasePost, ""},
		{AgentEventStep, AgentPhasePost, ""},
		{AgentEventRun, AgentPhasePost, ""},
	}
	// Reasoning/text appear twice (rounds 2 and 3); count them separately.
	if seen[key{AgentEventRun, AgentPhasePre, ""}] != 1 ||
		seen[key{AgentEventStep, AgentPhasePre, ""}] != 1 ||
		seen[key{AgentEventToolCall, AgentPhasePre, "call-life-1"}] != 1 ||
		seen[key{AgentEventToolCall, AgentPhasePost, "call-life-1"}] != 1 ||
		seen[key{AgentEventStep, AgentPhasePost, ""}] != 1 ||
		seen[key{AgentEventRun, AgentPhasePost, ""}] != 1 {
		t.Fatalf("lifecycle pairing broken after compaction: seen=%v summary=%v want keys=%v",
			seen, eventSummary(events), want)
	}
	if seen[key{AgentEventReasoning, AgentPhasePost, ""}] != 2 {
		t.Fatalf("reasoning events = %d, want 2 (one per round); summary=%v",
			seen[key{AgentEventReasoning, AgentPhasePost, ""}], eventSummary(events))
	}
	if seen[key{AgentEventText, AgentPhasePost, ""}] != 2 {
		t.Fatalf("text events = %d, want 2; summary=%v",
			seen[key{AgentEventText, AgentPhasePost, ""}], eventSummary(events))
	}

	var stepPost AgentLifecycleEvent
	for _, e := range events {
		if e.Kind == AgentEventStep && e.Phase == AgentPhasePost {
			stepPost = e
		}
	}
	if stepPost.Detail != finalOut {
		t.Fatalf("step post Detail = %q, want post-compaction final %q", stepPost.Detail, finalOut)
	}
	if stepPost.Status != AgentStatusOK {
		t.Fatalf("step post Status = %q, want ok", stepPost.Status)
	}

	box.mu.Lock()
	defer box.mu.Unlock()
	var alpha int
	for _, name := range box.calls {
		if name == "alpha" {
			alpha++
		}
	}
	// Compaction re-hydration may call discovery tools (skill/mcp_list) through
	// the same Toolbox; the headless tool round must still execute alpha once.
	if alpha != 1 {
		t.Fatalf("alpha executions = %d in %v, want 1", alpha, box.calls)
	}
}

// TestLifecycleThinkingFoldsIntoReasoningObserver pins the observer contract
// for thinking/reasoning: visible thinking text is AgentEventReasoning via
// emitRoundContentObservers (same seam as conversationRules.Stream →
// resp.Reasoning from core.Response.Reasoning()). There is no separate
// "thinking" kind — matching the UI round-stream Thinking disclosure, which
// renders Reasoning text, not ReasoningExtra.
func TestLifecycleThinkingFoldsIntoReasoningObserver(t *testing.T) {
	svc := New(Deps{})
	obs := &recordingObserver{}
	svc.RegisterAgentObserver(obs)
	run := &TurnRun{
		ID: "run-think", ConversationID: "conv-think",
		Headless: true, ToolKind: tools.AgentAutomation,
		Ctx: context.Background(),
	}

	thinking := "I will inspect the file before answering"
	svc.emitRoundContentObservers(run, 1, thinking, "done")

	obs.mu.Lock()
	defer obs.mu.Unlock()
	var kinds []string
	var reasoning AgentLifecycleEvent
	for _, e := range obs.events {
		kinds = append(kinds, e.Kind)
		if e.Kind == AgentEventReasoning {
			reasoning = e
		}
	}
	for _, k := range kinds {
		if k == "thinking" {
			t.Fatalf("unexpected separate thinking kind in %v", kinds)
		}
	}
	if reasoning.Detail != thinking || reasoning.Phase != AgentPhasePost || reasoning.Round != 1 {
		t.Fatalf("thinking must fold into reasoning event, got %+v", reasoning)
	}
}

// TestLifecycleReasoningExtraDoesNotEmitObserverEvent documents that opaque
// provider ReasoningExtra (encrypted replay state) is not observer-visible.
// Only the plaintext Reasoning string becomes AgentEventReasoning Detail.
func TestLifecycleReasoningExtraDoesNotEmitObserverEvent(t *testing.T) {
	svc := New(Deps{})
	obs := &recordingObserver{}
	svc.RegisterAgentObserver(obs)
	run := &TurnRun{
		ID: "run-extra", ConversationID: "conv-extra",
		Headless: true, ToolKind: tools.AgentAutomation,
		Ctx: context.Background(),
	}

	// Simulate a completed round whose ChatResponse carries only opaque Extra
	// (empty Reasoning): Stream still calls emitRoundContentObservers with the
	// Reasoning string field, so observers must stay silent for reasoning.
	svc.emitRoundContentObservers(run, 1, "", "visible text")
	_ = json.RawMessage(`{"type":"reasoning","encrypted_content":"opaque"}`) // fixture shape; not emitted

	obs.mu.Lock()
	defer obs.mu.Unlock()
	for _, e := range obs.events {
		if e.Kind == AgentEventReasoning {
			t.Fatalf("ReasoningExtra-only / empty Reasoning must not emit reasoning: %+v", e)
		}
		if e.Kind == "thinking" {
			t.Fatalf("no thinking kind expected: %+v", e)
		}
	}
	if len(obs.events) != 1 || obs.events[0].Kind != AgentEventText {
		t.Fatalf("events = %v, want single text", eventSummary(obs.events))
	}
}
