package domain

import (
	"math"
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

func TestMemoryStrengthFresh(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	fresh := &MemoryEntry{Content: "test", Tags: []string{"fact"}, CreatedAt: time.Now()}
	if s := MemoryStrength(fresh, cfg); s < 0.79 || s > 0.81 {
		t.Errorf("fresh fact strength = %v, want ~0.8", s)
	}
}

func TestMemoryStrengthHalfLife(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	old := &MemoryEntry{Content: "test", Tags: []string{"fact"}, CreatedAt: time.Now().Add(-168 * time.Hour)}
	if s := MemoryStrength(old, cfg); s < 0.39 || s > 0.41 {
		t.Errorf("1-week-old fact strength = %v, want ~0.4", s)
	}
}

func TestMemoryStrengthNearZeroAfterFourWeeks(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	ancient := &MemoryEntry{Content: "test", Tags: []string{"fact"}, CreatedAt: time.Now().Add(-672 * time.Hour)}
	if s := MemoryStrength(ancient, cfg); s > 0.05 {
		t.Errorf("4-week-old fact strength = %v, want < 0.05", s)
	}
}

func TestMemoryStrengthByTag(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	now := time.Now()
	cases := []struct {
		tags []string
		want float64
	}{
		{[]string{"fact"}, 0.8},
		{[]string{"error", "fix"}, 0.7},
		{[]string{"decision"}, 0.6},
		{[]string{"preference"}, 0.5},
		{nil, 0.5},
	}
	for _, tc := range cases {
		e := &MemoryEntry{Content: "test", Tags: tc.tags, CreatedAt: now}
		got := MemoryStrength(e, cfg)
		if math.Abs(got-tc.want) > 0.01 {
			t.Errorf("tags=%v strength = %v, want ~%v", tc.tags, got, tc.want)
		}
	}
}

func TestMemoryStrengthClamped(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	// Negative half-life would produce unbounded values; guard against that
	// by ensuring the result is always in [0, 1].
	cfg.DecayHalfLife = -1
	e := &MemoryEntry{Content: "test", Tags: []string{"fact"}, CreatedAt: time.Now()}
	if s := MemoryStrength(e, cfg); s < 0 || s > 1 {
		t.Errorf("strength = %v, want in [0, 1]", s)
	}
}
