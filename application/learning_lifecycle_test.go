package application

import (
	"testing"
	"time"

	"nusashell/domain"
)

func TestComputeStrengthDecays(t *testing.T) {
	cfg := domain.DefaultLifecycleConfig()
	// Fresh entry (0 hours old) → full base strength.
	fresh := &domain.MemoryEntry{Content: "test", Tags: []string{"fact"}, CreatedAt: time.Now()}
	if s := domain.MemoryStrength(fresh, cfg); s < 0.79 || s > 0.81 {
		t.Errorf("fresh fact strength = %v, want ~0.8", s)
	}
	// Old entry (1 week = 168h = 1 half-life) → half strength.
	old := &domain.MemoryEntry{Content: "test", Tags: []string{"fact"}, CreatedAt: time.Now().Add(-168 * time.Hour)}
	if s := domain.MemoryStrength(old, cfg); s < 0.39 || s > 0.41 {
		t.Errorf("1-week-old fact strength = %v, want ~0.4", s)
	}
	// Very old entry (4 weeks) → near zero.
	ancient := &domain.MemoryEntry{Content: "test", Tags: []string{"fact"}, CreatedAt: time.Now().Add(-672 * time.Hour)}
	if s := domain.MemoryStrength(ancient, cfg); s > 0.05 {
		t.Errorf("4-week-old fact strength = %v, want < 0.05", s)
	}
}

func TestComputeStrengthByTag(t *testing.T) {
	cfg := domain.DefaultLifecycleConfig()
	now := time.Now()
	tests := []struct {
		tags []string
		want float64
	}{
		{[]string{"fact"}, 0.8},
		{[]string{"error", "fix"}, 0.7},
		{[]string{"decision"}, 0.6},
		{[]string{"preference"}, 0.5},
		{[]string{}, 0.5},
	}
	for _, tc := range tests {
		e := &domain.MemoryEntry{Content: "test", Tags: tc.tags, CreatedAt: now}
		got := domain.MemoryStrength(e, cfg)
		if got < tc.want-0.01 || got > tc.want+0.01 {
			t.Errorf("tags=%v strength = %v, want ~%v", tc.tags, got, tc.want)
		}
	}
}

func TestPruneOnceRemovesWeakEntries(t *testing.T) {
	mem := &fakeMemoryStore{entries: []*domain.MemoryEntry{
		{ID: "old1", Content: "old fact", Tags: []string{"fact"}, CreatedAt: time.Now().Add(-700 * time.Hour)},
		{ID: "old2", Content: "old pref", Tags: []string{"preference"}, CreatedAt: time.Now().Add(-700 * time.Hour)},
		{ID: "new1", Content: "new fact", Tags: []string{"fact"}, CreatedAt: time.Now()},
	}}
	m := NewLifecycleManager(mem, &fakeSkillStore{}, domain.DefaultLifecycleConfig())
	m.PruneOnce()
	remaining := mem.List()
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining (new1), got %d: %+v", len(remaining), remaining)
	}
	if remaining[0].ID != "new1" {
		t.Errorf("survivor = %q, want new1", remaining[0].ID)
	}
}

func TestPruneOnceRespectsCapacity(t *testing.T) {
	// Create 10 entries, capacity 5.
	entries := make([]*domain.MemoryEntry, 10)
	for i := range entries {
		entries[i] = &domain.MemoryEntry{
			ID:        "mem_" + string(rune('a'+i)),
			Content:   "entry " + string(rune('a'+i)),
			Tags:      []string{"fact"},
			CreatedAt: time.Now().Add(-time.Duration(i) * time.Hour), // staggered age
		}
	}
	mem := &fakeMemoryStore{entries: entries}
	cfg := domain.DefaultLifecycleConfig()
	cfg.MaxMemory = 5
	m := NewLifecycleManager(mem, &fakeSkillStore{}, cfg)
	m.PruneOnce()
	remaining := mem.List()
	if len(remaining) > 5 {
		t.Fatalf("expected at most 5 remaining, got %d", len(remaining))
	}
	// The newest entries (a, b, c, d, e) should survive.
	survivors := map[string]bool{}
	for _, e := range remaining {
		survivors[e.ID] = true
	}
	if !survivors["mem_a"] {
		t.Error("newest entry mem_a should survive capacity prune")
	}
}
