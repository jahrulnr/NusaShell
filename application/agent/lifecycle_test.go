package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"nusashell/application/tools"
	"nusashell/domain"
)

type lifecycleConvStore struct {
	mu   sync.Mutex
	byID map[string]*domain.Conversation
}

func (s *lifecycleConvStore) List() []*domain.Conversation { return nil }
func (s *lifecycleConvStore) Get(id string) (*domain.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.byID[id]
	if c == nil {
		return nil, nil
	}
	cp := *c
	return &cp, nil
}
func (s *lifecycleConvStore) Save(c *domain.Conversation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byID == nil {
		s.byID = map[string]*domain.Conversation{}
	}
	cp := *c
	s.byID[c.ID] = &cp
	return nil
}
func (s *lifecycleConvStore) Delete(string) error { return nil }
func (s *lifecycleConvStore) ArchiveChunk(string, []domain.Message) (int, error) {
	return 0, nil
}
func (s *lifecycleConvStore) GetChunk(string, int) ([]domain.Message, error) {
	return nil, nil
}

type recordingObserver struct {
	mu     sync.Mutex
	events []AgentLifecycleEvent
	tag    string
	order  *[]string
}

func (o *recordingObserver) OnAgentEvent(_ context.Context, ev AgentLifecycleEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, ev)
	if o.order != nil && o.tag != "" {
		*o.order = append(*o.order, o.tag)
	}
}

func TestObserverRegistryFIFO(t *testing.T) {
	svc := New(Deps{})
	var order []string
	o1 := &recordingObserver{tag: "a", order: &order}
	o2 := &recordingObserver{tag: "b", order: &order}
	svc.RegisterAgentObserver(o1)
	svc.RegisterAgentObserver(o2)

	run := &TurnRun{
		ID: "run_fifo", ConversationID: "conv_fifo",
		Headless: true, ToolKind: tools.AgentAutomation,
		Ctx: context.Background(),
	}
	svc.emitHeadlessEvent(run, AgentLifecycleEvent{
		Kind: AgentEventStep, Phase: AgentPhasePre, Status: AgentStatusRunning,
	})

	if len(order) != 2 || order[0] != "a" || order[1] != "b" {
		t.Fatalf("FIFO order = %v, want [a b]", order)
	}
	o1.mu.Lock()
	o2.mu.Lock()
	defer o1.mu.Unlock()
	defer o2.mu.Unlock()
	if len(o1.events) != 1 || len(o2.events) != 1 {
		t.Fatalf("both observers should receive the event: o1=%d o2=%d", len(o1.events), len(o2.events))
	}
	if o1.events[0].Kind != AgentEventStep || o1.events[0].RunID != "run_fifo" {
		t.Fatalf("event = %+v", o1.events[0])
	}
}

func TestLifecycleSimulatedTurnEmitsPrePostPairs(t *testing.T) {
	store := &lifecycleConvStore{byID: map[string]*domain.Conversation{}}
	svc := New(Deps{Conversations: store})
	obs := &recordingObserver{}
	svc.RegisterAgentObserver(obs)

	run := &TurnRun{
		ID:             "run_life",
		ConversationID: "conv_life",
		Headless:       true,
		ToolKind:       tools.AgentAutomation,
		Ctx:            context.Background(),
	}
	svc.runsMu.Lock()
	svc.runs[run.ID] = run
	svc.runsMu.Unlock()

	svc.emitRunStepPre(run)
	svc.emitRoundContentObservers(run, 1, "first thought", "hello")
	svc.emitToolCallPre(run, 1, domainToolCallRef{ID: "call_a", Name: "file_read", Args: `{"path":"/tmp/x"}`})
	svc.emitToolCallPre(run, 1, domainToolCallRef{ID: "call_b", Name: "exec", Args: `{"cmd":"ls"}`})
	svc.emitToolCallPost(run, 1, domainToolCallRef{ID: "call_a", Name: "file_read"}, AgentStatusOK, "file contents")
	svc.emitToolCallPost(run, 1, domainToolCallRef{ID: "call_b", Name: "exec"}, AgentStatusError, "boom")
	svc.emitRunStepPost(run, AgentStatusOK, "", "final output")

	obs.mu.Lock()
	defer obs.mu.Unlock()
	events := obs.events

	type key struct{ kind, phase, callID string }
	seen := map[key]int{}
	for _, e := range events {
		if e.ConversationID != "conv_life" || e.RunID != "run_life" {
			t.Fatalf("identity missing on %+v", e)
		}
		seen[key{e.Kind, e.Phase, e.CallID}]++
	}
	wantPairs := []key{
		{AgentEventRun, AgentPhasePre, ""},
		{AgentEventStep, AgentPhasePre, ""},
		{AgentEventReasoning, AgentPhasePost, ""},
		{AgentEventText, AgentPhasePost, ""},
		{AgentEventToolCall, AgentPhasePre, "call_a"},
		{AgentEventToolCall, AgentPhasePre, "call_b"},
		{AgentEventToolCall, AgentPhasePost, "call_a"},
		{AgentEventToolCall, AgentPhasePost, "call_b"},
		{AgentEventStep, AgentPhasePost, ""},
		{AgentEventRun, AgentPhasePost, ""},
	}
	for _, k := range wantPairs {
		if seen[k] != 1 {
			t.Fatalf("missing/duplicate %+v in events (count=%d); all=%v", k, seen[k], eventSummary(events))
		}
	}
	// Parallel tool calls keep distinct CallIDs through pre/post.
	var callA, callB int
	for _, e := range events {
		if e.Kind != AgentEventToolCall {
			continue
		}
		switch e.CallID {
		case "call_a":
			callA++
			if e.Phase == AgentPhasePost && e.Status != AgentStatusOK {
				t.Fatalf("call_a post status = %q", e.Status)
			}
			if e.Phase == AgentPhasePre && !strings.Contains(e.Detail, "/tmp/x") {
				t.Fatalf("call_a pre detail = %q", e.Detail)
			}
		case "call_b":
			callB++
			if e.Phase == AgentPhasePost && e.Status != AgentStatusError {
				t.Fatalf("call_b post status = %q", e.Status)
			}
		default:
			t.Fatalf("unexpected CallID %q", e.CallID)
		}
	}
	if callA != 2 || callB != 2 {
		t.Fatalf("call pairing: call_a=%d call_b=%d want 2 each", callA, callB)
	}
}

func eventSummary(events []AgentLifecycleEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Kind+"/"+e.Phase+"/"+e.CallID)
	}
	return out
}

type stubToolbox struct {
	mu    sync.Mutex
	calls []string
}

func (s *stubToolbox) ListTools() []tools.ToolInfo { return nil }

func (s *stubToolbox) Execute(_ context.Context, name string, _ []byte) (string, error) {
	s.mu.Lock()
	s.calls = append(s.calls, name)
	s.mu.Unlock()
	if name == "fail_me" {
		return "", errString("nope")
	}
	return "ok:" + name, nil
}

type errString string

func (e errString) Error() string { return string(e) }

func TestExecuteTurnToolsEmitsToolCallPairsWithCallIDs(t *testing.T) {
	store := &lifecycleConvStore{byID: map[string]*domain.Conversation{
		"conv_tools": {
			ID: "conv_tools", Status: "running",
			Messages: []domain.Message{{
				ID: "msg1", Role: domain.RoleAssistant,
				ToolCalls: []domain.ToolCall{
					{ID: "c1", Name: "alpha"},
					{ID: "c2", Name: "fail_me"},
				},
			}},
		},
	}}
	box := &stubToolbox{}
	svc := New(Deps{Conversations: store, Toolbox: box, Settings: staticSettings{}})
	obs := &recordingObserver{}
	svc.RegisterAgentObserver(obs)

	run := &TurnRun{
		ID: "run_tools", ConversationID: "conv_tools",
		Headless: true, ToolKind: tools.AgentAutomation,
		Ctx: context.Background(),
	}
	calls := []domain.ToolCall{
		{ID: "c1", Name: "alpha", Args: `{}`},
		{ID: "c2", Name: "fail_me", Args: `{}`},
	}
	if err := svc.ExecuteTurnTools(run, "msg1", calls, ModelCapabilities{}, domain.Settings{MaxParallelTools: 2}, 3); err != nil {
		t.Fatalf("ExecuteTurnTools: %v", err)
	}

	obs.mu.Lock()
	defer obs.mu.Unlock()
	var pre, post []AgentLifecycleEvent
	for _, e := range obs.events {
		if e.Kind != AgentEventToolCall {
			continue
		}
		if e.Round != 3 {
			t.Fatalf("round = %d, want 3", e.Round)
		}
		switch e.Phase {
		case AgentPhasePre:
			pre = append(pre, e)
		case AgentPhasePost:
			post = append(post, e)
		}
	}
	if len(pre) != 2 || len(post) != 2 {
		t.Fatalf("pre=%d post=%d events=%v", len(pre), len(post), eventSummary(obs.events))
	}
	ids := map[string]string{}
	for _, e := range post {
		ids[e.CallID] = e.Status
	}
	if ids["c1"] != AgentStatusOK || ids["c2"] != AgentStatusError {
		t.Fatalf("post statuses = %v", ids)
	}
}

type staticSettings struct{}

func (staticSettings) Get() domain.Settings { return domain.Settings{MaxParallelTools: 2} }

func TestLifecycleEmitsReasoningAndTextPerRound(t *testing.T) {
	store := &lifecycleConvStore{byID: map[string]*domain.Conversation{}}
	svc := New(Deps{Conversations: store})
	obs := &recordingObserver{}
	svc.RegisterAgentObserver(obs)

	run := &TurnRun{
		ID: "run_rt", ConversationID: "conv_rt",
		Headless: true, ToolKind: tools.AgentAutomation,
		Ctx: context.Background(),
	}
	svc.emitRoundContentObservers(run, 2, " deep thought ", "partial text")

	obs.mu.Lock()
	defer obs.mu.Unlock()
	var reasoning, text AgentLifecycleEvent
	for _, e := range obs.events {
		switch e.Kind {
		case AgentEventReasoning:
			reasoning = e
		case AgentEventText:
			text = e
		}
	}
	if reasoning.Kind != AgentEventReasoning || reasoning.Round != 2 || reasoning.Phase != AgentPhasePost || reasoning.Detail != "deep thought" {
		t.Fatalf("reasoning event missing/wrong: %+v", reasoning)
	}
	if text.Kind != AgentEventText || text.Detail != "partial text" {
		t.Fatalf("text event missing/wrong: %+v", text)
	}
}
