package application

import (
	"nusashell/contracts"
	"nusashell/domain/turndiff"
)

func (a *App) trackTurnDiff(run *TurnRun, delta turndiff.Delta) {
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
	a.Bus.Emit(contracts.EventTurnDiff, contracts.TurnDiffEvent{
		RunID: run.ID, ConversationID: run.ConversationID, UnifiedDiff: text,
	})
}

func (a *App) emitFinalTurnDiff(run *TurnRun) {
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
	a.Bus.Emit(contracts.EventTurnDiff, contracts.TurnDiffEvent{
		RunID: run.ID, ConversationID: run.ConversationID, UnifiedDiff: unified,
	})
}

func (r *TurnRun) initTurnDiff() {
	if r == nil || r.TurnDiff != nil {
		return
	}
	r.TurnDiff = turndiff.New(turndiff.WithDisplayRoot(r.Workspace))
}
