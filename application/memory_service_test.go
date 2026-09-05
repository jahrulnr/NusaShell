package application

import (
	"testing"
	"time"

	"nusashell/domain"
)

func TestStrengthenDoesNotResetUpdatedAt(t *testing.T) {
	created := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	rec := &domain.MemoryRecord{
		ID:            "mem_s",
		Type:          domain.MemoryTypePreference,
		Body:          "use pnpm in this repo",
		Status:        domain.MemoryStatusLearned,
		Scope:         domain.MemoryScope{Level: domain.MemoryScopeUser},
		CreatedAt:     created,
		UpdatedAt:     created,
		LastConfirmed: created,
		EvidenceCount: 1,
		Utility:       0.5,
	}
	svc := NewMemoryService(&fakeMemoryRecordStore{items: []*domain.MemoryRecord{rec}}, nil)
	if err := svc.Apply(&domain.LearningOperation{
		Kind:     domain.OpMemoryStrengthen,
		Payload:  map[string]any{"id": "mem_s"},
		Evidence: []string{"exp_2"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.records.Get("mem_s")
	if err != nil {
		t.Fatal(err)
	}
	if !got.UpdatedAt.Equal(created) {
		t.Fatalf("UpdatedAt reset from %s to %s", created, got.UpdatedAt)
	}
	if !got.LastConfirmed.After(created) {
		t.Fatalf("LastConfirmed=%s, want later than CreatedAt", got.LastConfirmed)
	}
	if got.EvidenceCount < 2 {
		t.Fatalf("EvidenceCount=%d", got.EvidenceCount)
	}
}
