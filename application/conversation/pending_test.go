package appconversation

import (
	"context"
	"sync"
	"testing"
	"time"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
	toolsummary "github.com/jahrulnr/nusashell/infrastructure/service/tool/summary"
)

func agentBinding(t *testing.T, id, model string) *appjob.RuntimeBinding {
	t.Helper()
	b, err := valueobject.NewAgentBinding(id, model)
	if err != nil {
		t.Fatal(err)
	}
	return &b
}

func harnessBinding(t *testing.T, provider, model string) *appjob.RuntimeBinding {
	t.Helper()
	b, err := valueobject.NewHarnessBinding(provider, model)
	if err != nil {
		t.Fatal(err)
	}
	return &b
}

// busyTurn starts a real turn and holds it inside the agent call: the room's
// claim is the one the send job took, not one a test placed. It returns the
// function that releases the turn.
//
// Every wait here is bounded on purpose: a pick that never parks must fail this
// test in seconds, not burn a package timeout that names nothing.
func busyTurn(t *testing.T, app *ConversationApp, rt *scriptedAgent, convID string) (release func()) {
	t.Helper()
	entered := make(chan struct{})
	free := make(chan struct{})
	rt.mu.Lock()
	rt.beforePrompt = func(context.Context) error { close(entered); <-free; return nil }
	rt.mu.Unlock()

	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		_, err := app.Send(ctx, convID, "", "", "hold on", nil, appjob.SendOptions{Agent: "fake"}, nil)
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("the turn never reached the agent")
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			close(free)
			// The turn must come back: waiting on it unbounded is how a test
			// turns a red into a package timeout that names nothing.
			select {
			case <-done:
			case <-time.After(15 * time.Second):
				cancel()
				t.Fatal("the turn never came back after it was released")
			}
			cancel()
			rt.mu.Lock()
			rt.beforePrompt = nil
			rt.mu.Unlock()
		})
	}
}

// pickWhileBusy answers what a pick did while a turn ran. It fails when the pick
// did not answer at all: a pick taken during a turn must come back at once with
// "it waits", never wait behind the turn itself.
func pickWhileBusy(t *testing.T, why string, fn func() (bool, error)) (bool, error) {
	t.Helper()
	type answer struct {
		parked bool
		err    error
	}
	ch := make(chan answer, 1)
	go func() {
		parked, err := fn()
		ch <- answer{parked, err}
	}()
	select {
	case got := <-ch:
		return got.parked, got.err
	case <-time.After(2 * time.Second):
		t.Fatalf("a pick did not answer while %s — it waited behind the job instead of parking", why)
		return false, nil
	}
}

// waitFor blocks until ok holds, naming what never happened.
func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("%s never happened", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitIdle blocks until the room's worker runs nothing and queues nothing.
func waitIdle(t *testing.T, app *ConversationApp, convID string) {
	t.Helper()
	waitFor(t, "the room going idle", func() bool {
		return !app.running(convID) && app.QueueDepth(convID) == 0
	})
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
	parked, err := app.Annotate(ctx, "c_idle", Annotation{Runtime: agentBinding(t, "fake", "swe-2-1")})
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
	release := busyTurn(t, app, rt, "c_pend")
	defer release()

	parked, err := pickWhileBusy(t, "a turn ran", func() (bool, error) {
		return app.Annotate(ctx, "c_pend", Annotation{Runtime: agentBinding(t, "fake", "swe-2-1")})
	})
	if err != nil || !parked {
		t.Fatalf("pick = parked %v, %v — want it taken for the next turn", parked, err)
	}
	sel, ok, err := app.Pending(ctx, "c_pend")
	if err != nil || !ok || sel.AgentID() != "fake" || sel.AgentModel() != "swe-2-1" {
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

// A model picked while the room is busy parks generation N. The owner then picks
// another model while the room is idle: that pick is the room's value now. The
// parked generation must not be applied over it, or the next turn would run the
// older model and the idle pick would look like it changed nothing.
func TestIdlePickSupersedesTheParkedGeneration(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_sup", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	release := busyTurn(t, app, rt, "c_sup")
	parked, err := pickWhileBusy(t, "a turn ran", func() (bool, error) {
		return app.Annotate(ctx, "c_sup", Annotation{Runtime: agentBinding(t, "fake", "stale-model")})
	})
	if err != nil || !parked {
		t.Fatalf("busy pick = parked %v, %v", parked, err)
	}
	release()
	waitIdle(t, app, "c_sup")

	// Idle now: the explicit pick must land on the record immediately.
	parked, err = app.Annotate(ctx, "c_sup", Annotation{Runtime: agentBinding(t, "fake", "swe-2-1")})
	if err != nil {
		t.Fatal(err)
	}
	if parked {
		t.Fatal("an idle pick is the room's value now — nothing may park it")
	}
	if info, err := app.deps.Convs.Info(ctx, "c_sup"); err != nil || info.ModelID != "swe-2-1" {
		t.Fatalf("record model = %v, %v — the idle pick must apply at once", info, err)
	}

	// The next turn must run the explicit pick, not the parked generation —
	// and the pending file must not survive to fail a later turn with a model
	// the agent no longer offers.
	if _, err := app.Send(ctx, "c_sup", "", "", "again", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	prompts := rt.Prompts()
	if got := prompts[len(prompts)-1].Model; got != "swe-2-1" {
		t.Fatalf("the next turn ran model %q, want the explicit idle pick swe-2-1 — the stale parked generation won", got)
	}
	if _, ok, _ := app.Pending(ctx, "c_sup"); ok {
		t.Fatal("the superseded generation survived — a stale pick can still fold over a newer one later")
	}
}

// Coverage is exact: an idle RUNTIME pick supersedes only the runtime fields.
// A session option parked while the room was busy is not covered by it and
// must stay parked — dropping it silently would kill the notice channel that
// is the only way the owner ever hears about it.
func TestIdleRuntimePickLeavesParkedOptionsAlone(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_cov", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	release := busyTurn(t, app, rt, "c_cov")
	parked, err := pickWhileBusy(t, "a turn ran", func() (bool, error) {
		return app.SetAgentOption(ctx, "c_cov", "mode", "ask")
	})
	if err != nil || !parked {
		t.Fatalf("option pick = parked %v, %v", parked, err)
	}
	release()
	waitIdle(t, app, "c_cov")

	if _, err := app.Annotate(ctx, "c_cov", Annotation{Runtime: agentBinding(t, "fake", "swe-2-1")}); err != nil {
		t.Fatal(err)
	}
	sel, ok, err := app.Pending(ctx, "c_cov")
	if err != nil || !ok || sel.Options["mode"] != "ask" {
		t.Fatalf("pending = %+v, %v, %v — the parked option is not the runtime pick's business and must survive it", sel, ok, err)
	}
	if sel.AgentModel() == "swe-2-1" || sel.AgentModel() != "" {
		t.Fatalf("the runtime fields must be superseded, not re-parked: %+v", sel)
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
	release := busyTurn(t, app, rt, "c_pend")
	defer release()

	parked, err := pickWhileBusy(t, "a turn ran", func() (bool, error) {
		return app.SetAgentOption(ctx, "c_pend", "mode", "build")
	})
	if err != nil || !parked {
		t.Fatalf("option pick = parked %v, %v", parked, err)
	}
	// The record keeps the old pick: nothing is applied under the running turn.
	if info, err := app.deps.Convs.Info(ctx, "c_pend"); err != nil || pickValue(info.AgentOptions, "mode") == "build" {
		t.Fatalf("the pick reached the record before the next turn: %+v", info)
	}
	// A second pick for the same option before the turn: the last one wins.
	if parked, err := pickWhileBusy(t, "a turn ran", func() (bool, error) {
		return app.SetAgentOption(ctx, "c_pend", "mode", "ask")
	}); err != nil || !parked {
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
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_pend", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	release := busyTurn(t, app, rt, "c_pend")
	defer release()

	if _, err := pickWhileBusy(t, "a turn ran", func() (bool, error) { return app.SetAgentOption(ctx, "c_pend", "nosuch", "x") }); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("unknown option = %v, want 400", err)
	}
	if _, err := pickWhileBusy(t, "a turn ran", func() (bool, error) { return app.SetAgentOption(ctx, "c_pend", "mode", "nope") }); apperror.As(err).StatusCode() != 400 {
		t.Fatalf("unoffered value = %v, want 400", err)
	}
	if _, ok, _ := app.Pending(ctx, "c_pend"); ok {
		t.Fatal("a refused pick was parked")
	}
}

// Once the turn is over the room is idle again, and a pick applies at once — the
// rule follows the turn, not a lingering flag.
func TestPickAfterTheTurnAppliesAtOnce(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_idle", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	release := busyTurn(t, app, rt, "c_idle")
	if parked, err := pickWhileBusy(t, "a turn ran", func() (bool, error) {
		return app.SetAgentOption(ctx, "c_idle", "mode", "build")
	}); err != nil || !parked {
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

// blockingCompactSvc holds the fold inside its first request: the compact job is
// running, its room's worker is busy, and a pick taken now must still be
// answered.
type blockingCompactSvc struct {
	*compactSvc
	entered chan struct{}
	free    chan struct{}
	once    sync.Once
}

func (s *blockingCompactSvc) Send(ctx context.Context, conv *aggregate.Conversation, prov *aggregate.Provider, model entity.Model, h event.TurnEventHandler) error {
	// Only the fold's own scratch dialogue holds: the room's ordinary turns run
	// through the same service and must not block.
	for _, tool := range conv.Tools() {
		if tool.Name() == toolsummary.ToolName {
			s.once.Do(func() { close(s.entered); <-s.free })
			break
		}
	}
	return s.compactSvc.Send(ctx, conv, prov, model, h)
}

// A pick taken while a fold runs is answered at once and waits for the next
// turn: the fold owns the room's worker for as long as it takes, and a pick that
// queued behind it would hang the request for minutes.
func TestPendingPickAnswersDuringACompact(t *testing.T) {
	svc := &blockingCompactSvc{compactSvc: &compactSvc{}, entered: make(chan struct{}), free: make(chan struct{})}
	app, _ := compactApp(t, svc, 0)
	ctx := t.Context()
	for _, content := range []string{"first", "second"} {
		if _, err := app.Send(ctx, "room", "gw1", "m1", content, nil, appjob.SendOptions{}, nil); err != nil {
			t.Fatal(err)
		}
	}

	folded := make(chan error, 1)
	go func() { folded <- app.Compact(ctx, "room") }()
	select {
	case <-svc.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the fold never reached the provider")
	}

	effort := enum.EffortMax
	parked, err := pickWhileBusy(t, "a fold ran", func() (bool, error) {
		return app.Annotate(ctx, "room", Annotation{Effort: &effort})
	})
	if err != nil || !parked {
		t.Fatalf("pick during a fold = parked %v, %v — want it answered for the next turn", parked, err)
	}
	// It is stored as a pick, not applied over the fold.
	if info, err := app.deps.Convs.Info(ctx, "room"); err != nil || info.Effort == "max" {
		t.Fatalf("the pick reached the record during the fold: %+v, %v", info, err)
	}
	if sel, ok, _ := app.Pending(ctx, "room"); !ok || sel.Effort == nil || sel.Effort.String() != "max" {
		t.Fatalf("pending = %+v, %v", sel, ok)
	}

	close(svc.free)
	select {
	case err := <-folded:
		if err != nil {
			t.Fatalf("the fold failed: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the fold never finished after it was released")
	}

	// The next turn applies it, and the pick is gone.
	if _, err := app.Send(ctx, "room", "gw1", "m1", "third", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	info, err := app.deps.Convs.Info(ctx, "room")
	if err != nil {
		t.Fatal(err)
	}
	if info.Effort != "max" {
		t.Fatalf("record effort = %q, want the pick the next turn ran", info.Effort)
	}
	if _, ok, _ := app.Pending(ctx, "room"); ok {
		t.Fatal("the pending survived the turn that applied it")
	}
}

// A pick already taken by the running turn is in play, not waiting: the
// projection must stop offering it, or a surface keeps saying "next turn" for a
// turn that already carries it.
func TestPendingIsHiddenWhileItsTurnRuns(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_consume", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	release := busyTurn(t, app, rt, "c_consume")
	parked, err := pickWhileBusy(t, "a turn ran", func() (bool, error) {
		return app.Annotate(ctx, "c_consume", Annotation{Runtime: agentBinding(t, "fake", "swe-2-1")})
	})
	if err != nil || !parked {
		t.Fatalf("park = %v, %v", parked, err)
	}
	release()
	waitIdle(t, app, "c_consume")

	// The next turn takes it; hold that turn inside the agent to observe it.
	entered, free := make(chan struct{}), make(chan struct{})
	rt.mu.Lock()
	rt.beforePrompt = func(context.Context) error { close(entered); <-free; return nil }
	rt.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		_, err := app.Send(ctx, "c_consume", "", "", "consume it", nil, appjob.SendOptions{Agent: "fake"}, nil)
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the consuming turn never reached the agent")
	}
	if _, ok, _ := app.Pending(ctx, "c_consume"); ok {
		t.Fatal("the projection still offers a pick the running turn already applied")
	}
	close(free)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the consuming turn never finished")
	}
	if _, ok, _ := app.Pending(ctx, "c_consume"); ok {
		t.Fatal("the pick was not cleared after the turn saved")
	}
	if info, err := app.deps.Convs.Info(ctx, "c_consume"); err != nil || info.ModelID != "swe-2-1" {
		t.Fatalf("record model = %q, %v", info.ModelID, err)
	}
}

// A pick parked *after* the running turn took the previous one is a different
// generation: it must stay visible.
func TestPendingNewPickStaysVisibleDuringTheTurn(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_gen", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	// Park a pick, and let the next turn consume it while we watch.
	release := busyTurn(t, app, rt, "c_gen")
	if parked, err := pickWhileBusy(t, "a turn ran", func() (bool, error) {
		return app.Annotate(ctx, "c_gen", Annotation{Runtime: agentBinding(t, "fake", "swe-2-1")})
	}); err != nil || !parked {
		t.Fatalf("park = %v, %v", parked, err)
	}
	release()
	waitIdle(t, app, "c_gen")

	entered, free := make(chan struct{}), make(chan struct{})
	rt.mu.Lock()
	rt.beforePrompt = func(context.Context) error { close(entered); <-free; return nil }
	rt.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		_, err := app.Send(ctx, "c_gen", "", "", "consume it", nil, appjob.SendOptions{Agent: "fake"}, nil)
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the consuming turn never reached the agent")
	}
	// A new pick, taken while that turn runs: another generation, and visible. It
	// is a session option — an agent room takes no effort, so an effort here is
	// refused rather than parked.
	parked, err := pickWhileBusy(t, "a turn ran", func() (bool, error) {
		return app.SetAgentOption(ctx, "c_gen", "mode", "build")
	})
	if err != nil || !parked {
		t.Fatalf("second park = %v, %v", parked, err)
	}
	sel, ok, err := app.Pending(ctx, "c_gen")
	if err != nil || !ok {
		t.Fatalf("a pick parked during the turn is not visible: %v, %v", ok, err)
	}
	if sel.Options["mode"] != "build" {
		t.Fatalf("pending = %+v, want the new generation", sel)
	}
	close(free)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the consuming turn never finished")
	}
	rt.mu.Lock()
	rt.beforePrompt = nil // the next turn must not re-enter the block above
	rt.mu.Unlock()

	// The turn that just ended consumed the *first* pick: its clear must not take
	// the newer one, which was never applied.
	after, still, err := app.Pending(ctx, "c_gen")
	if err != nil || !still {
		t.Fatalf("a newer pick was cleared by the older turn: %v, %v", still, err)
	}
	if after.Options["mode"] != "build" {
		t.Fatalf("pending after the turn = %+v, want the newer generation", after)
	}
	// The surviving file merged the pick this turn took with the new one, so the
	// next turn applies both — G1 again (idempotent) and G2 for the first time.
	if after.AgentModel() != "swe-2-1" {
		t.Fatalf("pending lost the applied pick it must re-apply: %+v", after)
	}
	// The next turn applies both, and only then do they go.
	if _, err := app.Send(ctx, "c_gen", "", "", "again", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	info, err := app.deps.Convs.Info(ctx, "c_gen")
	if err != nil {
		t.Fatal(err)
	}
	if info.ModelID != "swe-2-1" {
		t.Fatalf("record model = %q, want the surviving file's model", info.ModelID)
	}
	if mode, _ := info.AgentOptions.Value("mode"); mode != "build" {
		t.Fatalf("record mode = %q, want the new generation applied", mode)
	}
	if _, ok, _ := app.Pending(ctx, "c_gen"); ok {
		t.Fatal("the applied pick was not cleared")
	}
}
