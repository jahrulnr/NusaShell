package job

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	repoagent "github.com/jahrulnr/nusashell/infrastructure/repository/agent"
)

// A room's first provider turn starts on Settings → Models → "New threads"
// when the caller names no effort and the room stores none: a cross-thread
// spawn and an automation run have no picker to send one, so this is where
// their value comes from. It becomes the room's own — the thread keeps what
// it started on.
func TestFirstTurnOfASpawnedRoomUsesTheConfiguredEffort(t *testing.T) {
	spy := &selectionSpy{}
	d := hydrationDeps(t, spy)
	d.ThreadsEffort = enum.EffortMax

	if err := send(t, d, spy, SendOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(spy.efforts) != 1 || spy.efforts[0] != enum.EffortMax {
		t.Fatalf("efforts = %v, want the configured max on the first turn", spy.efforts)
	}
	if effort, _ := storedSelection(t, d); effort != enum.EffortMax {
		t.Fatalf("stored effort = %q, want the value the room started on", effort)
	}
}

// A caller that does pick an effort keeps it: the WebUI's picker and a chat
// channel both send one, and the setting is only the fallback.
func TestFirstTurnKeepsAnExplicitEffort(t *testing.T) {
	spy := &selectionSpy{}
	d := hydrationDeps(t, spy)
	d.ThreadsEffort = enum.EffortMax

	if err := send(t, d, spy, SendOptions{Effort: enum.EffortLow}); err != nil {
		t.Fatal(err)
	}
	if len(spy.efforts) != 1 || spy.efforts[0] != enum.EffortLow {
		t.Fatalf("efforts = %v, want the caller's low", spy.efforts)
	}
	if effort, _ := storedSelection(t, d); effort != enum.EffortLow {
		t.Fatalf("stored effort = %q, want the caller's low", effort)
	}
}

// A room that already started keeps its own effort: the setting is a start
// value, not a live one, so changing it never rewrites threads retroactively.
func TestExistingRoomKeepsItsOwnEffort(t *testing.T) {
	spy := &selectionSpy{}
	d := hydrationDeps(t, spy)
	d.ThreadsEffort = enum.EffortMax
	conv := seedRoom(t, d.Convs, "room", "hi")
	conv.SetEffort(enum.EffortMedium)
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}

	if err := send(t, d, spy, SendOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(spy.efforts) != 1 || spy.efforts[0] != enum.EffortMedium {
		t.Fatalf("efforts = %v, want the room's medium", spy.efforts)
	}
	if effort, _ := storedSelection(t, d); effort != enum.EffortMedium {
		t.Fatalf("stored effort = %q, want the room's medium", effort)
	}
}

// stubAgentRuntime stands in for the agent process: one prompt appends the
// agent's reply. The rest of the runtime surface is never reached here.
type stubAgentRuntime struct {
	dservice.AgentRuntime
}

func (stubAgentRuntime) Prompt(_ context.Context, p dservice.AgentPrompt, _ event.TurnEventHandler, _ dservice.Approver) error {
	m, err := entity.NewMessage(enum.RoleAssistant, "agent reply")
	if err != nil {
		return err
	}
	m.SetFinishReason(enum.FinishReasonStop)
	p.Conversation.Append(m)
	return nil
}

func (stubAgentRuntime) Steer(context.Context, string, valueobject.UserInput, func()) bool {
	return false
}

// An agent room carries its effort in the agent's own session, so the thread
// setting never touches it — and never fails the turn either.
// useAgent registers an agent and its runtime on the job deps, the way the
// composition does before an agent turn can run.
func useAgent(t *testing.T, d *Deps) {
	t.Helper()
	agent, err := aggregate.NewAgent("claude", "Claude", "claude", nil, enum.ApprovalPolicyAsk)
	if err != nil {
		t.Fatal(err)
	}
	agents := repoagent.New(t.TempDir(), nil)
	if err := agents.Save(t.Context(), &agent); err != nil {
		t.Fatal(err)
	}
	d.Agents = agents
	d.AgentRuntime = stubAgentRuntime{}
}

// storedRecord reads the room's record, not a loaded aggregate: Load replays
// BindAgent, which strips provider picks from agent rooms — so a loaded
// conversation cannot tell a value that was written from one that was dropped.
func storedRecord(t *testing.T, d Deps) (string, string) {
	t.Helper()
	info, err := d.Convs.Info(t.Context(), "room")
	if err != nil {
		t.Fatal(err)
	}
	return info.Effort, info.Upstream
}

// A provider room that becomes an agent room drops the provider's picks. The
// agent applies neither an effort nor an upstream, so a room that kept them
// would show a value nothing honours — for good.
// A relayed prompt is stored as the sender wrote it: the provenance rides its
// own record, and the head a model reads is rendered at request time.
func TestRelayedPromptStoresItsBodyAndItsProvenance(t *testing.T) {
	spy := &selectionSpy{}
	d := hydrationDeps(t, spy)
	relay := valueobject.NewRelay("c_sender", "otter", "Planner")
	j := &Send{Deps: d, Ctx: t.Context(), Receipt: NewPersistenceReceipt(),
		ProviderID: "gw1", ModelID: "m1", Message: "ship it",
		Opts: SendOptions{Origin: valueobject.TurnOrigin{Kind: enum.TurnOriginConversation}, Relay: &relay},
	}
	j.Result = make(chan error, 1)
	j.Bind("room", nil)
	if err := j.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	conv, err := d.Convs.Load(t.Context(), "room")
	if err != nil {
		t.Fatal(err)
	}
	var stored *entity.Message
	for i, m := range conv.Messages() {
		if m.Role() == enum.RoleUser {
			stored = &conv.Messages()[i]
		}
	}
	if stored == nil {
		t.Fatalf("no user message stored: %+v", conv.Messages())
	}
	if got := stored.Content(); got != "ship it" {
		t.Fatalf("stored body = %q, want the sender's text alone", got)
	}
	if got := stored.Relay(); got.ID != "c_sender" || got.Label != "otter" || got.Title != "Planner" {
		t.Fatalf("stored relay = %+v", got)
	}
	if got := stored.UserType(); got != enum.LLMUserTypeRelay {
		t.Fatalf("user type = %q, want relay", got)
	}
	// The head is nowhere in the record; it is what a request renders.
	if strings.Contains(stored.Content(), "From thread") {
		t.Fatalf("the head reached the body: %q", stored.Content())
	}
}

func TestBindingAnAgentDropsTheProviderSelection(t *testing.T) {
	spy := &selectionSpy{}
	d := hydrationDeps(t, spy)
	d.ThreadsEffort = enum.EffortMax
	if err := send(t, d, spy, SendOptions{Upstream: "Alpha"}); err != nil {
		t.Fatal(err)
	}
	if effort, upstream := storedSelection(t, d); effort != enum.EffortMax || upstream != "Alpha" {
		t.Fatalf("provider room stored %q, %q", effort, upstream)
	}
	useAgent(t, &d)
	if err := send(t, d, spy, SendOptions{Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	// The record itself: the binding must not leave the old picks behind for
	// a later load to clean up.
	if effort, upstream := storedRecord(t, d); effort != "" || upstream != "" {
		t.Fatalf("agent room kept %q, %q — the agent applies neither", effort, upstream)
	}
}

// A pick the caller sends with the first turn of an agent room never reaches
// the record: the fresh-room path stores what a provider turn would use, and
// an agent room has none.
func TestAgentRoomIgnoresAPickedEffort(t *testing.T) {
	spy := &selectionSpy{}
	d := hydrationDeps(t, spy)
	d.ThreadsEffort = enum.EffortMax
	useAgent(t, &d)
	if err := send(t, d, spy, SendOptions{Effort: enum.EffortHigh, Upstream: "Alpha", Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	if effort, upstream := storedRecord(t, d); effort != "" || upstream != "" {
		t.Fatalf("agent room stored %q, %q, want neither", effort, upstream)
	}
}

func TestAgentRoomIgnoresTheThreadEffort(t *testing.T) {
	spy := &selectionSpy{}
	d := hydrationDeps(t, spy)
	d.ThreadsEffort = enum.EffortMax
	agent, err := aggregate.NewAgent("claude", "Claude", "claude", nil, enum.ApprovalPolicyAsk)
	if err != nil {
		t.Fatal(err)
	}
	agents := repoagent.New(t.TempDir(), nil)
	if err := agents.Save(t.Context(), &agent); err != nil {
		t.Fatal(err)
	}
	d.Agents = agents
	d.AgentRuntime = stubAgentRuntime{}

	if err := send(t, d, spy, SendOptions{Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	if len(spy.efforts) != 0 {
		t.Fatalf("provider calls = %v, want none on an agent room", spy.efforts)
	}
	conv, err := d.Convs.Load(t.Context(), "room")
	if err != nil {
		t.Fatal(err)
	}
	if conv.Effort() != "" {
		t.Fatalf("agent room stored effort %q, want none", conv.Effort())
	}
	if last := conv.Last(); last == nil || last.Content() != "agent reply" {
		t.Fatalf("agent turn left %+v, want the reply", last)
	}
}

// An unset setting — absent, or the model's own "default" — changes nothing:
// the first turn runs the model's configured effort and stores none.
func TestUnsetThreadEffortLeavesTheModelAlone(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configured enum.Effort
	}{
		{"absent", ""},
		{"default", enum.EffortDefault},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spy := &selectionSpy{}
			d := hydrationDeps(t, spy)
			d.ThreadsEffort = tc.configured

			if err := send(t, d, spy, SendOptions{}); err != nil {
				t.Fatal(err)
			}
			if len(spy.efforts) != 1 || spy.efforts[0] != "" {
				t.Fatalf("efforts = %v, want the model's own", spy.efforts)
			}
			if effort, _ := storedSelection(t, d); effort != "" {
				t.Fatalf("stored effort = %q, want none", effort)
			}
		})
	}
}

// gateRecorder is a TurnGate that remembers what it heard.
type gateRecorder struct {
	mu     sync.Mutex
	begins []string
	ends   []string
}

func (g *gateRecorder) BeginTurn(convID string) {
	g.mu.Lock()
	g.begins = append(g.begins, convID)
	g.mu.Unlock()
}

func (g *gateRecorder) EndTurn(convID string) {
	g.mu.Lock()
	g.ends = append(g.ends, convID)
	g.mu.Unlock()
}

func (g *gateRecorder) Begun() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.begins...)
}

func (g *gateRecorder) Ended() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.ends...)
}

// A fork copies a room on that room's worker, so the source is what is busy —
// its claim is what a surface reads. The destination is new; nothing there waits.
func TestForkClaimsTheSourceRoom(t *testing.T) {
	spy := &selectionSpy{}
	d := hydrationDeps(t, spy)
	gate := &gateRecorder{}
	d.Turns = gate
	src := seedRoom(t, d.Convs, "c_src", "hi")
	dst, err := src.Fork()
	if err != nil {
		t.Fatal(err)
	}
	j := &Fork{Deps: d, Ctx: t.Context(), Dst: dst}
	j.Result = make(chan error, 1)
	j.Bind("c_src", nil)
	if err := j.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := gate.Begun(); len(got) != 1 || got[0] != "c_src" {
		t.Fatalf("claimed %v, want the source room", got)
	}
	if got := gate.Ended(); len(got) != 1 || got[0] != "c_src" {
		t.Fatalf("released %v, want the source room", got)
	}
}
