package domain

import (
	"strings"
	"testing"
	"time"
)

func TestNewULIDIsSortableAndPrefixed(t *testing.T) {
	a := NewULID("skill")
	time.Sleep(2 * time.Millisecond)
	b := NewULID("skill")
	if !strings.HasPrefix(a, "skill_") {
		t.Fatalf("missing prefix: %q", a)
	}
	if !strings.HasPrefix(b, "skill_") {
		t.Fatalf("missing prefix: %q", b)
	}
	if a >= b {
		t.Fatalf("ULIDs not sortable by time: %q >= %q", a, b)
	}
}

func TestNewULIDUniqueness(t *testing.T) {
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		id := NewULID("mem")
		if seen[id] {
			t.Fatalf("duplicate ULID: %q", id)
		}
		seen[id] = true
	}
}

func TestCombineWeights(t *testing.T) {
	tests := []struct {
		w1, w2, want float64
	}{
		{0, 0, 0},
		{1, 1, 1},
		{0.5, 0.5, 0.75},
		{0.3, 0.6, 1 - 0.7*0.4},
	}
	for _, tc := range tests {
		got := CombineWeights(tc.w1, tc.w2)
		if abs(got-tc.want) > 1e-9 {
			t.Errorf("CombineWeights(%v, %v) = %v, want %v", tc.w1, tc.w2, got, tc.want)
		}
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
