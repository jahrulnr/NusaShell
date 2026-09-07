package domain

import "testing"

func TestRequestTokenHeuristicConstants(t *testing.T) {
	if RequestTokenPerMessageOverhead != 4 {
		t.Errorf("RequestTokenPerMessageOverhead = %d, want 4", RequestTokenPerMessageOverhead)
	}
	if RequestTokenImageCost != 1500 {
		t.Errorf("RequestTokenImageCost = %d, want 1500", RequestTokenImageCost)
	}
	if RequestTokenSafetyBuffer != 1.05 {
		t.Errorf("RequestTokenSafetyBuffer = %v, want 1.05", RequestTokenSafetyBuffer)
	}
	if RequestTokenCharsPerToken != 4 {
		t.Errorf("RequestTokenCharsPerToken = %d, want 4", RequestTokenCharsPerToken)
	}
}
