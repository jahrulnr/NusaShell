package application

import (
	"io"
	"log/slog"
	"nusashell/contracts"
	"nusashell/domain"
	"sync/atomic"
	"testing"
	"time"
)

// --- from app_runtime_test.go ---

func TestCloseWaitsForLearningGoroutines(t *testing.T) {
	app := &App{}
	started := make(chan struct{})
	release := make(chan struct{})
	var ran atomic.Bool
	app.goSafe("learning", func() {
		close(started)
		<-release
		ran.Store(true)
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("learning goroutine did not start")
	}

	closed := make(chan struct{})
	go func() {
		app.Close()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("Close returned before the learning goroutine finished")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not return after the learning goroutine finished")
	}
	if !ran.Load() {
		t.Fatal("learning goroutine did not run to completion")
	}
}

func TestCloseDoesNotWaitForUntrackedGoSafe(t *testing.T) {
	app := &App{}
	release := make(chan struct{})
	app.goSafe("agent", func() { <-release })
	closed := make(chan struct{})
	go func() {
		app.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close blocked on a non-learning goroutine")
	}
	close(release)
}

// --- from bus_test.go ---

type panicLogStore struct{}

func (*panicLogStore) Append(*domain.LogEntry) {
	panic("log store unavailable")
}

func (*panicLogStore) List(string, int) []*domain.LogEntry { return nil }
func (*panicLogStore) Clear()                              {}

func TestGoSafeSurvivesPanicInRecoveryLogging(t *testing.T) {
	app := &App{
		Logs:   &panicLogStore{},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	done := make(chan struct{})
	app.goSafe("test", func() {
		defer close(done)
		panic("worker failed")
	})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("goSafe worker did not recover")
	}
}

// --- from bus_test.go ---

func TestBusPreservesTurnTerminalEventsBehindDeltaBurst(t *testing.T) {
	bus := NewBus()
	_, events, unsubscribe := bus.Subscribe()
	defer unsubscribe()

	for i := 0; i < busNormalQueueLimit*2; i++ {
		bus.Emit(contracts.EventContextEstimate, map[string]any{"estimated_tokens": 1})
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

func TestBusPreservesSteerAppliedBeforeNextRoundBehindDeltaBurst(t *testing.T) {
	bus := NewBus()
	_, events, unsubscribe := bus.Subscribe()
	defer unsubscribe()

	for i := 0; i < busNormalQueueLimit*2; i++ {
		bus.Emit(contracts.EventContextEstimate, map[string]any{"estimated_tokens": 1})
	}
	bus.Emit(contracts.EventSteerApplied, contracts.SteerEvent{
		ConversationID: "conv_1", SteerID: "steer_1", Text: "fix it", Status: "applied",
	})
	bus.Emit(contracts.EventTurnStarted, contracts.TurnStartedEvent{
		RunID: "run_1", ConversationID: "conv_1", MessageID: "msg_2", Round: 2,
	})

	var lifecycle []string
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for len(lifecycle) < 2 {
		select {
		case event := <-events:
			switch event.Type {
			case contracts.EventSteerApplied, contracts.EventTurnStarted:
				lifecycle = append(lifecycle, event.Type)
			}
		case <-deadline.C:
			t.Fatalf("ordered steer lifecycle events lost: %v", lifecycle)
		}
	}

	if lifecycle[0] != contracts.EventSteerApplied || lifecycle[1] != contracts.EventTurnStarted {
		t.Fatalf("steer lifecycle order = %v, want applied then turn.started", lifecycle)
	}
}

func TestBusPreservesBlockingLifecycleEventsBehindDeltaBurst(t *testing.T) {
	eventTypes := []string{
		contracts.EventToolCompleted,
		contracts.EventSteerQueued,
		contracts.EventSteerCancelled,
		contracts.EventAskPending,
		contracts.EventAskAnswered,
		contracts.EventAskCancelled,
		contracts.EventCompacting,
		contracts.EventAcpRunDone,
		contracts.EventAcpPermissionRequested,
		contracts.EventAcpPermissionDecided,
	}
	for _, eventType := range eventTypes {
		t.Run(eventType, func(t *testing.T) {
			bus := NewBus()
			_, events, unsubscribe := bus.Subscribe()
			defer unsubscribe()

			for i := 0; i < busNormalQueueLimit*2; i++ {
				bus.Emit(contracts.EventContextEstimate, map[string]any{"estimated_tokens": 1})
			}
			bus.Emit(eventType, map[string]any{"id": "lifecycle"})

			deadline := time.NewTimer(2 * time.Second)
			defer deadline.Stop()
			for {
				select {
				case event := <-events:
					if event.Type == eventType {
						return
					}
				case <-deadline.C:
					t.Fatalf("%s was lost behind delta burst", eventType)
				}
			}
		})
	}
}

// --- from auto_update_test.go ---

func TestAutoUpdateDefaultOff(t *testing.T) {
	var p domain.Plugin
	if p.Manifest.AutoUpdate {
		t.Fatal("AutoUpdate must default to false (OFF)")
	}
}
