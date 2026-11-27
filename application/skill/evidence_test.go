package skill

import (
	"fmt"
	"sync"
	"testing"
)

func TestEvidenceTakeMatchesOnlyItsMessage(t *testing.T) {
	ev := NewEvidence()
	ev.RecordEdit("room", "m1", "old", nil)
	if _, ok := ev.Take("room", "m2"); ok {
		t.Fatal("a take for another message consumed the record")
	}
	rec, ok := ev.Take("room", "m1")
	if !ok || rec.MsgID != "m1" || rec.Previous != "old" {
		t.Fatalf("take = %+v, %v; want the recorded edit", rec, ok)
	}
	if _, ok := ev.Take("room", "m1"); ok {
		t.Fatal("a record was taken twice")
	}
}

func TestEvidenceNewestEditReplacesTheRoomsOlder(t *testing.T) {
	ev := NewEvidence()
	ev.RecordEdit("room", "m1", "first", nil)
	ev.RecordEdit("room", "m2", "second", nil)
	if _, ok := ev.Take("room", "m1"); ok {
		t.Fatal("an older edit survived a newer one in the same room")
	}
	if rec, ok := ev.Take("room", "m2"); !ok || rec.Previous != "second" {
		t.Fatalf("newest edit = %+v, %v", rec, ok)
	}
}

func TestEvidenceRoomsAreIndependent(t *testing.T) {
	ev := NewEvidence()
	ev.RecordEdit("room-a", "m1", "text", nil)
	if _, ok := ev.Take("room-b", "m1"); ok {
		t.Fatal("another room took the edit")
	}
	if _, ok := ev.Take("room-a", "m1"); !ok {
		t.Fatal("the recording room lost its edit")
	}
}

// However many finished turns race for one record, exactly one consumes it.
func TestEvidenceTakeConsumesOnce(t *testing.T) {
	ev := NewEvidence()
	ev.RecordEdit("room", "m1", "text", nil)
	var wg sync.WaitGroup
	var mu sync.Mutex
	taken := 0
	for range 16 {
		wg.Go(func() {
			if _, ok := ev.Take("room", "m1"); ok {
				mu.Lock()
				taken++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if taken != 1 {
		t.Fatalf("takes = %d, want exactly 1", taken)
	}
}

func TestEvidenceRecordsManyRoomsConcurrently(t *testing.T) {
	ev := NewEvidence()
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			ev.RecordEdit(fmt.Sprintf("room-%d", i), "m", "text", nil)
		})
	}
	wg.Wait()
	for i := range 32 {
		if _, ok := ev.Take(fmt.Sprintf("room-%d", i), "m"); !ok {
			t.Fatalf("room-%d lost its record", i)
		}
	}
}

func TestEvidenceDiscardDropsOnlyItsMessage(t *testing.T) {
	ev := NewEvidence()
	ev.RecordEdit("room", "m1", "old", nil)
	ev.DiscardEdit("room", "m2")
	if _, ok := ev.Take("room", "m1"); !ok {
		t.Fatal("a discard for another message dropped the record")
	}
	ev.RecordEdit("room", "m1", "old", nil)
	ev.DiscardEdit("room", "m1")
	if _, ok := ev.Take("room", "m1"); ok {
		t.Fatal("a discarded rewrite was still taken")
	}
}
