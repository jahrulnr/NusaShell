package appconversation

import (
	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// A message accepted for a busy room is in history before its turn starts: the
// queue lives in memory, so the line must not wait for the job that may never
// run.
func TestEnqueueRecordsAnAcceptedRelayBeforeItsTurn(t *testing.T) {
	app, _ := fixture(t, okHandler)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_queue", "gw1", "m1", "hi", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	hold := newWedgeJob()
	app.post("c_queue", hold)
	select {
	case <-hold.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the holding job never started")
	}
	position, err := app.Enqueue(ctx, "c_queue", "", "", "verdict from PA", nil, appjob.SendOptions{
		Origin: valueobject.TurnOrigin{Kind: enum.TurnOriginConversation},
		Relay:  &valueobject.Relay{ID: "c_pa", Label: "coyote"},
	})
	if err != nil || position == 0 {
		t.Fatalf("enqueue = %d, %v; want it waiting", position, err)
	}
	// The line is already stored, while the job waits behind the holding one.
	rows := userRowsWithText(t, app, "c_queue", "verdict from PA")
	if len(rows) != 1 {
		t.Fatalf("an accepted relay has %d lines before its turn, want one", len(rows))
	}
	if rows[0].UserType() != enum.LLMUserTypeRelay || rows[0].Relay().ID != "c_pa" {
		t.Fatalf("the line lost its record: %s %+v", rows[0].UserType(), rows[0].Relay())
	}
	close(hold.release)
	waitIdle(t, app, "c_queue")
	// The turn used that line: still one, and the model read it.
	if rows := userRowsWithText(t, app, "c_queue", "verdict from PA"); len(rows) != 1 {
		t.Fatalf("the turn wrote a second line: %d", len(rows))
	}
}

// A recorded relay an agent turn could not consume rides the next turn through
// the same accept path: the text reaches the agent, and the room keeps one line.
func TestRecordedRelayReachesTheNextAgentTurn(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_ride", "", "", "start", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	release := busyTurn(t, app, rt, "c_ride")
	taken, _, err := app.SteerInput(ctx, "c_ride", valueobject.UserInput{
		Text:  "verdict from PA",
		Relay: &valueobject.Relay{ID: "c_pa", Label: "coyote"},
	})
	if err != nil || !taken {
		t.Fatalf("steer = %v, %v", taken, err)
	}
	release()
	waitFor(t, "the follow-up reaching the agent", func() bool {
		return countPromptText(rt, "verdict from PA") > 0
	})
	waitIdle(t, app, "c_ride")
	if rows := userRowsWithText(t, app, "c_ride", "verdict from PA"); len(rows) != 1 {
		t.Fatalf("the recorded relay has %d lines, want one", len(rows))
	}
	if n := countPromptText(rt, "verdict from PA"); n != 1 {
		t.Fatalf("the agent read the text %d times, want once", n)
	}
}

// The whole point of writing the line at accept: a restart between accept and
// the turn that reads it postpones delivery, it does not lose the message. The
// queued job here never runs — the second app stands in for the process that
// came back.
func TestAcceptedRelaySurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	app, _ := fixtureAt(t, dir, okHandler, nil)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_keep", "gw1", "m1", "hi", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	hold := newWedgeJob()
	app.post("c_keep", hold)
	select {
	case <-hold.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the holding job never started")
	}
	if _, err := app.Enqueue(ctx, "c_keep", "", "", "verdict that must survive", nil, appjob.SendOptions{
		Origin: valueobject.TurnOrigin{Kind: enum.TurnOriginConversation},
		Relay:  &valueobject.Relay{ID: "c_pa", Label: "coyote"},
	}); err != nil {
		t.Fatal(err)
	}
	// A second app over the same tree is the restart: same store, no memory.
	restarted, _ := fixtureAt(t, dir, okHandler, nil)
	rows := userRowsWithText(t, restarted, "c_keep", "verdict that must survive")
	if len(rows) != 1 {
		t.Fatalf("after restart the accepted relay has %d lines, want one", len(rows))
	}
	if rows[0].UserType() != enum.LLMUserTypeRelay || rows[0].Relay().ID != "c_pa" {
		t.Fatalf("the surviving line lost its record: %s %+v", rows[0].UserType(), rows[0].Relay())
	}
	close(hold.release)
	waitIdle(t, app, "c_keep")
	if rows := userRowsWithText(t, restarted, "c_keep", "verdict that must survive"); len(rows) != 1 {
		t.Fatalf("the queued turn wrote a second line: %d", len(rows))
	}
}

// A projection answers "what is answering me now": while a turn runs it names the
// binding that turn uses, and after it ends the stored one answers — the active
// binding is gone, not merely ignored.
func TestInfoShowsTheRunningTurnsModel(t *testing.T) {
	p := newProbeTurnAfter(t, 1)
	app, prov := fixture(t, p.handler)
	ctx := t.Context()
	addModel(t, app, prov, "m2")
	if _, err := app.Send(ctx, "c_model", "gw1", "m1", "first", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	p.arm() // the next turn's first call is the one this test holds
	done := make(chan error, 1)
	go func() {
		_, err := app.Send(ctx, "c_model", "gw1", "m2", "second", nil, appjob.SendOptions{}, nil)
		done <- err
	}()
	select {
	case <-p.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the turn never reached the provider")
	}
	info, err := app.Info(ctx, "c_model")
	if err != nil {
		t.Fatal(err)
	}
	if info.ModelID != "m2" || info.Runtime != enum.RuntimeHarness {
		t.Fatalf("mid-turn the projection shows %q %q, want the running turn's m2 harness", info.ModelID, info.Runtime)
	}
	// The job reference, held while the turn runs: after it ends the same job
	// must report no active binding. Reading the worker instead would answer
	// "nothing is running" and prove nothing about the clear.
	app.mu.Lock()
	w := app.workers["c_model"]
	app.mu.Unlock()
	var job interface {
		Active() (appjob.ActiveBinding, bool)
	}
	if w != nil {
		if running := w.Running(); running != nil {
			job, _ = running.(interface {
				Active() (appjob.ActiveBinding, bool)
			})
		}
	}
	if job == nil {
		t.Fatal("the running job does not expose its binding")
	}
	if b, ok := job.Active(); !ok || b.Kind != enum.RuntimeHarness || b.ModelID != "m2" {
		t.Fatalf("mid-turn binding = %+v ok=%v, want harness m2", b, ok)
	}
	release(t, app, p, done, "c_model")
	info, err = app.Info(ctx, "c_model")
	if err != nil {
		t.Fatal(err)
	}
	if info.ModelID != "m2" {
		t.Fatalf("after the turn the projection shows %q, want the stored m2", info.ModelID)
	}
	if _, ok := job.Active(); ok {
		t.Fatal("the active binding outlived its turn on the job that ran it")
	}
}

// A failed turn never rebinds: the projection keeps answering with the model the
// room stored, not the one that was attempted.
func TestFailedTurnKeepsTheStoredModel(t *testing.T) {
	var calls atomic.Int32
	app, prov := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			okHandler(w, r)
			return
		}
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	ctx := t.Context()
	addModel(t, app, prov, "m2")
	if _, err := app.Send(ctx, "c_fail", "gw1", "m1", "first", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Send(ctx, "c_fail", "gw1", "m2", "second", nil, appjob.SendOptions{}, nil); err == nil {
		t.Fatal("the failing turn reported success")
	}
	info, err := app.Info(ctx, "c_fail")
	if err != nil {
		t.Fatal(err)
	}
	if info.ModelID != "m1" {
		t.Fatalf("after a failed turn the projection shows %q, want the stored m1", info.ModelID)
	}
}

// addModel offers one more model on the fixture's provider, so a turn can switch
// to a binding the room does not store yet.
func addModel(t *testing.T, app *ConversationApp, prov *aggregate.Provider, id string) {
	t.Helper()
	m, err := entity.NewModel(id)
	if err != nil {
		t.Fatal(err)
	}
	models := append(prov.Models(), m)
	prov.SetModels(models)
	if err := app.deps.Providers.Save(t.Context(), prov); err != nil {
		t.Fatal(err)
	}
}

// An idle room's relay starts a new turn through the same acceptance boundary:
// the row exists as soon as the send is accepted and the turn never writes a
// second copy.
func TestIdleRelayTurnWritesOneLine(t *testing.T) {
	app, _ := fixture(t, okHandler)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_idleone", "gw1", "m1", "seed", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Enqueue(ctx, "c_idleone", "", "", "idle relay verdict", nil, appjob.SendOptions{
		Origin: valueobject.TurnOrigin{Kind: enum.TurnOriginConversation},
		Relay:  &valueobject.Relay{ID: "c_pa", Label: "coyote", Title: "PA"},
	}); err != nil {
		t.Fatal(err)
	}
	rows := userRowsWithText(t, app, "c_idleone", "idle relay verdict")
	if len(rows) != 1 || rows[0].UserType() != enum.LLMUserTypeRelay || rows[0].Relay().ID != "c_pa" {
		t.Fatalf("accepted idle relay rows = %+v, want one relay line", rows)
	}
	waitIdle(t, app, "c_idleone")
	if rows := userRowsWithText(t, app, "c_idleone", "idle relay verdict"); len(rows) != 1 {
		t.Fatalf("the new turn wrote %d lines, want one", len(rows))
	}
}

// A full queue refuses before the acceptance row exists: the sender can retry
// later without risking a duplicate prompt in history.
func TestFullQueueRefusesRelayBeforeWriting(t *testing.T) {
	app, _ := fixture(t, okHandler)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_fullrelay", "gw1", "m1", "seed", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	app.cfg.MailboxCap = 1
	running := newWedgeJob()
	queued := newWedgeJob()
	app.post("c_fullrelay", running)
	select {
	case <-running.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the running job never started")
	}
	app.post("c_fullrelay", queued)
	waitFor(t, "the queued job occupying the mailbox", func() bool { return app.QueueDepth("c_fullrelay") == 1 })
	_, err := app.Enqueue(ctx, "c_fullrelay", "", "", "must not be written", nil, appjob.SendOptions{
		Origin: valueobject.TurnOrigin{Kind: enum.TurnOriginConversation},
		Relay:  &valueobject.Relay{ID: "c_pa", Label: "coyote"},
	})
	if apperror.As(err).StatusCode() != 429 {
		t.Fatalf("full relay queue = %v, want 429", err)
	}
	if rows := userRowsWithText(t, app, "c_fullrelay", "must not be written"); len(rows) != 0 {
		t.Fatalf("a full-queue refusal wrote %d rows", len(rows))
	}
	close(running.release)
	close(queued.release)
	waitIdle(t, app, "c_fullrelay")
}
