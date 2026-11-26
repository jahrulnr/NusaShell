package appconversation

import (
	"testing"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

// The room's option reading is what the room projection carries as
// agentOptions. A pick parked behind a busy turn is not in use yet: the session
// runs the old value until the next turn applies the pick, so the reading keeps
// the old value while the pick waits.
func TestRoomOptionReadingIgnoresAParkedPick(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_read", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	release := busyTurn(t, app, rt, "c_read")
	defer release()
	parked, err := pickWhileBusy(t, "a turn ran", func() (bool, error) {
		return app.SetAgentOption(ctx, "c_read", "mode", "build")
	})
	if err != nil || !parked {
		t.Fatalf("pick = parked %v, %v — want it parked behind the turn", parked, err)
	}
	if got := roomMode(t, app, "c_read"); got != "ask" {
		t.Fatalf("reading shows mode %q while build waits — the session still runs ask", got)
	}

	release()
	waitIdle(t, app, "c_read")
	if got := roomMode(t, app, "c_read"); got != "ask" {
		t.Fatalf("reading shows mode %q before the turn that applies the pick — still ask", got)
	}
	if _, err := app.Send(ctx, "c_read", "", "", "again", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := roomMode(t, app, "c_read"); got != "build" {
		t.Fatalf("reading shows mode %q after the turn ran build", got)
	}
}

// roomMode answers the mode the room's option reading holds as current.
func roomMode(t *testing.T, app *ConversationApp, convID string) string {
	t.Helper()
	opts, _, err := app.AgentOptions(t.Context(), convID)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range opts {
		if o.ID == "mode" {
			return o.Current
		}
	}
	t.Fatal("reading carries no mode option")
	return ""
}
