package appconversation

import (
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
