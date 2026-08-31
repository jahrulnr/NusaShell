package domain

import "testing"

func TestServerCompactionThresholdFloorConstant(t *testing.T) {
	if ServerCompactionThresholdFloor != 120_000 {
		t.Fatalf("ServerCompactionThresholdFloor = %d, want 120000", ServerCompactionThresholdFloor)
	}
}

func TestServerCompactionThreshold(t *testing.T) {
	cases := []struct {
		name   string
		window int
		want   int
	}{
		{"below floor uses floor", 100_000, ServerCompactionThresholdFloor},
		{"at floor uses floor", ServerCompactionThresholdFloor, ServerCompactionThresholdFloor},
		{"above floor uses 90 percent", 200_000, 180_000},
		{"large window uses 90 percent", 1_000_000, 900_000},
		{"zero window uses floor", 0, ServerCompactionThresholdFloor},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ServerCompactionThreshold(tc.window); got != tc.want {
				t.Fatalf("ServerCompactionThreshold(%d) = %d, want %d", tc.window, got, tc.want)
			}
		})
	}
}

func TestUserNudgeTextConstant(t *testing.T) {
	if UserNudgeText != "." {
		t.Fatalf("UserNudgeText = %q, want %q", UserNudgeText, ".")
	}
}
