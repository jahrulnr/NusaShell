package domain

import "testing"

func TestDecideLearningTrigger_FactualQADoesNotEnqueue(t *testing.T) {
	exp := Experience{
		Goal:    "what is HTTP 403",
		Outcome: ExperienceOutcome{Status: "success"},
	}
	got := DecideLearningTrigger(exp, nil)
	if got.Enqueue {
		t.Fatalf("factual Q&A enqueued: %+v", got)
	}
}

func TestDecideLearningTrigger_EnglishRememberIsNotASpawnGate(t *testing.T) {
	exp := Experience{
		Goal:    "please remember that I prefer Go",
		Outcome: ExperienceOutcome{Status: "success"},
		Signals: ExperienceSignals{ExplicitTeaching: true},
	}
	got := DecideLearningTrigger(exp, nil)
	if got.Enqueue {
		t.Fatalf("keyword teaching must not enqueue without a structural or periodic gate: %+v", got)
	}
}

func TestDecideLearningTrigger_CorrectionIsP0(t *testing.T) {
	exp := Experience{
		Corrections: []UserCorrection{{UserSaid: "Pakai Go", Explicit: true}},
		Signals:     ExperienceSignals{UserCorrections: 1},
	}
	got := DecideLearningTrigger(exp, nil)
	if !got.Enqueue || got.Priority != PriorityP0Teaching || got.Reason != TriggerCorrection {
		t.Fatalf("got %+v", got)
	}
}

func TestDecideLearningTrigger_HeadlessNeverEnqueues(t *testing.T) {
	exp := Experience{
		Headless:    true,
		Corrections: []UserCorrection{{UserSaid: "Use Go"}},
		Signals:     ExperienceSignals{UserCorrections: 1, ExplicitTeaching: true},
	}
	got := DecideLearningTriggerWith(exp, nil, LearningReviewProgress{
		UnreviewedUserTurns: DefaultLearnerNudgeInterval,
		Interval:            DefaultLearnerNudgeInterval,
	})
	if got.Enqueue {
		t.Fatal("headless episode must not enqueue")
	}
}

func TestDecideLearningTrigger_Recovery(t *testing.T) {
	exp := Experience{
		Outcome: ExperienceOutcome{Status: "success"},
		Signals: ExperienceSignals{RootCauseRecovered: true},
	}
	got := DecideLearningTrigger(exp, nil)
	if !got.Enqueue || got.Reason != TriggerRecovery || got.Priority != PriorityP1Recovery {
		t.Fatalf("got %+v", got)
	}
}

func TestDecideLearningTrigger_RepeatedFailure(t *testing.T) {
	exp := Experience{
		Signals: ExperienceSignals{FailureSignature: "nginx-403"},
		Outcome: ExperienceOutcome{Status: "fail"},
	}
	history := []Experience{{Signals: ExperienceSignals{FailureSignature: "nginx-403"}}}
	got := DecideLearningTrigger(exp, history)
	if !got.Enqueue || got.Priority != PriorityP1Recovery || got.Reason != TriggerRepeatedFailure {
		t.Fatalf("got %+v", got)
	}
}

func TestDecideLearningTrigger_RepeatedProcedure(t *testing.T) {
	fp := "file_read>file_patch>exec"
	exp := Experience{
		Signals: ExperienceSignals{ProcedureFingerprint: fp},
		Actions: []ExperienceAction{{Name: "file_read"}, {Name: "file_patch"}, {Name: "exec"}},
	}
	history := []Experience{
		{Signals: ExperienceSignals{ProcedureFingerprint: fp}},
		{Signals: ExperienceSignals{ProcedureFingerprint: fp}},
	}
	got := DecideLearningTrigger(exp, history)
	if !got.Enqueue || got.Reason != TriggerRepeatedProcedure {
		t.Fatalf("got %+v", got)
	}
}

func TestDecideLearningTrigger_SingleProcedureDoesNotEnqueue(t *testing.T) {
	fp := "file_read>file_patch>exec"
	exp := Experience{
		Signals: ExperienceSignals{ProcedureFingerprint: fp},
		Actions: []ExperienceAction{{Name: "file_read"}, {Name: "file_patch"}, {Name: "exec"}},
	}
	got := DecideLearningTrigger(exp, nil)
	if got.Enqueue {
		t.Fatalf("one occurrence enqueued: %+v", got)
	}
}

func TestDecideLearningTrigger_FirstThreeToolSuccessDoesNotEnqueue(t *testing.T) {
	exp := Experience{
		Outcome: ExperienceOutcome{Status: "success"},
		Actions: []ExperienceAction{{Name: "file_read"}, {Name: "file_patch"}, {Name: "exec"}},
		Signals: ExperienceSignals{
			VerifiedSuccess:      true,
			NovelWorkflow:        true,
			ProcedureFingerprint: "file_read>file_patch>exec",
		},
	}
	got := DecideLearningTrigger(exp, nil)
	if got.Enqueue {
		t.Fatalf("first 3-tool success enqueued: %+v", got)
	}
}

func TestDecideLearningTrigger_PeriodicUserTurns(t *testing.T) {
	exp := Experience{Goal: "apa kabar", Outcome: ExperienceOutcome{Status: "success"}}
	got := DecideLearningTriggerWith(exp, nil, LearningReviewProgress{
		UnreviewedUserTurns: DefaultLearnerNudgeInterval,
		Interval:            DefaultLearnerNudgeInterval,
	})
	if !got.Enqueue || got.Reason != TriggerPeriodic || got.Priority != PriorityP4Inferred {
		t.Fatalf("got %+v", got)
	}
}

func TestDecideLearningTrigger_PeriodicBelowIntervalDoesNotEnqueue(t *testing.T) {
	exp := Experience{Goal: "apa kabar", Outcome: ExperienceOutcome{Status: "success"}}
	got := DecideLearningTriggerWith(exp, nil, LearningReviewProgress{
		UnreviewedUserTurns: DefaultLearnerNudgeInterval - 1,
		Interval:            DefaultLearnerNudgeInterval,
	})
	if got.Enqueue {
		t.Fatalf("below-interval periodic enqueued: %+v", got)
	}
}

func TestDecideLearningTrigger_PeriodicToolIters(t *testing.T) {
	exp := Experience{Goal: "keep going", Outcome: ExperienceOutcome{Status: "success"}}
	got := DecideLearningTriggerWith(exp, nil, LearningReviewProgress{
		UnreviewedToolIters: DefaultLearnerNudgeInterval,
		Interval:            DefaultLearnerNudgeInterval,
	})
	if !got.Enqueue || got.Reason != TriggerPeriodic {
		t.Fatalf("got %+v", got)
	}
}

func TestDecideLearningTrigger_PeriodicDisabledWhenIntervalZero(t *testing.T) {
	exp := Experience{Goal: "apa kabar", Outcome: ExperienceOutcome{Status: "success"}}
	got := DecideLearningTriggerWith(exp, nil, LearningReviewProgress{
		UnreviewedUserTurns: 50,
		UnreviewedToolIters: 50,
		Interval:            0,
	})
	if got.Enqueue {
		t.Fatalf("interval 0 still enqueued: %+v", got)
	}
}

func TestDecideLearningTrigger_StructuralWinsOverPeriodic(t *testing.T) {
	fp := "file_read>file_patch>exec"
	exp := Experience{
		Signals: ExperienceSignals{ProcedureFingerprint: fp},
		Actions: []ExperienceAction{{Name: "file_read"}, {Name: "file_patch"}, {Name: "exec"}},
	}
	history := []Experience{
		{Signals: ExperienceSignals{ProcedureFingerprint: fp}},
		{Signals: ExperienceSignals{ProcedureFingerprint: fp}},
	}
	got := DecideLearningTriggerWith(exp, history, LearningReviewProgress{
		UnreviewedUserTurns: DefaultLearnerNudgeInterval,
		Interval:            DefaultLearnerNudgeInterval,
	})
	if !got.Enqueue || got.Reason != TriggerRepeatedProcedure {
		t.Fatalf("periodic must not replace structural reason: %+v", got)
	}
}

func TestCountUnreviewedLearningProgress(t *testing.T) {
	messages := []Message{
		{Role: RoleUser, Content: "one"},
		{Role: RoleAssistant, Content: "ok", ToolCalls: []ToolCall{{Name: "file_read"}}},
		{Role: RoleUser, Content: "two"},
		{Role: RoleAssistant, Content: "ok", ToolCalls: []ToolCall{{Name: "file_patch"}, {Name: "exec"}}},
		{Role: RoleUser, Content: "three"},
		{Role: RoleAssistant, Content: "done"},
	}
	turns, iters := CountUnreviewedLearningProgress(messages, 0)
	if turns != 3 {
		t.Fatalf("turns=%d want 3", turns)
	}
	if iters != 2 {
		t.Fatalf("iters=%d want 2 assistant tool rounds", iters)
	}
	turns, iters = CountUnreviewedLearningProgress(messages, 2)
	if turns != 2 || iters != 1 {
		t.Fatalf("from index 2: turns=%d iters=%d", turns, iters)
	}
}

func TestCreatorMayPromote(t *testing.T) {
	if CreatorMayPromote(ActorSkillEvolver) {
		t.Fatal("evolver must not promote")
	}
	if CreatorMayPromote(ActorLearner) {
		t.Fatal("learner must not promote")
	}
}

func TestEffectiveLearnerNudgeInterval(t *testing.T) {
	if got := EffectiveLearnerNudgeInterval(nil); got != DefaultLearnerNudgeInterval {
		t.Fatalf("nil = %d, want default %d", got, DefaultLearnerNudgeInterval)
	}
	neg := -1
	if got := EffectiveLearnerNudgeInterval(&neg); got != DefaultLearnerNudgeInterval {
		t.Fatalf("negative = %d, want default", got)
	}
	zero := 0
	if got := EffectiveLearnerNudgeInterval(&zero); got != 0 {
		t.Fatalf("zero = %d, want 0 (disabled)", got)
	}
	custom := 4
	if got := EffectiveLearnerNudgeInterval(&custom); got != 4 {
		t.Fatalf("custom = %d, want 4", got)
	}
	over := LearnerNudgeIntervalCap + 50
	if got := EffectiveLearnerNudgeInterval(&over); got != LearnerNudgeIntervalCap {
		t.Fatalf("over cap = %d, want %d", got, LearnerNudgeIntervalCap)
	}
}
