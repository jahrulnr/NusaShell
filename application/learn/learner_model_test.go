package learn

import (
	"testing"

	"nusashell/domain"
)

// stubSettings holds a fixed domain.Settings for the learn.Settings port.
type stubSettings struct {
	settings domain.Settings
}

func (s *stubSettings) Get() domain.Settings { return s.settings }

// TestLearningModelIDExplicitReviewModelWins verifies that an explicitly
// configured review_model is returned unconditionally, ignoring the
// ResolveLearnerModel cascade.
func TestLearningModelIDExplicitReviewModelWins(t *testing.T) {
	svc := New(Deps{
		Settings: &stubSettings{settings: domain.Settings{ReviewModel: "explicit:model"}},
		ResolveLearnerModel: func(string) string {
			t.Fatal("ResolveLearnerModel should not be called when review_model is set")
			return ""
		},
	})
	if got := svc.learningModelID("conv_src"); got != "explicit:model" {
		t.Fatalf("learningModelID = %q, want %q", got, "explicit:model")
	}
}

// TestLearningModelIDFallsBackToResolverWhenReviewModelEmpty verifies that
// when review_model is empty, the ResolveLearnerModel seam is consulted so
// the learner gets an explicit model instead of inheriting DelegateModel
// via ResolveHeadlessModel.
func TestLearningModelIDFallsBackToResolverWhenReviewModelEmpty(t *testing.T) {
	svc := New(Deps{
		Settings: &stubSettings{settings: domain.Settings{DelegateModel: "delegate:model"}},
		ResolveLearnerModel: func(sourceConvID string) string {
			if sourceConvID != "conv_src" {
				t.Fatalf("ResolveLearnerModel received sourceConvID = %q, want %q", sourceConvID, "conv_src")
			}
			return "resolved:model"
		},
	})
	got := svc.learningModelID("conv_src")
	if got == "" {
		t.Fatal("learningModelID = empty, want resolved model so the learner does not inherit DelegateModel")
	}
	if got == "delegate:model" {
		t.Fatal("learningModelID = delegate model; the learner must not inherit DelegateModel")
	}
	if got != "resolved:model" {
		t.Fatalf("learningModelID = %q, want %q", got, "resolved:model")
	}
}

// TestLearningModelIDEmptyWhenNoResolverWired verifies that without a
// resolver the function returns "" (today's behavior for partial wiring /
// tests), letting the headless turn resolve the first enabled provider.
func TestLearningModelIDEmptyWhenNoResolverWired(t *testing.T) {
	svc := New(Deps{
		Settings: &stubSettings{settings: domain.Settings{DelegateModel: "delegate:model"}},
	})
	if got := svc.learningModelID("conv_src"); got != "" {
		t.Fatalf("learningModelID = %q, want empty when no resolver is wired", got)
	}
}
