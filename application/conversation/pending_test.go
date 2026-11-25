package appconversation

import (
	"context"
	"sync"
	"testing"
	"time"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// blockingTurn is a turn that holds its room: nothing else runs there until it
// ends, which is the state a pick has to wait for.
type blockingTurn struct {
	appjob.Base
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingTurn() *blockingTurn {
	j := &blockingTurn{started: make(chan struct{}), release: make(chan struct{})}
	j.Result = make(chan error, 1)
	return j
}

func (j *blockingTurn) IsTurn() bool { return true }

func (j *blockingTurn) Execute(context.Context) error {
	j.once.Do(func() { close(j.started) })
	<-j.release
	return nil
}

func (j *blockingTurn) Interrupt() {}

// busyRoom puts a turn in the room's worker and waits until it is running. The
// returned function lets it end; it is safe to call twice.
func busyRoom(t *testing.T, app *ConversationApp, convID string) func() {
	t.Helper()
	turn := newBlockingTurn()
	app.post(convID, turn)
	select {
	case <-turn.started:
	case <-time.After(2 * time.Second):
		t.Fatal("the blocking turn never started")
	}
	var once sync.Once
	return func() { once.Do(func() { close(turn.release) }) }
}

// waitIdle blocks until the room's worker runs nothing and queues nothing.
func waitIdle(t *testing.T, app *ConversationApp, convID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for app.running(convID) || app.QueueDepth(convID) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("the room never went idle")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// pickValue answers one pick from an ordered selection list.
func pickValue(picks []valueobject.ConfigSelection, id string) string {
	for _, pick := range picks {
		if pick.ID == id {
			return pick.Value
		}
	}
	return ""
}

// An idle room has no turn to disturb, so a pick applies at once: the record
// changes now and nothing waits.
func TestIdleRoomAppliesAPickAtOnce(t *testing.T) {
	app, _, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_idle", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	parked, err := app.Annotate(ctx, "c_idle", Annotation{Runtime: &RuntimeBinding{AgentID: "fake", AgentModel: "swe-2-1"}})
	if err != nil || parked {
		t.Fatalf("idle pick = parked %v, %v — an idle room has nothing to wait for", parked, err)
	}
	info, err := app.deps.Convs.Info(ctx, "c_idle")
	if err != nil || info.ModelID != "swe-2-1" {
		t.Fatalf("record model = %q, %v — an idle pick applies now", info.ModelID, err)
	}
	if _, ok, _ := app.Pending(ctx, "c_idle"); ok {
		t.Fatal("an idle pick left a sidecar behind")
	}
}

// A model picked while a turn runs waits for the next turn: the running turn's
// runtime is fixed when it starts, so nothing about this one changes. The next
// turn — wherever it is sent from — runs the pick, and the record keeps it.
func TestPendingModelWaitsForTheNextTurn(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_pend", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	release := busyRoom(t, app, "c_pend")
	defer release()

	parked, err := app.Annotate(ctx, "c_pend", Annotation{Runtime: &RuntimeBinding{AgentID: "fake", AgentModel: "swe-2-1"}})
	if err != nil || !parked {
		t.Fatalf("pick = parked %v, %v — want it taken for the next turn", parked, err)
	}
	sel, ok, err := app.Pending(ctx, "c_pend")
	if err != nil || !ok || sel.AgentID != "fake" || sel.AgentModel != "swe-2-1" {
		t.Fatalf("pending = %+v, %v, %v", sel, ok, err)
	}
	// A 202 is not "applied": the record still holds what it held.
	if info, err := app.deps.Convs.Info(ctx, "c_pend"); err != nil || info.ModelID == "swe-2-1" {
		t.Fatalf("the pick reached the record before the next turn: %+v, %v", info, err)
	}

	release()
	waitIdle(t, app, "c_pend")
	if _, err := app.Send(ctx, "c_pend", "", "", "again", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	info, err := app.deps.Convs.Info(ctx, "c_pend")
	if err != nil {
		t.Fatal(err)
	}
	if info.ModelID != "swe-2-1" {
		t.Fatalf("record model = %q, want the pick the next turn ran", info.ModelID)
	}
	prompts := rt.Prompts()
	if got := prompts[len(prompts)-1].Model; got != "swe-2-1" {
		t.Fatalf("the next turn ran model %q, want swe-2-1", got)
	}
	if _, ok, _ := app.Pending(ctx, "c_pend"); ok {
		t.Fatal("the pending survived the turn that applied it")
	}
}

// A session option picked while a turn runs waits the same way, and the turn
// after it starts the session on it. Two picks before that turn: the last wins.
func TestPendingOptionWaitsForTheNextTurn(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_pend", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	release := busyRoom(t, app, "c_pend")
	defer release()

	parked, err := app.SetAgentOption(ctx, "c_pend", "mode", "build")
	if err != nil || !parked {
		t.Fatalf("option pick = parked %v, %v", parked, err)
	}
	// The record keeps the old pick: nothing is applied under the running turn.
	if info, err := app.deps.Convs.Info(ctx, "c_pend"); err != nil || pickValue(info.AgentOptions, "mode") == "build" {
		t.Fatalf("the pick reached the record before the next turn: %+v", info)
	}
	// A second pick for the same option before the turn: the last one wins.
	if parked, err := app.SetAgentOption(ctx, "c_pend", "mode", "ask"); err != nil || !parked {
		t.Fatalf("second pick = parked %v, %v", parked, err)
	}
	if sel, _, _ := app.Pending(ctx, "c_pend"); sel.Options["mode"] != "ask" {
		t.Fatalf("pending options = %v, want the last pick", sel.Options)
	}

	release()
	waitIdle(t, app, "c_pend")
	if _, err := app.Send(ctx, "c_pend", "", "", "again", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	prompts := rt.Prompts()
	if got := pickValue(prompts[len(prompts)-1].Options, "mode"); got != "ask" {
		t.Fatalf("the next turn ran mode %q, want ask", got)
	}
	if _, ok, _ := app.Pending(ctx, "c_pend"); ok {
		t.Fatal("the pending survived the turn that applied it")
	}
}

// A pick the catalog does not know is refused while the room is busy too: a pick
// is checked when it arrives, never left for the turn to discover.
func TestPendingPickIsCheckedWhenItArrives(t *testing.T) {
	app, _, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_pend", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	release := busyRoom(t, app, "c_pend")
	defer release()

	if _, err := app.SetAgentOption(ctx, "c_pend", "nosuch", "x"); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("unknown option = %v, want 400", err)
	}
	if _, err := app.SetAgentOption(ctx, "c_pend", "mode", "nope"); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("unoffered value = %v, want 400", err)
	}
	if _, ok, _ := app.Pending(ctx, "c_pend"); ok {
		t.Fatal("a refused pick was parked")
	}
}

// Once the turn is over the room is idle again, and a pick applies at once — the
// rule follows the turn, not a lingering flag.
func TestPickAfterTheTurnAppliesAtOnce(t *testing.T) {
	app, _, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_idle", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	release := busyRoom(t, app, "c_idle")
	if parked, err := app.SetAgentOption(ctx, "c_idle", "mode", "build"); err != nil || !parked {
		t.Fatalf("pick under the turn = parked %v, %v, want it parked", parked, err)
	}
	release()
	waitIdle(t, app, "c_idle")

	// The parked pick is still waiting (the turn that was running never applied
	// it) and the room is idle now. A new pick applies at once, and merges into
	// the parked one only as the pending it replaces.
	parked, err := app.SetAgentOption(ctx, "c_idle", "strict", "true")
	if err != nil || parked {
		t.Fatalf("pick on an idle room = parked %v, %v, want it applied now", parked, err)
	}
	info, err := app.deps.Convs.Info(ctx, "c_idle")
	if err != nil || pickValue(info.AgentOptions, "strict") != "true" {
		t.Fatalf("idle pick did not reach the record: %+v, %v", info.AgentOptions, err)
	}
	sel, ok, _ := app.Pending(ctx, "c_idle")
	if !ok || sel.Options["mode"] != "build" || sel.Options["strict"] != "" {
		t.Fatalf("pending = %+v, %v — the idle pick must not have joined it", sel, ok)
	}
}
