package job

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

func mustElicitation(t *testing.T) valueobject.Elicitation {
	t.Helper()
	e, err := valueobject.NewElicitation("eli_1", "form", "Pick one",
		json.RawMessage(`{"type":"object","properties":{"env":{"type":"string","enum":["dev","prod"]}},"required":["env"]}`), "")
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// resolvedEvents collects the delta of every elicitation_resolved event.
func resolvedEvents() (event.TurnEventHandler, *[]string) {
	var deltas []string
	return func(ev event.TurnEvent) {
		if ev.Kind() == enum.TurnEventElicitationResolved {
			deltas = append(deltas, ev.Delta())
		}
	}, &deltas
}

// The owner's answer passes through with the owner recorded as its source.
func TestElicitationAskOwnerAnswer(t *testing.T) {
	b := NewElicitations(time.Minute)
	emit, deltas := resolvedEvents()
	done := make(chan valueobject.ElicitationAnswer, 1)
	go func() {
		a, err := b.ask(t.Context(), "c1", mustElicitation(t), emit)
		if err != nil {
			t.Errorf("ask: %v", err)
		}
		done <- a
	}()
	for len(b.Pending("c1")) == 0 {
		time.Sleep(time.Millisecond)
	}
	if err := b.Answer("c1", "eli_1", valueobject.ElicitationAnswer{Action: "accept", Content: json.RawMessage(`{"env":"dev"}`)}); err != nil {
		t.Fatal(err)
	}
	a := <-done
	if a.Action != "accept" || a.ResolvedBy != "owner" {
		t.Fatalf("answer = %+v, want accept/owner", a)
	}
	if len(*deltas) != 1 || (*deltas)[0] != "accept" {
		t.Fatalf("resolved events = %v", *deltas)
	}
}

// The answer deadline resolves the request itself: the agent gets cancel,
// the transcript reads expired, and nobody answered.
func TestElicitationDeadlineExpiresAsCancel(t *testing.T) {
	b := NewElicitations(20 * time.Millisecond)
	emit, deltas := resolvedEvents()
	a, err := b.ask(t.Context(), "c1", mustElicitation(t), emit)
	if err != nil {
		t.Fatalf("an expiry is not the caller's error: %v", err)
	}
	if a.Action != "cancel" || a.ResolvedBy != "expired" {
		t.Fatalf("expired answer = %+v, want cancel/expired", a)
	}
	if len(*deltas) != 1 || (*deltas)[0] != "expired" {
		t.Fatalf("resolved events = %v, want one marked expired", *deltas)
	}
	if err := b.Answer("c1", "eli_1", valueobject.ElicitationAnswer{Action: "accept", Content: json.RawMessage(`{"env":"dev"}`)}); err == nil {
		t.Fatal("an answer after the deadline resolved")
	}
}

// A dying turn cancels the request; unanswered is recorded, and the caller
// keeps the context's error.
func TestElicitationTurnEndCancelsAsUnanswered(t *testing.T) {
	b := NewElicitations(time.Minute)
	emit, deltas := resolvedEvents()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var a valueobject.ElicitationAnswer
	var err error
	go func() {
		defer close(done)
		a, err = b.ask(ctx, "c1", mustElicitation(t), emit)
	}()
	for len(b.Pending("c1")) == 0 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	if !errors.Is(err, context.Canceled) || a.Action != "cancel" || a.ResolvedBy != "unanswered" {
		t.Fatalf("ctx-done answer = %+v, %v — want cancel/unanswered + ctx err", a, err)
	}
	if len(*deltas) != 1 || (*deltas)[0] != "unanswered" {
		t.Fatalf("resolved events = %v, want one unanswered mark", *deltas)
	}
}

// Pending reports the stamped deadline so a card can count down.
func TestElicitationPendingCarriesDeadline(t *testing.T) {
	b := NewElicitations(15 * time.Minute)
	emit := func(event.TurnEvent) {}
	done := make(chan struct{})
	go func() { defer close(done); _, _ = b.ask(t.Context(), "c1", mustElicitation(t), emit) }()
	var dl time.Time
	for dl.IsZero() {
		for _, e := range b.Pending("c1") {
			dl = e.Deadline()
		}
		time.Sleep(time.Millisecond)
	}
	if until := time.Until(dl); until < 14*time.Minute || until > 15*time.Minute {
		t.Fatalf("deadline in %v — want ~15m", until)
	}
	_ = b.Answer("c1", "eli_1", valueobject.ElicitationAnswer{Action: "decline"})
	<-done
}

// The broker still refuses an answer for a request nothing waits on.
func TestElicitationAnswerUnknownIs404(t *testing.T) {
	b := NewElicitations(time.Minute)
	err := b.Answer("c1", "eli_ghost", valueobject.ElicitationAnswer{Action: "decline"})
	if err == nil || apperror.As(err).StatusCode() != 404 {
		t.Fatalf("Answer(ghost) = %v, want 404", err)
	}
}
