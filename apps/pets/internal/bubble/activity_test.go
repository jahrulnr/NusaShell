package bubble

import (
	"testing"
	"time"

	"nusashell-pets/internal/state"
)

func TestActivityHasTwoLinesAndRotatesThinkingCopy(t *testing.T) {
	a := Activity{}
	now := time.Unix(100, 0)
	a.Update(state.Event{State: state.StateThinking}, now)
	header, first := a.Text(now)
	if header != "Thinking…" || first == "" {
		t.Fatalf("thinking = %q / %q", header, first)
	}
	_, second := a.Text(now.Add(5 * time.Second))
	if second == "" || second == first {
		t.Fatalf("thinking copy did not rotate: %q -> %q", first, second)
	}
	now = now.Add(10 * time.Second)
	a.Update(state.Event{State: state.StateReasoning, Title: "Executing…", Message: "read_file(...)"}, now)
	if h, b := a.Text(now); h != "Executing…" || b != "read_file(...)" {
		t.Fatalf("tool = %q / %q", h, b)
	}
	// Same-state events must refresh the tool; a later empty event must not
	// carry a previous operation into a new activity.
	a.Update(state.Event{State: state.StateReasoning, Title: "Executing…", Message: "docs(...)"}, now)
	now = now.Add(4 * time.Second)
	if _, b := a.Text(now); b != "docs(...)" {
		t.Fatalf("stale tool: %q", b)
	}
	a.Update(state.Event{State: state.StateThinking}, now)
	now = now.Add(4 * time.Second)
	if _, b := a.Text(now); b != first {
		t.Fatalf("stale detail after tool completion: %q", b)
	}
	a.Update(state.Event{State: state.StateIdle}, now)
	now = now.Add(4 * time.Second)
	if h, b := a.Text(now); h != "" || b != "" {
		t.Fatalf("idle must hide bubble: %q / %q", h, b)
	}
}

func TestActivityHoldsForFourSecondsAndCoalescesFastEvents(t *testing.T) {
	a := Activity{}
	now := time.Unix(100, 0)
	a.Update(state.Event{State: state.StateThinking}, now)
	h, b := a.Text(now)
	a.Update(state.Event{State: state.StateReasoning, Title: "Executing…", Message: "first(...)"}, now.Add(time.Second))
	a.Update(state.Event{State: state.StateReasoning, Title: "Executing…", Message: "latest(...)"}, now.Add(2*time.Second))
	if gotH, gotB := a.Text(now.Add(3 * time.Second)); gotH != h || gotB != b {
		t.Fatalf("bubble flickered before dwell: %q / %q", gotH, gotB)
	}
	if gotH, gotB := a.Text(now.Add(4 * time.Second)); gotH != "Executing…" || gotB != "latest(...)" {
		t.Fatalf("must show newest, not replay backlog: %q / %q", gotH, gotB)
	}
	a.Update(state.Event{State: state.StateIdle}, now.Add(5*time.Second))
	if gotH, _ := a.Text(now.Add(7 * time.Second)); gotH == "" {
		t.Fatal("idle hid bubble before its dwell elapsed")
	}
	if gotH, _ := a.Text(now.Add(8 * time.Second)); gotH != "" {
		t.Fatal("idle did not hide bubble after dwell")
	}
}

func TestActivityDefaultsAndExplicitMessages(t *testing.T) {
	for _, s := range []state.PetState{state.StateThinking, state.StateReasoning, state.StateWaiting, state.StateDone, state.StateError} {
		a := Activity{}
		a.Update(state.Event{State: s}, time.Time{})
		if h, b := a.Text(time.Time{}); h == "" || b == "" {
			t.Fatalf("%s missing a line: %q / %q", s, h, b)
		}
		a.Update(state.Event{State: s, Message: "  real event message  "}, time.Time{})
		if _, b := a.Text(time.Time{}.Add(time.Minute)); b != "real event message" {
			t.Fatalf("%s lost actual message: %q", s, b)
		}
	}
}

func TestThinkingRotationAlsoGetsMinimumDwell(t *testing.T) {
	a := Activity{}
	now := time.Unix(100, 0)
	a.Update(state.Event{State: state.StateThinking}, now)
	a.Text(now)
	h, b := a.Text(now.Add(5 * time.Second))
	a.Update(state.Event{State: state.StateReasoning, Title: "Executing…", Message: "read_file(...)"}, now.Add(6*time.Second))
	if gotH, gotB := a.Text(now.Add(6 * time.Second)); gotH != h || gotB != b {
		t.Fatalf("rotated copy replaced after only 1s: %q / %q", gotH, gotB)
	}
	if gotH, _ := a.Text(now.Add(9 * time.Second)); gotH != "Executing…" {
		t.Fatal("pending tool not displayed after rotated copy dwell")
	}
}
