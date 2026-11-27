package repoconversation

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
)

// parkedRoom stores one room with a pick waiting for its next turn.
func parkedRoom(t *testing.T) (*ConversationRepository, string) {
	t.Helper()
	root := t.TempDir()
	r := NewConversationRepository(root)
	conv, err := aggregate.NewConversation("c_park")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	upstream := "Alpha"
	if err := r.SavePendingSelection(t.Context(), "c_park", drepo.PendingSelection{
		AgentID: "dewey", AgentModel: "swe-2-1", Upstream: &upstream,
		Options: map[string]string{"mode": "accept-edits"}, AcceptedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	return r, root
}

// The pick waits in its own file beside the record — a turn's Save rewrites the
// record wholesale and would take a field there with it.
func TestPendingSelectionSurvivesARecordSave(t *testing.T) {
	r, root := parkedRoom(t)
	conv, err := r.Load(t.Context(), "c_park")
	if err != nil {
		t.Fatal(err)
	}
	conv.SetTitle("renamed while a pick waits")
	if err := r.Save(t.Context(), conv); err != nil {
		t.Fatal(err)
	}
	sel, ok, err := r.PendingSelection(t.Context(), "c_park")
	if err != nil || !ok {
		t.Fatalf("pending = %+v, %v, %v", sel, ok, err)
	}
	if sel.AgentID != "dewey" || sel.AgentModel != "swe-2-1" || sel.Options["mode"] != "accept-edits" {
		t.Fatalf("a record save lost the pick: %+v", sel)
	}
	if _, err := os.Stat(filepath.Join(root, "c_park", pendingFile)); err != nil {
		t.Fatalf("the pick does not live beside the record: %v", err)
	}
}

// A pick replaced before the turn: the last one wins per field, one runtime at a
// time, and the later accept time replaces the earlier.
func TestPendingSelectionMergesLastPickWins(t *testing.T) {
	r, _ := parkedRoom(t)
	effort := enum.EffortMax
	if err := r.SavePendingSelection(t.Context(), "c_park", drepo.PendingSelection{
		ProviderID: "openrouter", ModelID: "swe-2", Effort: &effort, AcceptedAt: time.Now().UTC(),
		Options: map[string]string{"mode": "ask", "strict": "true"},
	}); err != nil {
		t.Fatal(err)
	}
	sel, _, err := r.PendingSelection(t.Context(), "c_park")
	if err != nil {
		t.Fatal(err)
	}
	// A provider pick replaces the agent runtime whole; effort from the older
	// pick survives its field, and options merge by id.
	if sel.AgentID != "" || sel.AgentModel != "" || sel.ProviderID != "openrouter" || sel.ModelID != "swe-2" {
		t.Fatalf("runtime merged two picks: %+v", sel)
	}
	if sel.Effort == nil || sel.Effort.String() != "max" || sel.Upstream == nil || *sel.Upstream != "Alpha" {
		t.Fatalf("effort/upstream did not survive: %+v", sel)
	}
	if sel.Options["mode"] != "ask" || sel.Options["strict"] != "true" {
		t.Fatalf("options did not merge by id: %+v", sel.Options)
	}
}

// A deleted room leaves no parked selection behind.
func TestPendingSelectionLeavesWithTheRoom(t *testing.T) {
	r, root := parkedRoom(t)
	if err := r.Delete(t.Context(), "c_park"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "c_park", pendingFile)); !os.IsNotExist(err) {
		t.Fatalf("a deleted room kept its pick: %v", err)
	}
	if _, ok, err := r.PendingSelection(t.Context(), "c_park"); err != nil || ok {
		t.Fatalf("a deleted room still waits for something: %v, %v", ok, err)
	}
}

// A pick for a room that does not exist is refused, not written into a
// directory of its own.
func TestPendingSelectionNeedsTheRoom(t *testing.T) {
	r := NewConversationRepository(t.TempDir())
	if err := r.SavePendingSelection(t.Context(), "c_missing", drepo.PendingSelection{AgentID: "dewey"}); err == nil {
		t.Fatal("a pick for a missing room was written")
	}
}

// A clear names the generation it consumed: a pick parked while that turn ran is
// newer and stays, or a turn's own clear would lose an owner's choice silently.
func TestClearPendingSelectionLeavesANewerGeneration(t *testing.T) {
	r, _ := parkedRoom(t)
	stored, ok, err := r.PendingSelection(t.Context(), "c_park")
	if err != nil || !ok {
		t.Fatalf("pending = %+v, %v, %v", stored, ok, err)
	}
	older := stored.AcceptedAt.Add(-time.Minute)
	if err := r.ClearPendingSelection(t.Context(), "c_park", older); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := r.PendingSelection(t.Context(), "c_park"); err != nil || !ok {
		t.Fatalf("clearing an older generation took the newer pick: %v, %v", ok, err)
	}
	// Its own generation still clears.
	if err := r.ClearPendingSelection(t.Context(), "c_park", stored.AcceptedAt); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := r.PendingSelection(t.Context(), "c_park"); err != nil || ok {
		t.Fatalf("the consumed generation survived its own clear: %v, %v", ok, err)
	}
}

// The compare and the remove share SavePendingSelection's lock, so a pick written
// while an older clear runs survives. A sequential test cannot see that window —
// this one interleaves a save with a clear and asserts the invariant after every
// round: the newer generation is never lost.
func TestClearAndSaveDoNotLoseANewerGeneration(t *testing.T) {
	r, _ := parkedRoom(t)
	effort := enum.EffortMax
	for i := range 200 {
		stored, ok, err := r.PendingSelection(t.Context(), "c_park")
		if err != nil || !ok {
			t.Fatalf("round %d: pending = %+v, %v, %v", i, stored, ok, err)
		}
		older := stored.AcceptedAt.Add(-time.Minute)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = r.SavePendingSelection(t.Context(), "c_park", drepo.PendingSelection{
				Effort: &effort, AcceptedAt: time.Now().UTC(),
			})
		}()
		go func() {
			defer wg.Done()
			_ = r.ClearPendingSelection(t.Context(), "c_park", older)
		}()
		wg.Wait()
		if _, ok, err := r.PendingSelection(t.Context(), "c_park"); err != nil || !ok {
			t.Fatalf("round %d lost the pick a concurrent save wrote: %v, %v", i, ok, err)
		}
	}
}
