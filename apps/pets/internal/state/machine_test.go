package state

import (
	"sync"
	"testing"
)

func TestValidStates(t *testing.T) {
	t.Parallel()
	valid := []PetState{StateIdle, StateThinking, StateReasoning, StateDone, StateError, StateWaiting}
	for _, s := range valid {
		if !Valid(s) {
			t.Fatalf("%q should be valid", s)
		}
	}
	if Valid("bogus") {
		t.Fatal("bogus should be invalid")
	}
}

func TestParseState(t *testing.T) {
	t.Parallel()
	if s, err := ParseState("thinking"); err != nil || s != StateThinking {
		t.Fatalf("ParseState thinking: %v %v", s, err)
	}
	if _, err := ParseState("nope"); err == nil {
		t.Fatal("expected error for unknown state")
	}
}

func TestNewMachineDefaults(t *testing.T) {
	t.Parallel()
	m := NewMachine("", []PetState{StateIdle, StateDone}, nil)
	if m.Current() != StateIdle {
		t.Fatalf("default start = %q, want idle", m.Current())
	}
	// no available -> idle forced
	m2 := NewMachine(StateError, nil, nil)
	if m2.Current() != StateIdle {
		t.Fatalf("empty available start = %q, want idle", m2.Current())
	}
	// start unavailable -> first available
	m3 := NewMachine(StateError, []PetState{StateDone}, nil)
	if m3.Current() != StateDone {
		t.Fatalf("unavailable start = %q, want done", m3.Current())
	}
	// Multiple fallback states should not depend on Go map iteration order.
	m4 := NewMachine(StateThinking, []PetState{StateError, StateDone}, nil)
	if m4.Current() != StateDone {
		t.Fatalf("stable fallback start = %q, want done", m4.Current())
	}
}

func TestTransitionAnyToAny(t *testing.T) {
	t.Parallel()
	m := NewMachine(StateIdle, []PetState{StateIdle, StateThinking, StateReasoning, StateDone, StateError}, nil)
	cases := []struct {
		ev   Event
		want PetState
		chg  bool
	}{
		{Event{State: StateThinking, Message: "step 1"}, StateThinking, true},
		{Event{State: StateThinking, Message: "step 2"}, StateThinking, false}, // same state
		{Event{State: StateReasoning}, StateReasoning, true},
		{Event{State: StateDone}, StateDone, true},
		{Event{State: StateError}, StateError, true},
		{Event{State: StateIdle}, StateIdle, true},
	}
	for _, c := range cases {
		got, chg := m.Transition(c.ev)
		if got != c.want || chg != c.chg {
			t.Fatalf("Transition(%+v) = %q chg=%v, want %q chg=%v", c.ev, got, chg, c.want, c.chg)
		}
	}
}

func TestTransitionUnknownRejected(t *testing.T) {
	t.Parallel()
	m := NewMachine(StateIdle, []PetState{StateIdle, StateDone}, nil)
	got, chg := m.Transition(Event{State: "bogus"})
	if got != StateIdle || chg {
		t.Fatalf("unknown state: got %q chg=%v, want idle/false", got, chg)
	}
}

func TestTransitionUnavailableClampsToIdle(t *testing.T) {
	t.Parallel()
	// only idle + done available; thinking should clamp to idle
	m := NewMachine(StateDone, []PetState{StateIdle, StateDone}, nil)
	got, chg := m.Transition(Event{State: StateThinking})
	if got != StateIdle || !chg {
		t.Fatalf("unavailable thinking: got %q chg=%v, want idle/true", got, chg)
	}
}

func TestTransitionUnavailableNoIdleNoChange(t *testing.T) {
	t.Parallel()
	// only done available; thinking has no idle fallback -> no change
	m := NewMachine(StateDone, []PetState{StateDone}, nil)
	got, chg := m.Transition(Event{State: StateThinking})
	if got != StateDone || chg {
		t.Fatalf("no idle fallback: got %q chg=%v, want done/false", got, chg)
	}
}

func TestListenerFiresOnChangeOnly(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var calls []struct {
		old, new PetState
		msg      string
	}
	l := ListenerFunc(func(old, new PetState, msg string) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, struct {
			old, new PetState
			msg      string
		}{old, new, msg})
	})
	m := NewMachine(StateIdle, []PetState{StateIdle, StateThinking, StateDone}, l)
	m.Transition(Event{State: StateThinking, Message: "hi"})
	m.Transition(Event{State: StateThinking, Message: "again"}) // no change
	m.Transition(Event{State: StateDone, Message: ""})

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("listener calls = %d, want 2: %+v", len(calls), calls)
	}
	if calls[0].old != StateIdle || calls[0].new != StateThinking || calls[0].msg != "hi" {
		t.Fatalf("call[0] = %+v", calls[0])
	}
	if calls[1].new != StateDone {
		t.Fatalf("call[1] = %+v", calls[1])
	}
}

func TestConcurrentTransitions(t *testing.T) {
	t.Parallel()
	m := NewMachine(StateIdle, []PetState{StateIdle, StateThinking, StateDone, StateError}, nil)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m.Transition(Event{State: StateThinking})
			_ = m.Current()
			m.Transition(Event{State: StateDone})
		}(i)
	}
	wg.Wait()
	// just ensure no panic/race; final state is one of the valid ones
	if !Valid(m.Current()) {
		t.Fatalf("final state %q invalid", m.Current())
	}
}

func TestConcurrentRejectedTransitionsAreRaceSafe(t *testing.T) {
	t.Parallel()
	m := NewMachine(StateIdle, []PetState{StateIdle, StateThinking}, nil)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				m.Transition(Event{State: "unknown"})
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				m.Transition(Event{State: StateThinking})
				m.Transition(Event{State: StateIdle})
			}
		}()
	}
	wg.Wait()
}
