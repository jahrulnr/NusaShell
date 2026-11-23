package job

import (
	"context"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
)

// selectionSpy records the effort and upstream each provider call ran with,
// and fails the calls it is told to.
type selectionSpy struct {
	efforts   []enum.Effort
	upstreams []string
	fail      error
}

func (s *selectionSpy) Send(_ context.Context, conv *aggregate.Conversation, _ *aggregate.Provider, model entity.Model, _ event.TurnEventHandler) error {
	s.efforts = append(s.efforts, model.Effort())
	s.upstreams = append(s.upstreams, model.Upstream())
	if s.fail != nil {
		return s.fail
	}
	m, _ := entity.NewMessage(enum.RoleAssistant, "done")
	m.SetFinishReason(enum.FinishReasonStop)
	conv.Append(m)
	return nil
}

// send runs one user turn on room through the Send job.
func send(t *testing.T, d Deps, spy *selectionSpy, opts SendOptions) error {
	t.Helper()
	j := &Send{Deps: d, Ctx: t.Context(), Receipt: NewPersistenceReceipt(), ProviderID: "gw1", ModelID: "m1", Message: "hi", Opts: opts}
	j.Result = make(chan error, 1)
	j.Bind("room", nil)
	if err := j.Execute(t.Context()); err != nil {
		return err
	}
	return <-j.Result
}

func storedSelection(t *testing.T, d Deps) (enum.Effort, string) {
	t.Helper()
	conv, err := d.Convs.Load(t.Context(), "room")
	if err != nil {
		t.Fatal(err)
	}
	return conv.Effort(), conv.Upstream()
}

// The effort a room was given outlives the browser tab and the server: a turn
// that names none runs with the room's, and a turn that names one becomes the
// room's once it has run.
func TestRoomKeepsTheEffortAndUpstreamItWasGiven(t *testing.T) {
	spy := &selectionSpy{}
	d := hydrationDeps(t, spy)

	// A new room takes the choice its first turn carries.
	if err := send(t, d, spy, SendOptions{Effort: enum.EffortHigh, Upstream: "Alpha"}); err != nil {
		t.Fatal(err)
	}
	if effort, upstream := storedSelection(t, d); effort != enum.EffortHigh || upstream != "Alpha" {
		t.Fatalf("stored = %q, %q", effort, upstream)
	}

	// A turn from anywhere that says nothing runs with the room's.
	if err := send(t, d, spy, SendOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := spy.efforts[len(spy.efforts)-1]; got != enum.EffortHigh {
		t.Fatalf("a turn with no effort ran with %q, want the room's high", got)
	}
	if got := spy.upstreams[len(spy.upstreams)-1]; got != "Alpha" {
		t.Fatalf("a turn with no upstream ran with %q, want the room's Alpha", got)
	}

	// An explicit default asks for the model's own and wins over the room's
	// for that turn; it is not "nothing was said".
	if err := send(t, d, spy, SendOptions{Effort: enum.EffortDefault}); err != nil {
		t.Fatal(err)
	}
	if got := spy.efforts[len(spy.efforts)-1]; got != "" {
		t.Fatalf("an explicit default ran with %q, want the model's own", got)
	}
	if effort, _ := storedSelection(t, d); effort != "" {
		t.Fatalf("an explicit default left %q stored", effort)
	}
}

// A turn that fails changes nothing the room keeps, like the model it was
// bound to: the retry runs with what the room had.
func TestFailedTurnLeavesTheRoomsEffortAlone(t *testing.T) {
	spy := &selectionSpy{}
	d := hydrationDeps(t, spy)
	if err := send(t, d, spy, SendOptions{Effort: enum.EffortLow}); err != nil {
		t.Fatal(err)
	}
	spy.fail = context.DeadlineExceeded
	if err := send(t, d, spy, SendOptions{Effort: enum.EffortMax}); err == nil {
		t.Fatal("the turn should have failed")
	}
	if effort, _ := storedSelection(t, d); effort != enum.EffortLow {
		t.Fatalf("a failed turn stored %q, want the room's low", effort)
	}
}
