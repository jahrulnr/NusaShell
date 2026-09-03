package renderer

import "testing"

import "time"

func TestNextFrameLoopsAtEnd(t *testing.T) {
	t.Parallel()
	if next, finished := nextFrame(3, 4, true); next != 0 || finished {
		t.Fatalf("loop end = next %d finished %v, want 0/false", next, finished)
	}
}

func TestNextFrameReportsNonLoopCompletion(t *testing.T) {
	t.Parallel()
	if next, finished := nextFrame(3, 4, false); next != 3 || !finished {
		t.Fatalf("non-loop end = next %d finished %v, want 3/true", next, finished)
	}
}

func TestNextFrameAdvancesBeforeEnd(t *testing.T) {
	t.Parallel()
	if next, finished := nextFrame(1, 4, false); next != 2 || finished {
		t.Fatalf("middle frame = next %d finished %v, want 2/false", next, finished)
	}
}

func TestNextFrameNormalizesInvalidFrame(t *testing.T) {
	t.Parallel()
	if next, finished := nextFrame(99, 2, true); next != 1 || finished {
		t.Fatalf("invalid frame = next %d finished %v, want 1/false", next, finished)
	}
}

func TestSlowPlaybackDelayHalvesPlaybackRate(t *testing.T) {
	t.Parallel()
	if got := slowPlaybackDelay(120 * time.Millisecond); got != 240*time.Millisecond {
		t.Fatalf("slowPlaybackDelay(120ms) = %v, want 240ms", got)
	}
}

func TestSlowPlaybackDelayUsesSafeFallback(t *testing.T) {
	t.Parallel()
	if got := slowPlaybackDelay(0); got != 10*time.Millisecond {
		t.Fatalf("slowPlaybackDelay(0) = %v, want 10ms", got)
	}
}
