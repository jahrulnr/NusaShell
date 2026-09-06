package memory

import (
	"fmt"
	"testing"
	"time"

	"nusashell/domain"
)

type fakeRecordStore struct {
	items []*domain.MemoryRecord
}

func (f *fakeRecordStore) List() []*domain.MemoryRecord {
	if f == nil {
		return nil
	}
	return f.items
}

func (f *fakeRecordStore) Get(id string) (*domain.MemoryRecord, error) {
	for _, m := range f.items {
		if m != nil && m.ID == id {
			return m, nil
		}
	}
	return nil, fmt.Errorf("memory %s not found", id)
}

func (f *fakeRecordStore) Save(e *domain.MemoryRecord) error {
	if e == nil {
		return fmt.Errorf("nil memory")
	}
	for i, existing := range f.items {
		if existing.ID == e.ID {
			f.items[i] = e
			return nil
		}
	}
	f.items = append(f.items, e)
	return nil
}

func (f *fakeRecordStore) Delete(id string) error {
	for i, e := range f.items {
		if e.ID == id {
			f.items = append(f.items[:i], f.items[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("memory %s not found", id)
}

func TestContradictUsesTargetIDWhenPayloadIDMissing(t *testing.T) {
	rec := &domain.MemoryRecord{
		ID:     "mem_old",
		Type:   domain.MemoryTypeFact,
		Body:   "file_patch can roll back a phantom hunk",
		Status: domain.MemoryStatusLearned,
		Scope:  domain.MemoryScope{Level: domain.MemoryScopeUser},
	}
	store := &fakeRecordStore{items: []*domain.MemoryRecord{rec}}
	svc := NewMemoryService(store, nil)
	op := &domain.LearningOperation{
		Kind:     domain.OpMemoryContradict,
		TargetID: "mem_old",
		Reason:   "learner supersede",
	}
	if err := svc.Apply(op); err != nil {
		t.Fatalf("contradict via TargetID: %v", err)
	}
	got, err := store.Get("mem_old")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.MemoryStatusSuperseded {
		t.Fatalf("status=%s, want superseded", got.Status)
	}
	if op.Status != domain.LearningOpAccepted {
		t.Fatalf("op status=%s", op.Status)
	}
}

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
	svc := NewMemoryService(&fakeRecordStore{items: []*domain.MemoryRecord{rec}}, nil)
	if err := svc.Apply(&domain.LearningOperation{
		Kind:     domain.OpMemoryStrengthen,
		Payload:  map[string]any{"id": "mem_s"},
		Evidence: []string{"exp_2"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.deps.Records.Get("mem_s")
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
