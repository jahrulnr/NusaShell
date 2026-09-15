package domain

import (
	"encoding/json"
	"testing"
	"time"
)

// TestAnnouncedRecordsUnmarshalOldStringFormat proves conversations persisted
// with the legacy []string format (last_announced_records: ["id1","id2"])
// are read correctly into the new AnnouncedRecords type — zero
// LastConfirmedAt, preserving the IDs so old dedup markers keep working.
func TestAnnouncedRecordsUnmarshalOldStringFormat(t *testing.T) {
	raw := `{"last_announced_records": ["rec-1", "rec-2"]}`
	var c Conversation
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("unmarshal old format: %v", err)
	}
	if len(c.LastAnnouncedRecords) != 2 {
		t.Fatalf("LastAnnouncedRecords = %+v, want 2 entries", c.LastAnnouncedRecords)
	}
	if c.LastAnnouncedRecords[0].ID != "rec-1" || c.LastAnnouncedRecords[1].ID != "rec-2" {
		t.Fatalf("IDs = %+v, want rec-1, rec-2", c.LastAnnouncedRecords)
	}
	// Old format has no LastConfirmedAt — zero time means "never confirmed
	// since announce", so the record is treated as announced-once.
	if !c.LastAnnouncedRecords[0].LastConfirmedAt.IsZero() {
		t.Fatalf("old format LastConfirmedAt should be zero, got %v", c.LastAnnouncedRecords[0].LastConfirmedAt)
	}
}

// TestAnnouncedRecordsUnmarshalNewFormat proves the new structured format
// (last_announced_records: [{"id":"...","last_confirmed_at":"..."}]) round-trips
// through marshal/unmarshal intact.
func TestAnnouncedRecordsUnmarshalNewFormat(t *testing.T) {
	ts := time.Date(2025, 9, 15, 12, 0, 0, 0, time.UTC)
	orig := Conversation{
		LastAnnouncedRecords: AnnouncedRecords{
			{ID: "rec-1", LastConfirmedAt: ts},
			{ID: "rec-2", LastConfirmedAt: ts.Add(time.Hour)},
		},
	}
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Conversation
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(back.LastAnnouncedRecords) != 2 {
		t.Fatalf("round-trip: got %d entries, want 2", len(back.LastAnnouncedRecords))
	}
	if back.LastAnnouncedRecords[0].ID != "rec-1" {
		t.Fatalf("round-trip ID[0] = %q, want rec-1", back.LastAnnouncedRecords[0].ID)
	}
	if !back.LastAnnouncedRecords[0].LastConfirmedAt.Equal(ts) {
		t.Fatalf("round-trip LastConfirmedAt[0] = %v, want %v", back.LastAnnouncedRecords[0].LastConfirmedAt, ts)
	}
	if back.LastAnnouncedRecords[1].LastConfirmedAt.Equal(ts) {
		t.Fatalf("round-trip LastConfirmedAt[1] should differ from [0]")
	}
}

// TestAnnouncedRecordsUnmarshalEmpty proves empty/null arrays unmarshal to
// nil/empty without error.
func TestAnnouncedRecordsUnmarshalEmpty(t *testing.T) {
	cases := []string{
		`{}`,
		`{"last_announced_records": null}`,
		`{"last_announced_records": []}`,
	}
	for _, raw := range cases {
		var c Conversation
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			t.Fatalf("unmarshal %q: %v", raw, err)
		}
		if len(c.LastAnnouncedRecords) != 0 {
			t.Fatalf("unmarshal %q: got %d entries, want 0", raw, len(c.LastAnnouncedRecords))
		}
	}
}
