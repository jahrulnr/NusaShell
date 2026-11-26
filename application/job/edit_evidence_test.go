package job

import (
	"context"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// editLog is what the evidence port heard, and how many messages the room's
// store still held when it heard it.
type editLog struct {
	store     drepo.Conversation
	calls     int
	convID    string
	msgID     string
	prior     string
	tail      []entity.Message
	held      int
	discards  int
	discarded string
}

func (l *editLog) RecordEdit(convID, msgID, previous string, tail []entity.Message) {
	l.calls++
	l.convID, l.msgID, l.prior, l.tail = convID, msgID, previous, tail
	if conv, err := l.store.Load(context.Background(), convID); err == nil {
		l.held = len(conv.Messages())
	}
}

func (l *editLog) DiscardEdit(_, msgID string) {
	l.discards++
	l.discarded = msgID
}

func TestEditHandsTheReplacedTailOverBeforeTheRewrite(t *testing.T) {
	svc := doneSvc{}
	d := hydrationDeps(t, svc)
	conv := seedRoom(t, d.Convs, "room", "original")
	conv.SetProviderID("gw1")
	conv.SetModelID("m1")
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	model, ok := mustProviderModel(t, d, "gw1", "m1")
	if !ok {
		t.Fatal("seed provider missing m1")
	}
	if err := d.runTurn(t.Context(), "room", conv, d.Environments[enum.EnvironmentLocal], nil, model, svc, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	before, err := d.Convs.Load(t.Context(), "room")
	if err != nil {
		t.Fatal(err)
	}
	target := before.Messages()[0].ID()
	rec := &editLog{store: d.Convs}
	d.EditEvidence = rec
	j := &Edit{Deps: d, MsgID: target, Content: "edited"}
	j.Bind("room", nil)
	if err := j.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	if rec.calls != 1 {
		t.Fatalf("RecordEdit calls = %d, want 1", rec.calls)
	}
	if rec.convID != "room" || rec.msgID != target || rec.prior != "original" {
		t.Fatalf("recorded edit = %q %q %q", rec.convID, rec.msgID, rec.prior)
	}
	if want := len(before.Messages()) - 1; len(rec.tail) != want || rec.tail[len(rec.tail)-1].Content() != "done" {
		t.Fatalf("tail = %d messages, want the %d after the target, ending in the reply", len(rec.tail), want)
	}
	if rec.held != len(before.Messages()) {
		t.Fatalf("the store held %d messages when the edit was recorded, want the %d from before the rewrite", rec.held, len(before.Messages()))
	}
	if rec.discards != 0 {
		t.Fatalf("a finished edit discarded its rewrite %d times", rec.discards)
	}
}

func TestEditDiscardsItsRewriteWhenTheEditFails(t *testing.T) {
	svc := doneSvc{}
	d := hydrationDeps(t, svc)
	conv := seedRoom(t, d.Convs, "room", "original")
	conv.SetProviderID("gw1")
	conv.SetModelID("m1")
	if err := d.Convs.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	model, ok := mustProviderModel(t, d, "gw1", "m1")
	if !ok {
		t.Fatal("seed provider missing m1")
	}
	if err := d.runTurn(t.Context(), "room", conv, d.Environments[enum.EnvironmentLocal], nil, model, svc, nil, nil, valueobject.TurnOrigin{}); err != nil {
		t.Fatal(err)
	}
	before, err := d.Convs.Load(t.Context(), "room")
	if err != nil {
		t.Fatal(err)
	}
	target := before.Messages()[0].ID()
	rec := &editLog{store: d.Convs}
	d.EditEvidence = rec
	j := &Edit{Deps: d, MsgID: target, Content: "edited", ProviderID: "no-such-provider"}
	j.Bind("room", nil)
	if err := j.Execute(t.Context()); err == nil {
		t.Fatal("an edit to a provider the room does not have succeeded")
	}
	if rec.calls != 1 || rec.discards != 1 || rec.discarded != target {
		t.Fatalf("recorded %d edits and discarded %d (%q); want the failed edit's rewrite discarded once", rec.calls, rec.discards, rec.discarded)
	}
}
