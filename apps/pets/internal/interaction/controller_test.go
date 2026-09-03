package interaction

import (
	"testing"
	"time"
)

func TestControllerTreatsSmallMotionAsClick(t *testing.T) {
	c := NewController(5)
	c.Press(Point{X: 10, Y: 20}, Point{X: 100, Y: 200})

	if got, moved := c.Move(Point{X: 14, Y: 23}); moved || got != (Point{X: 100, Y: 200}) {
		t.Fatalf("small move = %+v, moved=%v", got, moved)
	}
	if !c.Release(Point{X: 14, Y: 23}) {
		t.Fatal("small motion should activate on release")
	}
}

func TestControllerMovesWindowAfterDragThreshold(t *testing.T) {
	c := NewController(5)
	c.Press(Point{X: 10, Y: 20}, Point{X: 100, Y: 200})

	got, moved := c.Move(Point{X: 20, Y: 35})
	if !moved || got != (Point{X: 110, Y: 215}) {
		t.Fatalf("drag move = %+v, moved=%v", got, moved)
	}
	if c.Release(Point{X: 20, Y: 35}) {
		t.Fatal("drag release must not activate")
	}
}

func TestControllerCancelDiscardsPress(t *testing.T) {
	c := NewController(5)
	c.Press(Point{X: 10, Y: 20}, Point{X: 100, Y: 200})
	c.Cancel()

	if got, moved := c.Move(Point{X: 50, Y: 60}); moved || got != (Point{}) {
		t.Fatalf("move after cancel = %+v, moved=%v", got, moved)
	}
	if c.Release(Point{X: 50, Y: 60}) {
		t.Fatal("cancelled press must not activate")
	}
}

func TestControllerCancelsDragWhenPolledPointerIsReleased(t *testing.T) {
	c := NewController(5)
	c.Press(Point{X: 110, Y: 220}, Point{X: 100, Y: 200})

	if got, moved := c.MoveWhileHeld(Point{X: 150, Y: 260}, true); !moved || got != (Point{X: 140, Y: 240}) {
		t.Fatalf("drag from global pointer = %+v, moved=%v", got, moved)
	}
	if got, moved := c.MoveWhileHeld(Point{X: 150, Y: 260}, false); moved || got != (Point{}) {
		t.Fatalf("released global pointer = %+v, moved=%v", got, moved)
	}
	if got, moved := c.MoveWhileHeld(Point{X: 200, Y: 300}, true); moved || got != (Point{}) {
		t.Fatalf("motion after polled release = %+v, moved=%v", got, moved)
	}
	if c.Release(Point{}) {
		t.Fatal("polled release must not activate the pet")
	}
}

func TestPollIntervalUsesDisplayRefreshRate(t *testing.T) {
	if got, want := PollInterval(144), time.Second/144; got != want {
		t.Fatalf("144 Hz interval = %v, want %v", got, want)
	}
	if got, want := PollInterval(75), time.Second/75; got != want {
		t.Fatalf("75 Hz interval = %v, want %v", got, want)
	}
}

func TestPollIntervalFallsBackForUnknownRefreshRate(t *testing.T) {
	if got, want := PollInterval(0), time.Second/60; got != want {
		t.Fatalf("unknown refresh interval = %v, want %v", got, want)
	}
}
