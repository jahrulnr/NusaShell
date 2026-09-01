package automation

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nusashell/domain"
)

func openTestStore(t *testing.T) *SQLite {
	t.Helper()
	st, err := OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// Regression: ClaimWait used SELECT-then-UPDATE without a status guard, so
// two concurrent claimers could both flip the same pending wait and both
// start the workflow run (duplicate execution).
func TestClaimWaitExactlyOneWinner(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	wake := time.Now().UTC().Add(-time.Minute)
	rec := &domain.WaitRecord{ID: "w1", WorkflowRunID: "run-1", Status: domain.SchedulePending, WakeAt: &wake}
	if err := st.PutWait(ctx, rec); err != nil {
		t.Fatal(err)
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := st.ClaimWait(ctx, "w1")
			if err != nil {
				t.Errorf("ClaimWait: %v", err)
				return
			}
			if got != nil {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("winners = %d, want exactly 1", winners.Load())
	}
}

// Same contract as ClaimWait, for the schedule timer table.
func TestClaimScheduleExactlyOneWinner(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	next := time.Now().UTC().Add(-time.Minute)
	rec := &domain.ScheduleRecord{ID: "s1", WorkflowID: "wf", TriggerID: "s1", Kind: domain.TriggerInterval, RunAt: next, NextRunAt: next, Status: domain.SchedulePending, CreatedAt: next}
	if err := st.PutSchedule(ctx, rec); err != nil {
		t.Fatal(err)
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := st.ClaimSchedule(ctx, "s1", time.Now().UTC())
			if err != nil {
				t.Errorf("ClaimSchedule: %v", err)
				return
			}
			if got != nil {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("winners = %d, want exactly 1", winners.Load())
	}
}

// Regression: Append computed the next log sequence with a read-then-write
// outside a transaction, so concurrent writers could receive the same
// sequence number and one insert died with a primary-key conflict (lost log
// chunk). The sequence must be allocated inside a serialized transaction.
func TestAppendSequenceIsSerialized(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	const writers, perWriter = 8, 12
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				chunk := domain.LogChunk{JobID: "job-1", Timestamp: time.Now().UTC(), Text: "x"}
				if err := st.Append(ctx, chunk); err != nil {
					t.Errorf("Append: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	// Read's after=0 means "seq > 0", so a single read with a generous
	// limit returns the full set; sequences must be exactly 1..N.
	chunks, err := st.Read(ctx, "job-1", 0, writers*perWriter+8)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != writers*perWriter {
		t.Fatalf("stored chunks = %d, want %d", len(chunks), writers*perWriter)
	}
	seen := make(map[uint64]bool)
	for _, c := range chunks {
		if seen[c.Sequence] {
			t.Fatalf("duplicate sequence %d", c.Sequence)
		}
		seen[c.Sequence] = true
		if c.Sequence < 1 || c.Sequence > uint64(writers*perWriter) {
			t.Fatalf("sequence %d out of range", c.Sequence)
		}
	}
}
