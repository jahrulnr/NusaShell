package domain

import (
	"math"
	"time"

	clock "nusashell/pkg/time"
)

// MaxMemoryEntries is the hard capacity limit for memory entries. The
// lifecycle manager prunes low-strength entries when this is exceeded.
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

// MemoryStrength returns the decayed strength of a memory entry.
// Formula (from memex temporal.go):
//
//	lambda = ln(2) / halfLifeHours
//	stability = 1 + log1p(accessCount)  — but we don't track access
//	count yet, so stability = 1 (flat decay).
//	multiplier = baseStrength * exp(-lambda * hoursSinceCreated / stability)
//
// baseStrength is derived from the signal tag: fact=0.8, error=0.7,
// decision=0.6, preference=0.5, default=0.5.
//
// The result is clamped to [0, 1] — decay never goes negative, and we
// don't exceed 1. A non-positive DecayHalfLife is treated as no decay
// (returns the base strength, clamped).
func MemoryStrength(e *MemoryEntry, cfg LifecycleConfig) float64 {
	if e == nil {
		return 0
	}
	base := 0.5
	for _, tag := range e.Tags {
		switch tag {
		case "fact":
			base = 0.8
		case "error", "fix":
			base = 0.7
		case "decision":
			base = 0.6
		case "preference":
			base = 0.5
		}
	}
	if cfg.DecayHalfLife <= 0 {
		return clamp01(base)
	}
	hoursSince := clock.NewTime().Since(e.CreatedAt).Hours()
	lambda := math.Ln2 / cfg.DecayHalfLife
	stability := 1.0
	strength := base * math.Exp(-lambda*hoursSince/stability)
	return clamp01(strength)
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
