package application

import (
	"testing"
	"time"

	"nusashell/contracts"
)

func TestBusPreservesTurnTerminalEventsBehindDeltaBurst(t *testing.T) {
	bus := NewBus()
	_, events, unsubscribe := bus.Subscribe()
	defer unsubscribe()

	for i := 0; i < busNormalQueueLimit*2; i++ {
		bus.Emit(contracts.EventMessageDelta, map[string]any{"text": "delta"})
	}
	bus.Emit(contracts.EventTurnDone, contracts.TurnDoneEvent{RunID: "run_1", ConversationID: "conv_1"})
	bus.Emit(contracts.EventTurnError, contracts.TurnErrorEvent{RunID: "run_2", ConversationID: "conv_2", Message: "failed"})

	gotDone := false
	gotError := false
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for !gotDone || !gotError {
		select {
		case event := <-events:
			gotDone = gotDone || event.Type == contracts.EventTurnDone
			gotError = gotError || event.Type == contracts.EventTurnError
		case <-deadline.C:
			t.Fatalf("terminal events lost: done=%v error=%v", gotDone, gotError)
		}
	}
}
