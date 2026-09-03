// Package state implements the pet state machine. States are event-driven
// (any -> any) and each maps to an animation in the loaded atlas. The machine
// is pure logic with no SDL2 or network dependency, so it is fully unit-testable.
package state

import (
	"fmt"
	"sort"
	"sync"
)

// PetState is a named pet activity state.
type PetState string

const (
	StateIdle      PetState = "idle"
	StateThinking  PetState = "thinking"
	StateReasoning PetState = "reasoning"
	StateDone      PetState = "done"
	StateError     PetState = "error"
	StateWaiting   PetState = "waiting"
)

// Event is a state-change message delivered over WebSocket.
type Event struct {
	State   PetState `json:"state"`
	Message string   `json:"message"`
	Title   string   `json:"-"` // local presentation caption, not a wire field
}

// Valid reports whether s is a known pet state.
func Valid(s PetState) bool {
	switch s {
	case StateIdle, StateThinking, StateReasoning, StateDone, StateError, StateWaiting:
		return true
	}
	return false
}

// Listener is notified on every state transition (old -> new) with the event
// message. Implementations render the new atlas animation and/or show a bubble.
type Listener interface {
	OnTransition(old, new PetState, message string)
}

// ListenerFunc adapts a function to Listener.
type ListenerFunc func(old, new PetState, message string)

func (f ListenerFunc) OnTransition(old, new PetState, message string) { f(old, new, message) }

// Machine is a thread-safe pet state machine. Transitions are event-driven;
// any state may transition to any other valid state. Unknown states are
// rejected and leave the previous state unchanged.
type Machine struct {
	mu        sync.RWMutex
	current   PetState
	listener  Listener
	available map[PetState]bool // states backed by an animation in the loaded pack
}

// NewMachine creates a machine starting in startState. available lists the
// states that have a backing animation; transitions to unavailable states are
// clamped to StateIdle so the pet never shows a blank frame.
func NewMachine(startState PetState, available []PetState, listener Listener) *Machine {
	avail := make(map[PetState]bool, len(available))
	for _, s := range available {
		avail[s] = true
	}
	if len(avail) == 0 {
		avail[StateIdle] = true
	}
	if !Valid(startState) || !avail[startState] {
		startState = StateIdle
	}
	if !avail[startState] {
		fallbacks := make([]string, 0, len(avail))
		for s := range avail {
			fallbacks = append(fallbacks, string(s))
		}
		sort.Strings(fallbacks)
		startState = PetState(fallbacks[0])
	}
	return &Machine{
		current:   startState,
		listener:  listener,
		available: avail,
	}
}

// Current returns the current state.
func (m *Machine) Current() PetState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}

// Transition applies an event. It returns the resulting state and whether the
// transition actually changed state. Unknown states are rejected (no change).
// Valid but unavailable states clamp to StateIdle if idle is available,
// otherwise the state is left unchanged.
func (m *Machine) Transition(ev Event) (PetState, bool) {
	m.mu.Lock()
	if !Valid(ev.State) {
		current := m.current
		m.mu.Unlock()
		return current, false
	}
	next := ev.State
	if !m.available[next] {
		if m.available[StateIdle] {
			next = StateIdle
		} else {
			current := m.current
			m.mu.Unlock()
			return current, false
		}
	}
	old := m.current
	changed := next != old
	m.current = next
	m.mu.Unlock()
	if m.listener != nil && changed {
		m.listener.OnTransition(old, next, ev.Message)
	}
	return next, changed
}

// Available reports whether a state has a backing animation.
func (m *Machine) Available(s PetState) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.available[s]
}

// ParseState normalizes a raw string into a PetState, returning an error if it
// is not a known state.
func ParseState(raw string) (PetState, error) {
	s := PetState(raw)
	if !Valid(s) {
		return "", fmt.Errorf("state: unknown %q", raw)
	}
	return s, nil
}
