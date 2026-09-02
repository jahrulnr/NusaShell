package journal

import (
	"testing"

	"nusashell/domain"
)

func TestStore_appendCompactionRoundtrip(t *testing.T) {
	dir := t.TempDir()
	st := newStore(dir)
	conv := "conv_compact"
	ev := domain.CompactionEvent{
		Trigger:    domain.CompactionTriggerEmergency,
		Model:      "compact-model",
		KeepBudget: 64000,
		Summary:    "handoff summary text",
	}
	if err := st.appendCompaction(conv, ev); err != nil {
		t.Fatal(err)
	}
	events, err := st.readAll(conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events want 1", len(events))
	}
	got := events[0]
	if got.Type != eventTypeCompaction {
		t.Fatalf("type = %q want %q", got.Type, eventTypeCompaction)
	}
	if got.Compaction == nil || *got.Compaction != ev {
		t.Fatalf("compaction payload = %+v want %+v", got.Compaction, ev)
	}
	// The audit event must survive the turn-end gzip rollover so the
	// compaction history stays readable after archiving.
	if err := st.archive(conv); err != nil {
		t.Fatal(err)
	}
	events, err = st.readAll(conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != eventTypeCompaction {
		t.Fatalf("after archive: got %d events, first type %q", len(events), events[0].Type)
	}
}
