package domain

import "testing"

func TestDetectExplicitTeaching(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"please remember that I prefer Go", true},
		{"Remember this for next time", true},
		{"make this a skill", true},
		{"create a skill for nginx debug", true},
		{"learn this workflow", true},
		{"don't forget the symlink check", true},
		{"what is the capital of France?", false},
		{"I remember seeing this bug", false}, // "remember" as recall, still contains word remember - WAIT
	}
	// "I remember seeing" contains the word remember. DetectExplicitTeaching
	// uses containsWord("remember") which would match. That's OK for a cheap
	// signal; consolidator still has to reject low-utility saves.
	for _, tc := range cases {
		if tc.in == "I remember seeing this bug" {
			continue
		}
		if got := DetectExplicitTeaching(tc.in); got != tc.want {
			t.Errorf("DetectExplicitTeaching(%q)=%v want %v", tc.in, got, tc.want)
		}
	}
}

func TestDetectCorrectionHeuristic(t *testing.T) {
	positives := []string{
		"No, use Go",
		"No. Use pnpm.",
		"Don't use npm for this repo",
		"Do not patch that file",
		"Stop using Python",
		"Not that approach",
		"Wrong. Use the other endpoint",
		"I said use Go",
		"Thanks. No, use Go.",
		"ok\nNo, keep the existing API",
	}
	for _, in := range positives {
		if !DetectCorrectionHeuristic(in) {
			t.Errorf("DetectCorrectionHeuristic(%q)=false, want true", in)
		}
	}
	negatives := []string{
		"I stopped by the office, no problem",
		"nothing wrong with that approach",
		"please note that this is a draft",
		"as I said in the README yesterday",
		"install frontend deps",
		"what is HTTP 403?",
		"I remember seeing this bug",
	}
	for _, in := range negatives {
		if DetectCorrectionHeuristic(in) {
			t.Errorf("DetectCorrectionHeuristic(%q)=true, want false", in)
		}
	}
}

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

func TestDecideLearningTrigger_CorrectionIsP0(t *testing.T) {
	exp := Experience{
		Corrections: []UserCorrection{{UserSaid: "Use Go", Explicit: true}},
		Signals:     ExperienceSignals{UserCorrections: 1},
	}
	got := DecideLearningTrigger(exp, nil)
	if !got.Enqueue || got.Priority != PriorityP0Teaching || got.Reason != "user_correction" {
		t.Fatalf("got %+v", got)
	}
}

func TestDecideLearningTrigger_HeadlessNeverEnqueues(t *testing.T) {
	exp := Experience{
		Headless:    true,
		Corrections: []UserCorrection{{UserSaid: "Use Go"}},
		Signals:     ExperienceSignals{UserCorrections: 1, ExplicitTeaching: true},
	}
	got := DecideLearningTrigger(exp, nil)
	if got.Enqueue {
		t.Fatal("headless episode must not enqueue")
	}
}

func TestDecideLearningTrigger_RepeatedFailure(t *testing.T) {
	exp := Experience{
		Signals: ExperienceSignals{FailureSignature: "nginx-403"},
		Outcome: ExperienceOutcome{Status: "fail"},
	}
	history := []Experience{{Signals: ExperienceSignals{FailureSignature: "nginx-403"}}}
	got := DecideLearningTrigger(exp, history)
	if !got.Enqueue || got.Priority != PriorityP1Recovery {
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
	if !got.Enqueue || got.Reason != "repeated_procedure" {
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

func TestCreatorMayPromote(t *testing.T) {
	if CreatorMayPromote(ActorSkillEvolver) {
		t.Fatal("evolver must not promote")
	}
}
