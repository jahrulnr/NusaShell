package job

import (
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// countingSurface answers a handler and counts how often it was asked.
type countingSurface struct {
	calls  int
	sealed int
}

func (s *countingSurface) Open(string, string, valueobject.TurnOrigin) (event.TurnEventHandler, func(bool, error)) {
	s.calls++
	return func(event.TurnEvent) {}, func(bool, error) { s.sealed++ }
}

// A job that already carries a handler keeps it: the surface fills a gap, it never
// opens a second journal for a turn a socket already admitted.
func TestLiveTurnNeverOpensASecondJournal(t *testing.T) {
	surface := &countingSurface{}
	d := Deps{TurnSurface: surface}

	mine := func(event.TurnEvent) {}
	got, seal := liveTurn(d, "c_1", "send", valueobject.TurnOrigin{}, mine)
	if got == nil {
		t.Fatal("a job with a handler lost it")
	}
	if surface.calls != 0 {
		t.Fatalf("a job that already had a handler opened %d journals", surface.calls)
	}
	seal(true, nil)

	got, seal = liveTurn(d, "c_1", "send", valueobject.TurnOrigin{}, nil)
	if got == nil {
		t.Fatal("a blind job was left without a handler")
	}
	if surface.calls != 1 {
		t.Fatalf("a blind job opened %d journals, want one", surface.calls)
	}
	seal(true, nil)
	// Only the turn that got a surface has a journal to close; the passthrough
	// seal of the already-journaled turn is a no-op.
	if surface.sealed != 1 {
		t.Fatalf("the surface sealed %d turns, want the one it opened", surface.sealed)
	}
}

// A composition without the surface (the CLI) runs blind, and the seal is a
// no-op — nothing to close.
func TestLiveTurnWithoutASurface(t *testing.T) {
	got, seal := liveTurn(Deps{}, "c_1", "send", valueobject.TurnOrigin{}, nil)
	if got != nil {
		t.Fatal("a job without a surface got a handler from nowhere")
	}
	seal(true, nil)
}

// A surface that refuses (a full pool) leaves the turn blind, never failed.
func TestLiveTurnKeepsARefusalBlind(t *testing.T) {
	d := Deps{TurnSurface: refusingSurface{}}
	got, seal := liveTurn(d, "c_1", "send", valueobject.TurnOrigin{}, nil)
	if got != nil {
		t.Fatal("a refused open still produced a handler")
	}
	seal(true, nil)
}

type refusingSurface struct{}

func (refusingSurface) Open(string, string, valueobject.TurnOrigin) (event.TurnEventHandler, func(bool, error)) {
	return nil, nil
}
