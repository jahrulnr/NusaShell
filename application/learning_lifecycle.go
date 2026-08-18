package application

import (
	"context"
	"math"
	"time"

	"nusashell/domain"
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

// LifecycleManager runs background decay and prune operations on the
// memory store. Decay reduces a synthetic "strength" score for each entry
// based on time since last access and access count. Prune removes entries
// whose strength falls below the threshold.
//
// The manager does not persist a strength field on MemoryEntry (the domain
// model stays simple). Instead, strength is computed on-the-fly from
// CreatedAt and the entry's Tags (which encode the signal weight). This
// keeps the storage format stable and avoids migration.
type LifecycleManager struct {
	memory MemoryStore
	skills SkillStore
	cfg    LifecycleConfig
	log    func(level, source, format string, args ...any)
}

// SetLogger wires a log function so the manager can report decay/prune
// activity. Optional — if not set, no logging.
func (m *LifecycleManager) SetLogger(fn func(level, source, format string, args ...any)) {
	m.log = fn
}

func (m *LifecycleManager) logf(level, format string, args ...any) {
	if m.log != nil {
		m.log(level, "learning", format, args...)
	}
}

// NewLifecycleManager creates a manager with the given config.
func NewLifecycleManager(memory MemoryStore, skills SkillStore, cfg LifecycleConfig) *LifecycleManager {
	if cfg.DecayInterval == 0 {
		cfg = DefaultLifecycleConfig()
	}
	return &LifecycleManager{memory: memory, skills: skills, cfg: cfg}
}

// Run starts the decay/prune loop. Blocks until ctx is cancelled.
func (m *LifecycleManager) Run(ctx context.Context) {
	decayTick := time.NewTicker(m.cfg.DecayInterval)
	pruneTick := time.NewTicker(m.cfg.PruneInterval)
	defer decayTick.Stop()
	defer pruneTick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-decayTick.C:
			m.runDecay()
		case <-pruneTick.C:
			m.runPrune()
		}
	}
}

// runDecay is a no-op for now — decay is computed on-the-fly during prune.
// This method exists as the hook for future persistent strength tracking.
// Logs a heartbeat so the user can see the decay cycle is alive.
func (m *LifecycleManager) runDecay() {
	entries := m.memory.List()
	m.logf("debug", "decay tick: %d entries (decay is computed on-the-fly during prune)", len(entries))
}

// runPrune removes memory entries whose computed strength falls below
// the threshold. Strength is derived from age and initial signal weight
// (encoded in tags). Frequently accessed entries (newer CreatedAt) decay
// slower per the memex formula.
func (m *LifecycleManager) runPrune() {
	entries := m.memory.List()
	if len(entries) == 0 {
		m.logf("debug", "prune tick: no entries")
		return
	}
	// If over capacity, prune the weakest entries down to the limit.
	target := m.cfg.MaxMemory
	if target <= 0 {
		target = MaxMemoryEntries
	}
	if len(entries) <= target {
		// Still prune entries below threshold. Snapshot IDs first to
		// avoid mutating the slice while iterating.
		var toDelete []string
		for _, e := range entries {
			if m.computeStrength(e) < m.cfg.PruneThreshold {
				toDelete = append(toDelete, e.ID)
			}
		}
		for _, id := range toDelete {
			_ = m.memory.Delete(id)
		}
		if len(toDelete) > 0 {
			m.logf("info", "pruned %d weak entries (had %d, threshold=%.2f)", len(toDelete), len(entries), m.cfg.PruneThreshold)
		} else {
			m.logf("debug", "prune tick: %d entries, none below threshold", len(entries))
		}
		return
	}
	// Over capacity — sort by strength and prune weakest.
	type entry struct {
		id    string
		score float64
	}
	scored := make([]entry, len(entries))
	for i, e := range entries {
		scored[i] = entry{id: e.ID, score: m.computeStrength(e)}
	}
	// Simple sort: weakest first.
	for i := 0; i < len(scored); i++ {
		for j := i + 1; j < len(scored); j++ {
			if scored[j].score < scored[i].score {
				scored[i], scored[j] = scored[j], scored[i]
			}
		}
	}
	toPrune := len(scored) - target
	for i := 0; i < toPrune; i++ {
		_ = m.memory.Delete(scored[i].id)
	}
	m.logf("info", "pruned %d over-capacity entries (had %d, target=%d)", toPrune, len(entries), target)
}

// computeStrength returns the decayed strength of a memory entry.
// Formula (from memex temporal.go):
//
//	lambda = ln(2) / halfLifeHours
//	stability = 1 + log1p(accessCount)  — but we don't track access
//	count yet, so stability = 1 (flat decay).
//	multiplier = baseStrength * exp(-lambda * hoursSinceCreated / stability)
//
// baseStrength is derived from the signal tag: fact=0.8, error=0.7,
// decision=0.6, preference=0.5, default=0.5.
func (m *LifecycleManager) computeStrength(e *domain.MemoryEntry) float64 {
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
	hoursSince := time.Since(e.CreatedAt).Hours()
	lambda := math.Ln2 / m.cfg.DecayHalfLife
	stability := 1.0
	strength := base * math.Exp(-lambda*hoursSince/stability)
	// Clamp to [0, 1] — decay never goes negative, and we don't exceed 1.
	if strength < 0 {
		strength = 0
	} else if strength > 1 {
		strength = 1
	}
	return strength
}

// PruneOnce runs a single prune cycle immediately. Used by tests and
// by the App on startup to clean up any over-capacity state.
func (m *LifecycleManager) PruneOnce() {
	m.runPrune()
}
