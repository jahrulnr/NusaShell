package job

import (
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/event"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

type receiptSealSurface struct{ persisted []bool }

func (s *receiptSealSurface) Open(string, string, valueobject.TurnOrigin) (event.TurnEventHandler, func(bool, error)) {
	return func(event.TurnEvent) {}, func(persisted bool, _ error) {
		s.persisted = append(s.persisted, persisted)
	}
}

// The job-owned journal seals with the receipt committed by the send's append
// step. This is the exact B-113 regression point: before the fix Execute sealed
// the zero value even though the user row had committed.
func TestJobOwnedSendSealsTheCommittedPersistenceReceipt(t *testing.T) {
	spy := &selectionSpy{}
	d := hydrationDeps(t, spy)
	surface := &receiptSealSurface{}
	d.TurnSurface = surface
	j := &Send{Deps: d, Ctx: t.Context(), Receipt: NewPersistenceReceipt(), ProviderID: "gw1", ModelID: "m1", Message: "hi"}
	j.Result = make(chan error, 1)
	j.Bind("room", nil)
	if err := j.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !j.Receipt.UserMessagePersisted() {
		t.Fatal("the user append did not commit")
	}
	if len(surface.persisted) != 1 || !surface.persisted[0] {
		t.Fatalf("journal seals = %v, want exactly one true receipt", surface.persisted)
	}
}
