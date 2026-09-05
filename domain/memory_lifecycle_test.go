package domain

import (
	"testing"
	"time"
)

func TestMaxMemoryEntriesConstant(t *testing.T) {
	if MaxMemoryEntries != 500 {
		t.Fatalf("MaxMemoryEntries = %d, want 500", MaxMemoryEntries)
	}
}

func TestDefaultLifecycleConfig(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	if cfg.DecayInterval != time.Hour {
		t.Errorf("DecayInterval = %v, want 1h", cfg.DecayInterval)
	}
	if cfg.PruneInterval != 24*time.Hour {
		t.Errorf("PruneInterval = %v, want 24h", cfg.PruneInterval)
	}
	if cfg.DecayHalfLife != 168 {
		t.Errorf("DecayHalfLife = %v, want 168", cfg.DecayHalfLife)
	}
	if cfg.PruneThreshold != 0.05 {
		t.Errorf("PruneThreshold = %v, want 0.05", cfg.PruneThreshold)
	}
	if cfg.MaxMemory != MaxMemoryEntries {
		t.Errorf("MaxMemory = %d, want %d", cfg.MaxMemory, MaxMemoryEntries)
	}
}

func TestMemoryRecordStrengthFresh(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	now := time.Now()
	fresh := &MemoryRecord{Body: "prefer Go", Utility: 0.8, Status: MemoryStatusLearned, LastConfirmed: now, CreatedAt: now}
	if s := MemoryRecordStrength(fresh, cfg); s < 0.79 || s > 0.81 {
		t.Errorf("fresh strength = %v, want ~0.8", s)
	}
}

func TestMemoryRecordStrengthHalfLife(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	now := time.Now()
	old := &MemoryRecord{Body: "prefer Go", Utility: 0.8, Status: MemoryStatusLearned, LastConfirmed: now.Add(-168 * time.Hour), CreatedAt: now.Add(-168 * time.Hour)}
	if s := MemoryRecordStrength(old, cfg); s < 0.39 || s > 0.41 {
		t.Errorf("1-week-old strength = %v, want ~0.4", s)
	}
}

func TestMemoryRecordStrengthRetiredIsZero(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	retired := &MemoryRecord{Body: "old", Utility: 0.9, Status: MemoryStatusRetired, LastConfirmed: time.Now()}
	if s := MemoryRecordStrength(retired, cfg); s != 0 {
		t.Errorf("retired strength = %v, want 0", s)
	}
}

func TestMemoryRecordStrengthIgnoresUpdatedAtTouch(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	now := time.Now()
	old := now.Add(-168 * time.Hour)
	touched := &MemoryRecord{
		Body:          "prefer Go",
		Utility:       0.8,
		Status:        MemoryStatusLearned,
		CreatedAt:     old,
		UpdatedAt:     now,
		LastConfirmed: time.Time{},
	}
	s := MemoryRecordStrength(touched, cfg)
	if s < 0.39 || s > 0.41 {
		t.Errorf("touched-but-unconfirmed strength = %v, want ~0.4 from CreatedAt", s)
	}
}

func TestMemoryRecordStrengthClamped(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	cfg.DecayHalfLife = -1
	e := &MemoryRecord{Body: "test", Utility: 0.8, Status: MemoryStatusLearned, LastConfirmed: time.Now()}
	if s := MemoryRecordStrength(e, cfg); s < 0 || s > 1 {
		t.Errorf("strength = %v, want in [0, 1]", s)
	}
}
