package appconversation

import (
	"testing"
	"time"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// The three delivery outcomes are one provenance mode: live steer, queued
// fallback, and a new turn in an idle room each leave exactly one relay-typed
// user line. Delivery chooses when the words run, never whether the record is
// written — and no outcome may leave zero lines.
func TestRelayDeliveryPathsNeverProduceZeroLines(t *testing.T) {
	p := newProbeTurn(t, 1)
	app, _ := fixture(t, p.handler)
	ctx := t.Context()
	relay := &valueobject.Relay{ID: "c_pa", Label: "coyote", Title: "PA"}
	assertOneRelayLine := func(convID, text string) {
		t.Helper()
		rows := userRowsWithText(t, app, convID, text)
		if len(rows) != 1 {
			t.Fatalf("%s has %d lines for %q, want exactly one", convID, len(rows), text)
		}
		if rows[0].UserType() != enum.LLMUserTypeRelay || rows[0].Relay().ID != "c_pa" || rows[0].Relay().Label != "coyote" || rows[0].Relay().Title != "PA" {
			t.Fatalf("%s lost relay provenance: %s %+v", convID, rows[0].UserType(), rows[0].Relay())
		}
	}

	// Live steer: the turn is really waiting in its provider call when the
	// cross-thread input arrives.
	done := startTurn(t, app, p, "c_live", "owner question")
	taken, _, err := app.SteerInput(ctx, "c_live", valueobject.UserInput{Text: "live relay", Relay: relay})
	if err != nil || !taken {
		t.Fatalf("live steer = %v, %v", taken, err)
	}
	assertOneRelayLine("c_live", "live relay")
	release(t, app, p, done, "c_live")
	assertOneRelayLine("c_live", "live relay")

	// Queued fallback: the room is occupied, so acceptance writes the line while
	// the message waits; running it later must not write another.
	if _, err := app.Send(ctx, "c_wait", "gw1", "m1", "seed", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	hold := newWedgeJob()
	app.post("c_wait", hold)
	select {
	case <-hold.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the holding job never started")
	}
	position, err := app.Enqueue(ctx, "c_wait", "", "", "queued relay", nil, appjob.SendOptions{
		Origin: valueobject.TurnOrigin{Kind: enum.TurnOriginConversation},
		Relay:  relay,
	})
	if err != nil || position == 0 {
		t.Fatalf("queued relay = position %d, %v; want waiting", position, err)
	}
	assertOneRelayLine("c_wait", "queued relay")
	close(hold.release)
	waitIdle(t, app, "c_wait")
	assertOneRelayLine("c_wait", "queued relay")

	// Idle room/new turn: control now reaches the turn through the same
	// acceptance boundary. Position zero means the turn started immediately;
	// provenance was already written before that post.
	if _, err := app.Send(ctx, "c_idle", "gw1", "m1", "seed", nil, appjob.SendOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	position, err = app.Enqueue(ctx, "c_idle", "", "", "idle relay", nil, appjob.SendOptions{
		Origin: valueobject.TurnOrigin{Kind: enum.TurnOriginConversation},
		Relay:  relay,
	})
	if err != nil || position != 0 {
		t.Fatalf("idle relay = position %d, %v; want an immediate turn", position, err)
	}
	assertOneRelayLine("c_idle", "idle relay")
	waitIdle(t, app, "c_idle")
	assertOneRelayLine("c_idle", "idle relay")
}
