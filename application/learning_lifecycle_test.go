package application

import (
	"testing"
	"time"

	"nusashell/domain"
)

func TestMemoryRecordStrengthDecays(t *testing.T) {
	cfg := domain.DefaultLifecycleConfig()
	now := time.Now()
	fresh := &domain.MemoryRecord{
		Body: "test", Status: domain.MemoryStatusLearned, Utility: 0.8, LastConfirmed: now,
	}
	if s := domain.MemoryRecordStrength(fresh, cfg); s < 0.79 || s > 0.81 {
		t.Errorf("fresh strength = %v, want ~0.8", s)
	}
	old := &domain.MemoryRecord{
		Body: "test", Status: domain.MemoryStatusLearned, Utility: 0.8, LastConfirmed: now.Add(-168 * time.Hour),
	}
	if s := domain.MemoryRecordStrength(old, cfg); s < 0.39 || s > 0.41 {
		t.Errorf("1-week-old strength = %v, want ~0.4", s)
	}
	ancient := &domain.MemoryRecord{
		Body: "test", Status: domain.MemoryStatusLearned, Utility: 0.8, LastConfirmed: now.Add(-672 * time.Hour),
	}
	if s := domain.MemoryRecordStrength(ancient, cfg); s > 0.05 {
		t.Errorf("4-week-old strength = %v, want < 0.05", s)
	}
}

func TestMemoryRecordStrengthRetiredIsZero(t *testing.T) {
	cfg := domain.DefaultLifecycleConfig()
	retired := &domain.MemoryRecord{
		Body: "test", Status: domain.MemoryStatusRetired, Utility: 0.9, LastConfirmed: time.Now(),
	}
	if s := domain.MemoryRecordStrength(retired, cfg); s != 0 {
		t.Errorf("retired strength = %v, want 0", s)
	}
}

func TestPruneOnceRetiresWeakRecords(t *testing.T) {
	now := time.Now()
	mem := &fakeMemoryRecordStore{items: []*domain.MemoryRecord{
		{ID: "old1", Body: "old fact", Status: domain.MemoryStatusLearned, Utility: 0.8, LastConfirmed: now.Add(-700 * time.Hour)},
		{ID: "old2", Body: "old pref", Status: domain.MemoryStatusLearned, Utility: 0.5, LastConfirmed: now.Add(-700 * time.Hour)},
		{ID: "new1", Body: "new fact", Status: domain.MemoryStatusLearned, Utility: 0.8, LastConfirmed: now},
	}}
	m := NewLifecycleManager(mem, &fakeSkillStore{}, domain.DefaultLifecycleConfig())
	m.PruneOnce()
	live := 0
	var survivor string
	for _, rec := range mem.List() {
		if rec.Retrievable() {
			live++
			survivor = rec.ID
		}
	}
	if live != 1 || survivor != "new1" {
		t.Fatalf("live=%d survivor=%q, want 1/new1: %+v", live, survivor, mem.List())
	}
}

func TestPruneOnceRespectsCapacity(t *testing.T) {
	now := time.Now()
	entries := make([]*domain.MemoryRecord, 10)
	for i := range entries {
		entries[i] = &domain.MemoryRecord{
			ID:            "mem_" + string(rune('a'+i)),
			Body:          "entry " + string(rune('a'+i)),
			Status:        domain.MemoryStatusLearned,
			Utility:       0.8,
			LastConfirmed: now.Add(-time.Duration(i) * time.Hour),
		}
	}
	mem := &fakeMemoryRecordStore{items: entries}
	cfg := domain.DefaultLifecycleConfig()
	cfg.MaxMemory = 5
	m := NewLifecycleManager(mem, &fakeSkillStore{}, cfg)
	m.PruneOnce()
	live := 0
	survivors := map[string]bool{}
	for _, e := range mem.List() {
		if e.Retrievable() {
			live++
			survivors[e.ID] = true
		}
	}
	if live > 5 {
		t.Fatalf("expected at most 5 live records, got %d", live)
	}
	if !survivors["mem_a"] {
		t.Error("newest entry mem_a should survive capacity prune")
	}
}
