package application

import (
	"context"
	"time"

	"nusashell/domain"
	clock "nusashell/pkg/time"
)

// MaxMemoryEntries is the hard capacity limit for memory records. The
// lifecycle manager retires weak records when this is exceeded.
const MaxMemoryEntries = domain.MaxMemoryEntries

// LifecycleConfig controls the decay and prune cycle for learning memory.
type LifecycleConfig = domain.LifecycleConfig

// LifecycleManager runs background decay and prune operations on
// MemoryRecords. Decay reduces a synthetic "strength" score for each record
// based on time since last access and access count. Prune retires records
// whose strength falls below the threshold (it does not delete them).
//
// The manager does not persist a strength field on MemoryRecord (the domain
// model stays simple). Instead, strength is computed on-the-fly from
// CreatedAt and the record type/evidence. This keeps the storage format
// stable.
type LifecycleManager struct {
	memory MemoryRecordStore
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
func NewLifecycleManager(memory MemoryRecordStore, skills SkillStore, cfg LifecycleConfig) *LifecycleManager {
	if cfg.DecayInterval == 0 {
		cfg = domain.DefaultLifecycleConfig()
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
func (m *LifecycleManager) runDecay() {
	// No-op: decay is computed on-the-fly during prune. Avoid logging every
	// tick to keep the log clean.
}

// runPrune retires memory records whose computed strength falls below
// the threshold. Strength is derived from age and initial signal weight.
// Frequently accessed records (newer CreatedAt) decay slower per the
// memex formula.
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
		var toRetire []string
		for _, e := range entries {
			if e == nil || e.Status == domain.MemoryStatusRetired {
				continue
			}
			if domain.MemoryRecordStrength(e, m.cfg) < m.cfg.PruneThreshold {
				toRetire = append(toRetire, e.ID)
			}
		}
		for _, id := range toRetire {
			rec, err := m.memory.Get(id)
			if err != nil || rec == nil {
				continue
			}
			rec.Retire(clock.NewTime().Time())
			rec.Source = "lifecycle"
			_ = m.memory.Save(rec)
		}
		if len(toRetire) > 0 {
			m.logf("info", "retired %d weak records (had %d, threshold=%.2f)", len(toRetire), len(entries), m.cfg.PruneThreshold)
		} else {
			m.logf("debug", "prune tick: %d records, none below threshold", len(entries))
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
		scored[i] = entry{id: e.ID, score: domain.MemoryRecordStrength(e, m.cfg)}
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
		rec, err := m.memory.Get(scored[i].id)
		if err != nil || rec == nil {
			continue
		}
		rec.Retire(clock.NewTime().Time())
		rec.Source = "lifecycle"
		_ = m.memory.Save(rec)
	}
	m.logf("info", "retired %d over-capacity records (had %d, target=%d)", toPrune, len(entries), target)
}

// PruneOnce runs a single prune cycle immediately. Used by tests and
// by the App on startup to clean up any over-capacity state.
func (m *LifecycleManager) PruneOnce() {
	m.runPrune()
}
