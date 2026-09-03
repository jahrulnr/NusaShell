package interaction

import "testing"

func TestRunnerSettlesRunDirectionAfterHysteresis(t *testing.T) {
	t.Parallel()
	r := NewRunner(8)

	// Below the hysteresis step no direction settles.
	if got := r.Update(3); got != 0 {
		t.Fatalf("small rightward move = %d, want 0", got)
	}
	if got := r.Update(2); got != 0 {
		t.Fatalf("accumulated 5px = %d, want 0", got)
	}
	// Accumulated 8px: settles right, accumulator resets.
	if got := r.Update(3); got != 1 {
		t.Fatalf("accumulated 8px = %d, want 1 (right)", got)
	}
	// Small counter-motion does not flap the pose.
	if got := r.Update(-4); got != 1 {
		t.Fatalf("4px leftwards = %d, want sticky 1", got)
	}
	// Accumulated -8px: settles left.
	if got := r.Update(-4); got != -1 {
		t.Fatalf("accumulated -8px = %d, want -1 (left)", got)
	}
	if got := r.Direction(); got != -1 {
		t.Fatalf("direction = %d, want -1", got)
	}
}

func TestRunnerKeepsDirectionOnVerticalMotion(t *testing.T) {
	t.Parallel()
	r := NewRunner(8)
	if got := r.Update(10); got != 1 {
		t.Fatalf("settle right = %d, want 1", got)
	}
	// Pure vertical drag has no horizontal delta; facing is retained.
	for i := 0; i < 5; i++ {
		if got := r.Update(0); got != 1 {
			t.Fatalf("vertical move = %d, want retained 1", got)
		}
	}
}

func TestRunnerResetStartsFreshDrag(t *testing.T) {
	t.Parallel()
	r := NewRunner(8)
	r.Update(10)
	if r.Direction() != 1 {
		t.Fatalf("direction = %d, want 1 before reset", r.Direction())
	}
	r.Reset()
	if got := r.Direction(); got != 0 {
		t.Fatalf("direction after reset = %d, want 0", got)
	}
	if got := r.Update(-8); got != -1 {
		t.Fatalf("fresh drag settling left = %d, want -1", got)
	}
}

func TestRunnerUsesDefaultStepForInvalidStep(t *testing.T) {
	t.Parallel()
	r := NewRunner(0)
	if got := r.Update(8); got != 1 {
		t.Fatalf("default-step settle = %d, want 1", got)
	}
}
