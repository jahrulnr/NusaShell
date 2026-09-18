package agent

import (
	"strings"

	"nusashell/contracts"
	"nusashell/domain/turndiff"
)

func (a *Service) TrackTurnDiff(run *TurnRun, delta turndiff.Delta) {
	if a == nil || run == nil || run.TurnDiff == nil {
		return
	}
	run.turnDiffMu.Lock()
	had := run.TurnDiff.HasUnifiedDiff()
	run.TurnDiff.TrackDelta(delta)
	unified, ok := run.TurnDiff.UnifiedDiff()
	run.turnDiffMu.Unlock()
	if !had && !ok {
		return
	}
	text := ""
	if ok {
		text = unified
	}
	if a.Bus == nil {
		return
	}
	a.emitBus(contracts.EventTurnDiff, contracts.TurnDiffEvent{
		RunID: run.ID, ConversationID: run.ConversationID, UnifiedDiff: text,
	})
}

func (a *Service) EmitFinalTurnDiff(run *TurnRun) {
	if a == nil || a.Bus == nil || run == nil {
		return
	}
	run.turnDiffMu.Lock()
	var unified string
	ok := false
	if run.TurnDiff != nil {
		unified, ok = run.TurnDiff.UnifiedDiff()
	}
	run.turnDiffMu.Unlock()
	if !ok || unified == "" {
		return
	}
	a.emitBus(contracts.EventTurnDiff, contracts.TurnDiffEvent{
		RunID: run.ID, ConversationID: run.ConversationID, UnifiedDiff: unified,
	})
}

func (r *TurnRun) InitTurnDiff() {
	if r == nil || r.TurnDiff != nil {
		return
	}
	r.TurnDiff = turndiff.New(turndiff.WithDisplayRoot(r.Workspace))
}

// PersistTurnDiff writes the net unified diff of this run's committed file_*
// mutations to the conversation's operation/ directory, so the patch history
// survives restarts and can be reviewed or re-applied later. Turns without
// mutations leave no file behind. Best-effort: a write failure is logged and
// never fails the turn, which has already persisted its transcript.
func (a *Service) PersistTurnDiff(run *TurnRun) {
	if a == nil || run == nil || a.deps.TurnPatches == nil {
		return
	}
	run.turnDiffMu.Lock()
	var unified string
	ok := false
	if run.TurnDiff != nil {
		unified, ok = run.TurnDiff.UnifiedDiff()
	}
	run.turnDiffMu.Unlock()
	if !ok || strings.TrimSpace(unified) == "" {
		return
	}
	if err := a.deps.TurnPatches.Save(run.ConversationID, run.ID, unified); err != nil {
		a.log("warn", "agent", "persist turn patch %s: %v", run.ID, err)
	}
}

// WithTurnDiff runs fn while holding the turn-diff mutex so tests can inspect
// TurnDiff without racing the turn loop.
func (r *TurnRun) WithTurnDiff(fn func()) {
	if r == nil {
		return
	}
	r.turnDiffMu.Lock()
	defer r.turnDiffMu.Unlock()
	fn()
}
