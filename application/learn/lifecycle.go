package learn

import (
	"context"
	"sort"
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
	memory       RecordStore
	skills       SkillCatalog
	cfg          LifecycleConfig
	log          func(level, source, format string, args ...any)
	housekeeping func()
}

// SetHousekeeping adds the auxiliary growth-retention pass to the daily prune.
func (m *LifecycleManager) SetHousekeeping(fn func()) {
	m.housekeeping = fn
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
func NewLifecycleManager(memory RecordStore, skills SkillCatalog, cfg LifecycleConfig) *LifecycleManager {
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
	if m.housekeeping != nil {
		defer m.housekeeping()
	}
	entries := m.memory.List()
	if len(entries) == 0 {
		m.logf("debug", "prune tick: no entries")
		return
	}

	// Retired and superseded rows are audit history, not live capacity. Keep
	// them briefly for inspection, then physically remove them so the JSONL
	// catalog and startup cost remain bounded over years of use.
	now := clock.NewTime().Time()
	cutoff := now.Add(-m.cfg.RetiredRetention)
	active := make([]*domain.MemoryRecord, 0, len(entries))
	var expiredIDs []string
	for _, record := range entries {
		if record == nil {
			continue
		}
		if !record.Retrievable() {
			if m.cfg.RetiredRetention > 0 && !record.UpdatedAt.IsZero() && record.UpdatedAt.Before(cutoff) {
				expiredIDs = append(expiredIDs, record.ID)
			}
			continue
		}
		active = append(active, record)
	}
	if len(expiredIDs) > 0 {
		if err := deleteStoreIDs(m.memory, expiredIDs); err != nil {
			m.logf("warn", "expired retired-record cleanup failed: %v", err)
		} else {
			m.logf("info", "deleted %d expired retired records", len(expiredIDs))
		}
	}
	if len(active) == 0 {
		return
	}

	// If over capacity, retire the weakest live entries down to the limit.
	target := m.cfg.MaxMemory
	if target <= 0 {
		target = MaxMemoryEntries
	}
	if len(active) <= target {
		var toRetire []string
		for _, record := range active {
			if domain.MemoryRecordStrength(record, m.cfg) < m.cfg.PruneThreshold {
				toRetire = append(toRetire, record.ID)
			}
		}
		for _, id := range toRetire {
			record, err := m.memory.Get(id)
			if err != nil || record == nil {
				continue
			}
			record.Retire(now)
			record.Source = "lifecycle"
			_ = m.memory.Save(record)
		}
		if len(toRetire) > 0 {
			m.logf("info", "retired %d weak records (had %d live, threshold=%.2f)", len(toRetire), len(active), m.cfg.PruneThreshold)
		} else {
			m.logf("debug", "prune tick: %d live records, none below threshold", len(active))
		}
		return
	}

	type scoredEntry struct {
		id    string
		score float64
	}
	scored := make([]scoredEntry, 0, len(active))
	for _, record := range active {
		scored = append(scored, scoredEntry{id: record.ID, score: domain.MemoryRecordStrength(record, m.cfg)})
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].score < scored[j].score })
	toPrune := len(scored) - target
	for i := 0; i < toPrune; i++ {
		record, err := m.memory.Get(scored[i].id)
		if err != nil || record == nil {
			continue
		}
		record.Retire(now)
		record.Source = "lifecycle"
		_ = m.memory.Save(record)
	}
	m.logf("info", "retired %d over-capacity records (had %d live, target=%d)", toPrune, len(active), target)
}

// PruneOnce runs a single prune cycle immediately. Used by tests and
// by the App on startup to clean up any over-capacity state.
func (m *LifecycleManager) PruneOnce() {
	m.runPrune()
}
