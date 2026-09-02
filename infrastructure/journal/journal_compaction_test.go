package journal

import (
	"context"
	"testing"

	"nusashell/domain"
)

// TestRecordCompactionIgnoredByWorkspaceState guards the compaction audit
// trail: compaction events live in the journal sidecar but must never leak
// into the workspace change state (which feeds the hydration slot).
func TestRecordCompactionIgnoredByWorkspaceState(t *testing.T) {
	j := New(t.TempDir())
	ev := domain.CompactionEvent{
		Trigger: domain.CompactionTriggerInitial,
		Model:   "compact-model",
		Summary: "handoff summary",
	}
	if err := j.RecordCompaction("c1", ev); err != nil {
		t.Fatal(err)
	}
	state, err := j.SessionState(context.Background(), "c1", "/ws")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Changes) != 0 {
		t.Fatalf("changes = %d, want 0 (compaction events must not leak)", len(state.Changes))
	}
	if state.JournalPath == "" {
		t.Fatal("journal path missing for a conversation with events")
	}
}
