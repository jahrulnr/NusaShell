package appconversation

import (
	"testing"

	appjob "github.com/jahrulnr/nusashell/application/job"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

// A live session that has offered nothing yet answers empty, a transient state
// the picker may read again. The reading keeps that state: folding it into ready
// would tell the owner the agent has no options, for good.
func TestRoomReadingKeepsAnEmptyLiveSessionEmpty(t *testing.T) {
	app, rt, _ := agentFixture(t, enum.ApprovalPolicyAutoAll)
	ctx := t.Context()
	if _, err := app.Send(ctx, "c_empty", "", "", "look", nil, appjob.SendOptions{Agent: "fake"}, nil); err != nil {
		t.Fatal(err)
	}
	rt.mu.Lock()
	rt.emptyLive = true
	rt.mu.Unlock()

	opts, state, err := app.AgentOptions(ctx, "c_empty")
	if err != nil {
		t.Fatal(err)
	}
	if state != dservice.OptionsEmpty {
		t.Fatalf("state = %q, want empty: the live session has offered nothing yet", state)
	}
	if len(opts) != 0 {
		t.Fatalf("options = %+v, want none", opts)
	}
}
