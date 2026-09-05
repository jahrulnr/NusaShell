package domain

import (
	"math"
	"time"

	clock "nusashell/pkg/time"
)

// MaxMemoryEntries is the hard capacity limit for live (non-retired)
// memory records. The lifecycle manager retires low-utility records when
// this is exceeded.
const MaxMemoryEntries = 500

// LifecycleConfig controls the decay and prune cycle for learning memory.
type LifecycleConfig struct {
	DecayInterval  time.Duration // default 1h
	PruneInterval  time.Duration // default 24h
	DecayHalfLife  float64       // hours, default 168 (1 week)
	PruneThreshold float64       // default 0.05
	MaxMemory      int           // default 500 — hard capacity limit
}

// DefaultLifecycleConfig returns sensible defaults for a personal shell.
func DefaultLifecycleConfig() LifecycleConfig {
	return LifecycleConfig{
		DecayInterval:  1 * time.Hour,
		PruneInterval:  24 * time.Hour,
		DecayHalfLife:  168, // 1 week
		PruneThreshold: 0.05,
		MaxMemory:      MaxMemoryEntries,
	}
}

// MemoryRecordStrength is utility decayed by time since last confirmation.
// UpdatedAt is a row-write stamp and must not reset decay; only
// LastConfirmed (else CreatedAt) is the anchor.
func MemoryRecordStrength(m *MemoryRecord, cfg LifecycleConfig) float64 {
	if m == nil || !m.Retrievable() {
		return 0
	}
	base := m.Utility
	if base <= 0 {
		base = 0.5
	}
	if cfg.DecayHalfLife <= 0 {
		return clamp01(base)
	}
	anchor := m.LastConfirmed
	if anchor.IsZero() {
		anchor = m.CreatedAt
	}
	hoursSince := clock.NewTime().Since(anchor).Hours()
	lambda := math.Ln2 / cfg.DecayHalfLife
	stability := m.Stability
	if stability <= 0 {
		stability = 1
	}
	return clamp01(base * math.Exp(-lambda*hoursSince/stability))
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}
