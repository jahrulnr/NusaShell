package domain

import (
	"testing"
	"time"
)

func TestReviewAgentPolicyConstants(t *testing.T) {
	if DefaultReviewCooldown != 15*time.Minute {
		t.Errorf("DefaultReviewCooldown = %v, want 15m", DefaultReviewCooldown)
	}
	if DefaultReviewMemoryEveryNTurns != 10 {
		t.Errorf("DefaultReviewMemoryEveryNTurns = %d, want 10", DefaultReviewMemoryEveryNTurns)
	}
	if DefaultReviewTranscriptTailMsgs != 40 {
		t.Errorf("DefaultReviewTranscriptTailMsgs = %d, want 40", DefaultReviewTranscriptTailMsgs)
	}
	if DefaultReviewMaxTranscriptChars != 4000 {
		t.Errorf("DefaultReviewMaxTranscriptChars = %d, want 4000", DefaultReviewMaxTranscriptChars)
	}
	if DefaultReviewMaxTranscriptTokens != 30000 {
		t.Errorf("DefaultReviewMaxTranscriptTokens = %d, want 30000", DefaultReviewMaxTranscriptTokens)
	}
	if ReviewMaxToolArgsChars != 500 {
		t.Errorf("ReviewMaxToolArgsChars = %d, want 500", ReviewMaxToolArgsChars)
	}
	if ReviewMaxToolOutputChars != 800 {
		t.Errorf("ReviewMaxToolOutputChars = %d, want 800", ReviewMaxToolOutputChars)
	}
}
