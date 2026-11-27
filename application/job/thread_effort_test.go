package job

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
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

func (stubAgentRuntime) Steer(context.Context, string, valueobject.UserInput, string, func()) bool {
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

// An effort pick on an agent room is refused, not stored and not dropped: the
// agent takes no effort, and the refusal says why before anything is written.
// The first turn of a fresh room is refused the same way, so no room appears.
func TestAnEffortPickOnAnAgentRoomIsRefused(t *testing.T) {
	spy := &selectionSpy{}
	d := hydrationDeps(t, spy)
	d.ThreadsEffort = enum.EffortMax
	useAgent(t, &d)
	err := send(t, d, spy, SendOptions{Effort: enum.EffortHigh, Upstream: "Alpha", Agent: "claude"})
	if err == nil || apperror.As(err).StatusCode() != 400 {
		t.Fatalf("send = %v, want a 400 refusal", err)
	}
	if _, err := d.Convs.Info(t.Context(), "room"); apperror.As(err).StatusCode() != apperror.StatusNotFound {
		t.Fatalf("a refused first turn created the room: %v", err)
	}
	if len(spy.efforts) != 0 {
		t.Fatalf("provider calls = %v, want none", spy.efforts)
	}
}

// The unset markers name no value — "default" and a blank upstream return a room
// to its own setting — so an agent room passes them, as a provider room does.
func TestAgentRoomPassesTheUnsetMarkers(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts SendOptions
	}{
		{"default effort", SendOptions{Effort: enum.EffortDefault}},
		{"blank upstream", SendOptions{Upstream: "  "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spy := &selectionSpy{}
			d := hydrationDeps(t, spy)
			useAgent(t, &d)
			opts := tc.opts
			opts.Agent = "claude"
			if err := send(t, d, spy, opts); err != nil {
				t.Fatalf("send = %v, want the turn to run", err)
			}
		})
	}
}

// A refused turn saves nothing, and the pick parked for the room's next turn stays
// in its file: the turn that would have applied it never ran.
func TestARefusedTurnLeavesTheParkedPickForTheNextTurn(t *testing.T) {
	spy := &selectionSpy{}
	d := hydrationDeps(t, spy)
	useAgent(t, &d)
	if err := send(t, d, spy, SendOptions{Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	parkAhead(t, d, "room", enum.EffortMax)
	if err := send(t, d, spy, SendOptions{Effort: enum.EffortHigh, Agent: "claude"}); err == nil || apperror.As(err).StatusCode() != 400 {
		t.Fatalf("send = %v, want a 400 refusal", err)
	}
	if _, ok, err := d.Convs.PendingSelection(t.Context(), "room"); err != nil || !ok {
		t.Fatalf("the refused turn took the parked pick: %v, %v", ok, err)
	}
}

// An edit is a turn: it refuses an effort pick on an agent room the way a send
// does, before the rewrite is stored, so the room keeps the messages it had.
func TestAnEffortPickOnAnAgentEditIsRefused(t *testing.T) {
	spy := &selectionSpy{}
	d := hydrationDeps(t, spy)
	useAgent(t, &d)
	if err := send(t, d, spy, SendOptions{Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	before, err := d.Convs.Load(t.Context(), "room")
	if err != nil {
		t.Fatal(err)
	}
	effort := enum.EffortHigh
	j := &Edit{Deps: d, Ctx: t.Context(), MsgID: before.Messages()[0].ID(), Content: "rewritten", Opts: SendOptions{Effort: effort, Agent: "claude"}}
	j.Result = make(chan error, 1)
	j.Bind("room", nil)
	err = j.Execute(t.Context())
	if want := AgentPickRefusal(true, &effort, nil); err == nil || err.Error() != want.Error() {
		t.Fatalf("edit = %v, want the refusal %q", err, want.Error())
	}
	after, err := d.Convs.Load(t.Context(), "room")
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Messages()) != len(before.Messages()) || after.Messages()[0].Content() != before.Messages()[0].Content() {
		t.Fatalf("a refused edit changed the room: %d messages, first %q", len(after.Messages()), after.Messages()[0].Content())
	}
}

// A retry is a turn too: it refuses an effort pick on an agent room before any
// provider or agent call, and the room keeps the turn it would have retried.
func TestAnEffortPickOnAnAgentResumeIsRefused(t *testing.T) {
	spy := &selectionSpy{}
	d := hydrationDeps(t, spy)
	useAgent(t, &d)
	if err := send(t, d, spy, SendOptions{Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	before, err := d.Convs.Load(t.Context(), "room")
	if err != nil {
		t.Fatal(err)
	}
	effort := enum.EffortHigh
	j := &Resume{Deps: d, Ctx: t.Context(), Opts: SendOptions{Effort: effort}}
	j.Result = make(chan error, 1)
	j.Bind("room", nil)
	err = j.Execute(t.Context())
	if want := AgentPickRefusal(true, &effort, nil); err == nil || err.Error() != want.Error() {
		t.Fatalf("resume = %v, want the refusal %q", err, want.Error())
	}
	if len(spy.efforts) != 0 {
		t.Fatalf("provider calls = %v, want none", spy.efforts)
	}
	after, err := d.Convs.Load(t.Context(), "room")
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Messages()) != len(before.Messages()) {
		t.Fatalf("a refused retry changed the room: %d messages, had %d", len(after.Messages()), len(before.Messages()))
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
	mu       sync.Mutex
	begins   []string
	ends     []string
	consumed []string
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

func (g *gateRecorder) ConsumePending(convID string, acceptedAt time.Time) {
	g.mu.Lock()
	g.consumed = append(g.consumed, convID)
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

// parkAhead writes a pick that waits for the room's next turn.
func parkAhead(t *testing.T, d Deps, convID string, effort enum.Effort) {
	t.Helper()
	upstream := "Alpha"
	if err := d.Convs.SavePendingSelection(t.Context(), convID, drepo.PendingSelection{
		Effort: &effort, Upstream: &upstream, AcceptedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
}

// An edit and a retry are turns: each takes the pick parked for the room's next
// turn, applies it, and clears it once its own save landed.
func TestEditAndResumeApplyThePendingPick(t *testing.T) {
	for _, kind := range []string{"edit", "resume"} {
		t.Run(kind, func(t *testing.T) {
			spy := &selectionSpy{}
			d := hydrationDeps(t, spy)
			gate := &gateRecorder{}
			d.Turns = gate
			room := seedRoom(t, d.Convs, "room", "hi")
			parkAhead(t, d, "room", enum.EffortMax)

			var err error
			switch kind {
			case "edit":
				j := &Edit{Deps: d, Ctx: t.Context(), MsgID: room.Messages()[0].ID(), Content: "rewritten", ProviderID: "gw1", ModelID: "m1"}
				j.Result = make(chan error, 1)
				j.Bind("room", nil)
				err = j.Execute(t.Context())
			case "resume":
				j := &Resume{Deps: d, Ctx: t.Context(), ProviderID: "gw1", ModelID: "m1"}
				j.Result = make(chan error, 1)
				j.Bind("room", nil)
				err = j.Execute(t.Context())
			}
			if err != nil {
				t.Fatalf("%s: %v", kind, err)
			}
			if effort, _ := storedSelection(t, d); effort != enum.EffortMax {
				t.Fatalf("%s: the parked pick did not reach the turn (effort %q)", kind, effort)
			}
			if _, ok, err := d.Convs.PendingSelection(t.Context(), "room"); err != nil || ok {
				t.Fatalf("%s: the consumed pick was not cleared: %v, %v", kind, ok, err)
			}
			// The room is claimed for the whole turn: a pick taken then waits, and
			// a surface reads the room as working.
			if got := gate.Begun(); len(got) != 1 || got[0] != "room" {
				t.Fatalf("%s: claimed %v, want the room", kind, got)
			}
			if got := gate.Ended(); len(got) != 1 || got[0] != "room" {
				t.Fatalf("%s: released %v, want the room", kind, got)
			}
		})
	}
}

// A fold is not a turn: it must leave the parked pick for the room's next turn.
func TestCompactLeavesThePendingPick(t *testing.T) {
	d := hydrationDeps(t, &selectionSpy{})
	seedRoom(t, d.Convs, "room", "hi")
	parkAhead(t, d, "room", enum.EffortMax)
	conv, err := d.Convs.Load(t.Context(), "room")
	if err != nil {
		t.Fatal(err)
	}
	// A fold that cannot run is enough: the rule is that it never touches the
	// pick, on any path.
	j := &Compact{Deps: d, Ctx: t.Context()}
	j.Result = make(chan error, 1)
	j.Bind("room", nil)
	if err := j.Execute(t.Context()); err == nil {
		t.Fatal("the fold was expected to refuse this room")
	}
	if _, ok, err := d.Convs.PendingSelection(t.Context(), "room"); err != nil || !ok {
		t.Fatalf("the fold consumed the pick: %v, %v", ok, err)
	}
	if effort, _ := storedSelection(t, d); effort == enum.EffortMax {
		t.Fatal("the fold applied the pick to the room")
	}
	_ = conv
}

// failingSave is a conversation store whose save fails, for the durability rule.
type failingSave struct {
	drepo.Conversation
	fail bool
}

func (f *failingSave) Save(ctx context.Context, conv *aggregate.Conversation) error {
	if f.fail {
		return apperror.New(500, "the store refused the write", nil)
	}
	return f.Conversation.Save(ctx, conv)
}

// A pick is cleared only after the turn's own save landed: a failed save keeps
// the file, so the next turn applies it again instead of losing it.
func TestPendingSurvivesAFailedSave(t *testing.T) {
	spy := &selectionSpy{}
	d := hydrationDeps(t, spy)
	store := &failingSave{Conversation: d.Convs}
	d.Convs = store
	seedRoom(t, d.Convs, "room", "hi")
	parkAhead(t, d, "room", enum.EffortMax)

	store.fail = true
	j := &Send{Deps: d, Ctx: t.Context(), Receipt: NewPersistenceReceipt(), ProviderID: "gw1", ModelID: "m1", Message: "again"}
	j.Result = make(chan error, 1)
	j.Bind("room", nil)
	if err := j.Execute(t.Context()); err == nil {
		t.Fatal("the turn passed although the store refused the write")
	}
	if _, ok, err := d.Convs.PendingSelection(t.Context(), "room"); err != nil || !ok {
		t.Fatalf("a failed save dropped the pick: %v, %v", ok, err)
	}
}
