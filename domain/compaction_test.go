package domain

import "testing"

func TestCompactionTriggerTokensAutoSubtractsMaxOutput(t *testing.T) {
	// 256k context, 64k output → available = 196608 → trigger = 80% × 196608 = 157286
	trigger := CompactionTriggerTokens(262144, 65536, Settings{CompactionThreshold: 0})
	if trigger != 157286 {
		t.Fatalf("auto trigger with maxOutput: got %d, want 157286", trigger)
	}
}

func TestCompactionTriggerTokensAutoNoMaxOutput(t *testing.T) {
	// No maxOutput → fallback to 80% × window
	trigger := CompactionTriggerTokens(262144, 0, Settings{CompactionThreshold: 0})
	if trigger != 209715 {
		t.Fatalf("auto trigger without maxOutput: got %d, want 209715", trigger)
	}
}

func TestCompactionTriggerTokensMaxOutputExceedsWindow(t *testing.T) {
	// maxOutput > window → available = window (not negative)
	trigger := CompactionTriggerTokens(100000, 200000, Settings{CompactionThreshold: 0})
	if trigger != 80000 {
		t.Fatalf("maxOutput > window: got %d, want 80000", trigger)
	}
}

func TestCompactionTriggerTokensExplicitThresholdCappedByBudget(t *testing.T) {
	// Explicit threshold 200k, but available budget = 196608 → cap at 80% × 196608 = 157286
	trigger := CompactionTriggerTokens(262144, 65536, Settings{CompactionThreshold: 200000})
	if trigger != 157286 {
		t.Fatalf("threshold capped by budget: got %d, want 157286", trigger)
	}
}

func TestCompactionTriggerTokensExplicitThresholdWithinBudget(t *testing.T) {
	// Explicit threshold 100k, available budget = 192k → 100k < 153600, use 100k
	trigger := CompactionTriggerTokens(262144, 65536, Settings{CompactionThreshold: 100000})
	if trigger != 100000 {
		t.Fatalf("threshold within budget: got %d, want 100000", trigger)
	}
}

func TestShouldCompact(t *testing.T) {
	tests := []struct {
		name      string
		estimated int
		trigger   int
		want      bool
	}{
		{"over watermark compacts", 157287, 157286, true},
		{"at watermark does not compact", 157286, 157286, false},
		{"under watermark does not compact", 100000, 157286, false},
		{"zero estimate does not compact", 0, 157286, false},
		{"any estimate compacts at zero trigger", 1, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldCompact(tt.estimated, tt.trigger); got != tt.want {
				t.Fatalf("ShouldCompact(%d, %d) = %v, want %v", tt.estimated, tt.trigger, got, tt.want)
			}
		})
	}
}

func TestMaxCompactionAttempts(t *testing.T) {
	if MaxCompactionAttempts != 3 {
		t.Fatalf("MaxCompactionAttempts = %d, want 3", MaxCompactionAttempts)
	}
}
