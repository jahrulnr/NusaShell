package application

import "nusashell/contracts"

// emitCompactionStarted publishes the room-scoped lifecycle event before a
// compaction call begins. Keeping this in one helper makes all compaction
// entry points (initial, proactive, and emergency) behave identically.
func (a *App) emitCompactionStarted(run *TurnRun, conversationID string) {
	if a == nil || a.Bus == nil || run == nil || conversationID == "" {
		return
	}
	a.Bus.Emit(contracts.EventCompacting, contracts.CompactingEvent{
		RunID:          run.ID,
		ConversationID: conversationID,
	})
}
