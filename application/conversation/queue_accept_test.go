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
	taken, err := app.SteerInput(ctx, "c_ride", valueobject.UserInput{
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
	if info.ModelID != "m2" {
		t.Fatalf("mid-turn the projection shows %q, want the running turn's m2", info.ModelID)
	}
	// The job reference, held while the turn runs: after it ends the same job
	// must report no active binding. Reading the worker instead would answer
	// "nothing is running" and prove nothing about the clear.
	app.mu.Lock()
	w := app.workers["c_model"]
	app.mu.Unlock()
	var job interface {
		ActiveModel() (string, string, bool)
	}
	if w != nil {
		if running := w.Running(); running != nil {
			job, _ = running.(interface {
				ActiveModel() (string, string, bool)
			})
		}
	}
	if job == nil {
		t.Fatal("the running job does not expose its binding")
	}
	release(t, app, p, done, "c_model")
	info, err = app.Info(ctx, "c_model")
	if err != nil {
		t.Fatal(err)
	}
	if info.ModelID != "m2" {
		t.Fatalf("after the turn the projection shows %q, want the stored m2", info.ModelID)
	}
	if _, _, ok := job.ActiveModel(); ok {
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
