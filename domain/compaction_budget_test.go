package domain

import "testing"

func TestCompactionBudgetConstants(t *testing.T) {
	cases := []struct {
		name string
		got  int
		want int
	}{
		{"CompactionKeepTokenBudget", CompactionKeepTokenBudget, 64000},
		{"CompactionSummaryMaxOut", CompactionSummaryMaxOut, 64000},
		{"CompactionSystemReserve", CompactionSystemReserve, 300},
		{"CompactionSummaryMinChars", CompactionSummaryMinChars, 200},
		{"CompactionSummaryMaxRetries", CompactionSummaryMaxRetries, 2},
		{"CompactionMaxToolCallChars", CompactionMaxToolCallChars, 200_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("%s = %d, want %d", tc.name, tc.got, tc.want)
			}
		})
	}
}
