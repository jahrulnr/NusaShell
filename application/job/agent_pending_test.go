package job

import (
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
)

// sendWithEvents runs one user turn on room and returns the events it emitted.
func sendWithEvents(t *testing.T, d Deps, opts SendOptions) ([]event.TurnEvent, error) {
	t.Helper()
	var events []event.TurnEvent
	j := &Send{
		Deps: d, Ctx: t.Context(), Receipt: NewPersistenceReceipt(),
		ProviderID: "gw1", ModelID: "m1", Message: "hi", Opts: opts,
		OnEvent: func(ev event.TurnEvent) { events = append(events, ev) },
	}
	j.Result = make(chan error, 1)
	j.Bind("room", nil)
	if err := j.Execute(t.Context()); err != nil {
		return events, err
	}
	return events, <-j.Result
}

// pickNotices is the text of every notice a turn raised about a pick it did not
// apply.
func pickNotices(events []event.TurnEvent) []string {
	var texts []string
	for _, ev := range events {
		if ev.Kind() == enum.TurnEventNotice && ev.NoticeTitle() == "Pick not applied" {
			texts = append(texts, ev.Delta())
		}
	}
	return texts
}

// A pick parked for an agent room is not applied, and the owner is told so with
// the notice a refused pick already gets. The agent takes no effort, so a silent
// drop would hide the refusal from the owner.
func TestAgentRoomDropsAParkedEffortWithANotice(t *testing.T) {
	spy := &selectionSpy{}
	d := hydrationDeps(t, spy)
	useAgent(t, &d)
	if err := send(t, d, spy, SendOptions{Agent: "claude"}); err != nil {
		t.Fatal(err)
	}
	parkAhead(t, d, "room", enum.EffortMax)

	events, err := sendWithEvents(t, d, SendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if effort, upstream := storedRecord(t, d); effort != "" || upstream != "" {
		t.Fatalf("agent room stored %q, %q from the parked pick — the agent applies neither", effort, upstream)
	}
	if _, ok, err := d.Convs.PendingSelection(t.Context(), "room"); err != nil || ok {
		t.Fatalf("the consumed pick was not cleared: %v, %v", ok, err)
	}
	notes := pickNotices(events)
	if len(notes) != 1 || !strings.Contains(notes[0], "effort") || !strings.Contains(notes[0], "not applied") {
		t.Fatalf("notices = %q, want one saying the effort pick is not applied", notes)
	}
}

// The control for the test above: a provider room takes the pick it parked and
// says nothing, so the notice is for agent rooms only.
func TestProviderRoomAppliesAParkedEffortWithoutANotice(t *testing.T) {
	spy := &selectionSpy{}
	d := hydrationDeps(t, spy)
	if err := send(t, d, spy, SendOptions{}); err != nil {
		t.Fatal(err)
	}
	parkAhead(t, d, "room", enum.EffortMax)

	events, err := sendWithEvents(t, d, SendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := spy.efforts[len(spy.efforts)-1]; got != enum.EffortMax {
		t.Fatalf("the turn ran with %q, want the parked max", got)
	}
	if notes := pickNotices(events); len(notes) != 0 {
		t.Fatalf("a provider room was told its pick is not applied: %q", notes)
	}
}
