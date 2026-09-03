package direction

import "testing"

func TestIndexFromPointUsesScreenClockwiseDirections(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		x, y int32
		want int
	}{
		{name: "up", x: 50, y: 0, want: 0},
		{name: "right", x: 100, y: 50, want: 4},
		{name: "down", x: 50, y: 100, want: 8},
		{name: "left", x: 0, y: 50, want: 12},
		{name: "up right", x: 85, y: 15, want: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IndexFromPoint(tc.x, tc.y, 100, 100, 8); got != tc.want {
				t.Fatalf("IndexFromPoint(%d,%d) = %d, want %d", tc.x, tc.y, got, tc.want)
			}
		})
	}
}

func TestIndexFromPointUsesNeutralDeadzone(t *testing.T) {
	t.Parallel()
	if got := IndexFromPoint(50, 50, 100, 100, 8); got != Neutral {
		t.Fatalf("center direction = %d, want Neutral (%d)", got, Neutral)
	}
	if got := IndexFromPoint(56, 50, 100, 100, 8); got != Neutral {
		t.Fatalf("deadzone direction = %d, want Neutral (%d)", got, Neutral)
	}
}

func TestIndexFromPointRejectsInvalidCanvas(t *testing.T) {
	t.Parallel()
	if got := IndexFromPoint(1, 1, 0, 100, 8); got != Neutral {
		t.Fatalf("invalid canvas direction = %d, want Neutral (%d)", got, Neutral)
	}
}
