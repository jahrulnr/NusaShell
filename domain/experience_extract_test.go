package domain

import "testing"

func TestExtractExperienceIncludesCorrections(t *testing.T) {
	conv := &Conversation{
		ID:     "conv_1",
		Status: "idle",
		Messages: []Message{
			{Role: RoleUser, Content: "implement a backend endpoint"},
			{Role: RoleAssistant, Content: "I'll use Python", ToolCalls: []ToolCall{
				{Name: "file_write", Status: ToolOK, Args: `{"path":"main.py"}`},
			}},
			{Role: RoleUser, Content: "Use Go.", Steer: true},
			{Role: RoleAssistant, Content: "switching to Go"},
		},
	}
	exp := ExtractExperience(conv, false)
	if exp.Goal != "implement a backend endpoint" {
		t.Fatalf("goal=%q", exp.Goal)
	}
	if len(exp.Corrections) == 0 || exp.Signals.UserCorrections == 0 {
		t.Fatalf("corrections missing: %+v", exp)
	}
	if exp.Corrections[0].UserSaid != "Use Go." {
		t.Fatalf("correction=%q", exp.Corrections[0].UserSaid)
	}
	trig := DecideLearningTrigger(exp, nil)
	if !trig.Enqueue {
		t.Fatal("correction should enqueue")
	}
}

func TestExtractExperienceFactualQADoesNotEnqueue(t *testing.T) {
	conv := &Conversation{
		ID:     "conv_2",
		Status: "idle",
		Messages: []Message{
			{Role: RoleUser, Content: "what is HTTP 403?"},
			{Role: RoleAssistant, Content: "Forbidden."},
		},
	}
	exp := ExtractExperience(conv, false)
	trig := DecideLearningTrigger(exp, nil)
	if trig.Enqueue {
		t.Fatalf("enqueued factual Q&A: %+v %+v", exp, trig)
	}
}

func TestExtractExperienceCasualNoProblemIsNotACorrection(t *testing.T) {
	conv := &Conversation{
		ID:     "conv_office",
		Status: "idle",
		Messages: []Message{
			{Role: RoleUser, Content: "I stopped by the office, no problem"},
			{Role: RoleAssistant, Content: "okay"},
		},
	}
	exp := ExtractExperience(conv, false)
	if len(exp.Corrections) != 0 || exp.Signals.UserCorrections != 0 {
		t.Fatalf("casual utterance recorded as correction: %+v", exp.Corrections)
	}
	if DecideLearningTrigger(exp, nil).Enqueue {
		t.Fatalf("casual utterance enqueued: %+v", exp)
	}
}

func TestExtractExperienceThreeToolSuccessDoesNotEnqueue(t *testing.T) {
	conv := &Conversation{
		ID:     "conv_tools",
		Status: "idle",
		Messages: []Message{
			{Role: RoleUser, Content: "add a health endpoint"},
			{Role: RoleAssistant, Content: "done", ToolCalls: []ToolCall{
				{Name: "file_read", Status: ToolOK, Args: `{"path":"main.go"}`},
				{Name: "file_patch", Status: ToolOK, Args: `{"path":"main.go"}`},
				{Name: "exec", Status: ToolOK, Args: `{"command":"go test"}`},
			}},
		},
	}
	exp := ExtractExperience(conv, false)
	if exp.Signals.VerifiedSuccess {
		t.Fatalf("action count must not mint verified_success: %+v", exp.Signals)
	}
	trig := DecideLearningTrigger(exp, nil)
	if trig.Enqueue {
		t.Fatalf("3-tool success without teaching/failure enqueued: %+v %+v", exp.Signals, trig)
	}
}

func TestExtractExperienceHeadlessFlag(t *testing.T) {
	conv := &Conversation{
		ID: "conv_3",
		Messages: []Message{
			{Role: RoleUser, Content: "ingat ya, saya lebih suka Go", Steer: true},
		},
	}
	exp := ExtractExperience(conv, true)
	if !exp.Headless {
		t.Fatal("headless")
	}
	if len(exp.Corrections) == 0 {
		t.Fatal("steer correction should still be extracted")
	}
	if DecideLearningTrigger(exp, nil).Enqueue {
		t.Fatal("headless must not enqueue")
	}
}

func TestExtractExperienceDoesNotKeywordMatchTeaching(t *testing.T) {
	for _, text := range []string{
		"please remember that I prefer Go",
		"ingat ya, saya lebih suka Go",
		"jangan lupa pakai pnpm",
	} {
		conv := &Conversation{
			ID:     "conv_teach",
			Status: "idle",
			Messages: []Message{
				{Role: RoleUser, Content: text},
				{Role: RoleAssistant, Content: "ok"},
			},
		}
		exp := ExtractExperience(conv, false)
		if exp.Signals.ExplicitTeaching {
			t.Fatalf("keyword/teaching phrase set ExplicitTeaching: %q", text)
		}
		if DecideLearningTrigger(exp, nil).Enqueue {
			t.Fatalf("teaching phrase enqueued without periodic/structural gate: %q", text)
		}
	}
}

func TestExtractExperienceSkipsHydrationTools(t *testing.T) {
	hyd := HydrateToolCallPrefix
	conv := &Conversation{
		ID:     "conv_hydrate",
		Status: "idle",
		Messages: []Message{
			{Role: RoleUser, Content: "Project NusaShell"},
			{Role: RoleAssistant, ToolCalls: []ToolCall{
				{ID: hyd + "n_0", Name: "runtime_context", Status: ToolOK},
				{ID: hyd + "n_1", Name: "file_read", Status: ToolOK, Args: `{"path":"AGENTS.md"}`},
				{ID: hyd + "n_2", Name: "file_read", Status: ToolOK, Args: `{"path":"user.md"}`},
				{ID: hyd + "n_3", Name: "file_read", Status: ToolOK, Args: `{"path":"soul.md"}`},
				{ID: hyd + "n_4", Name: "memory_project", Status: ToolOK},
				{ID: hyd + "n_5", Name: "skill", Status: ToolOK},
			}},
			{Role: RoleAssistant, Content: "ok", ToolCalls: []ToolCall{
				{ID: "call_real", Name: "file_read", Status: ToolOK, Args: `{"path":"main.go"}`},
			}},
		},
	}
	exp := ExtractExperience(conv, false)
	if len(exp.Actions) != 1 || exp.Actions[0].Name != "file_read" || exp.Actions[0].Digest != `{"path":"main.go"}` {
		t.Fatalf("actions=%+v, want one real file_read", exp.Actions)
	}
	if exp.Signals.ProcedureFingerprint != "file_read" {
		t.Fatalf("fingerprint=%q, want file_read without hydration", exp.Signals.ProcedureFingerprint)
	}
	if len(exp.Signals.SkillIDs) != 0 {
		t.Fatalf("hydration skill call leaked into skill IDs: %v", exp.Signals.SkillIDs)
	}
	if exp.Signals.VerifiedSuccess {
		t.Fatal("hydration tools must not count toward verified success")
	}
}

func TestExtractExperienceSkipsHydrationCallsInMixedTurn(t *testing.T) {
	conv := &Conversation{
		ID:     "conv_mixed",
		Status: "idle",
		Messages: []Message{
			{Role: RoleUser, Content: "read main"},
			{Role: RoleAssistant, Content: "ok", ToolCalls: []ToolCall{
				{ID: HydrateToolCallPrefix + "n_0", Name: "runtime_context", Status: ToolOK},
				{ID: "call_real", Name: "file_read", Status: ToolOK, Args: `{"path":"main.go"}`},
			}},
		},
	}
	exp := ExtractExperience(conv, false)
	if len(exp.Actions) != 1 || exp.Actions[0].Name != "file_read" {
		t.Fatalf("actions=%+v, want only the real file_read", exp.Actions)
	}
	if exp.Signals.ProcedureFingerprint != "file_read" {
		t.Fatalf("fingerprint=%q", exp.Signals.ProcedureFingerprint)
	}
}

func TestExtractExperienceHydrationOnlyIsNotVerifiedSuccess(t *testing.T) {
	hyd := HydrateToolCallPrefix
	conv := &Conversation{
		ID:     "conv_hydrate_only",
		Status: "idle",
		Messages: []Message{
			{Role: RoleUser, Content: "hi"},
			{Role: RoleAssistant, ToolCalls: []ToolCall{
				{ID: hyd + "n_0", Name: "runtime_context", Status: ToolOK},
				{ID: hyd + "n_1", Name: "file_read", Status: ToolOK},
				{ID: hyd + "n_2", Name: "file_read", Status: ToolOK},
				{ID: hyd + "n_3", Name: "memory_project", Status: ToolOK},
			}},
		},
	}
	exp := ExtractExperience(conv, false)
	if len(exp.Actions) != 0 {
		t.Fatalf("hydration-only actions=%+v", exp.Actions)
	}
	if exp.Signals.ProcedureFingerprint != "" {
		t.Fatalf("fingerprint=%q", exp.Signals.ProcedureFingerprint)
	}
	if exp.Signals.VerifiedSuccess {
		t.Fatal("hydration-only turn must not be verified success")
	}
	if DecideLearningTrigger(exp, nil).Enqueue {
		t.Fatalf("hydration-only turn enqueued: %+v", exp)
	}
}

func TestExtractExperienceRootCauseRecoveredRequiresSameToolFailThenSuccess(t *testing.T) {
	conv := &Conversation{
		ID:     "conv_recover",
		Status: "idle",
		Messages: []Message{
			{Role: RoleUser, Content: "run the tests"},
			{Role: RoleAssistant, Content: "retrying", ToolCalls: []ToolCall{
				{Name: "exec", Status: ToolFailed, Args: `{"command":"go test ./missing"}`},
				{Name: "exec", Status: ToolOK, Args: `{"command":"go test ./domain"}`},
			}},
		},
	}
	exp := ExtractExperience(conv, false)
	if !exp.Signals.RootCauseRecovered {
		t.Fatalf("same tool fail then success must be recovery: %+v", exp.Signals)
	}
	if DecideLearningTrigger(exp, nil).Reason != TriggerRecovery {
		t.Fatalf("recovery should enqueue: %+v", DecideLearningTrigger(exp, nil))
	}
}

func TestExtractExperienceUnrelatedToolFailureIsNotRecovery(t *testing.T) {
	conv := &Conversation{
		ID:     "conv_typo",
		Status: "idle",
		Messages: []Message{
			{Role: RoleUser, Content: "inspect the repo"},
			{Role: RoleAssistant, Content: "done", ToolCalls: []ToolCall{
				{Name: "exec", Status: ToolFailed, Args: `{"command":"ls fooo"}`},
				{Name: "file_read", Status: ToolOK, Args: `{"path":"main.go"}`},
				{Name: "grep", Status: ToolOK, Args: `{"pattern":"func"}`},
			}},
		},
	}
	exp := ExtractExperience(conv, false)
	if exp.Signals.RootCauseRecovered {
		t.Fatalf("a failed tool plus other successes is not recovery: %+v", exp.Signals)
	}
	if DecideLearningTrigger(exp, nil).Enqueue {
		t.Fatalf("typo shell failure enqueued recovery: %+v", DecideLearningTrigger(exp, nil))
	}
}

func TestExtractExperienceActionsAreFromCurrentTurn(t *testing.T) {
	var firstTurn []ToolCall
	for i := 0; i < maxActionSteps; i++ {
		firstTurn = append(firstTurn, ToolCall{ID: "old", Name: "exec", Status: ToolOK, Args: "{}"})
	}
	conv := &Conversation{
		ID:     "conv_long",
		Status: "idle",
		Messages: []Message{
			{Role: RoleUser, Content: "first task"},
			{Role: RoleAssistant, Content: "done", ToolCalls: firstTurn},
			{Role: RoleUser, Content: "now search for the handler"},
			{Role: RoleAssistant, Content: "found it", ToolCalls: []ToolCall{
				{Name: "grep", Status: ToolOK, Args: `{"pattern":"handle"}`},
				{Name: "file_read", Status: ToolOK, Args: `{"path":"app.go"}`},
			}},
		},
	}
	exp := ExtractExperience(conv, false)
	if exp.Goal != "now search for the handler" {
		t.Fatalf("goal=%q", exp.Goal)
	}
	if len(exp.Actions) != 2 || exp.Actions[0].Name != "grep" || exp.Actions[1].Name != "file_read" {
		t.Fatalf("current-turn actions=%+v, want grep then file_read", exp.Actions)
	}
	if exp.Signals.ProcedureFingerprint != "grep>file_read" {
		t.Fatalf("fingerprint=%q", exp.Signals.ProcedureFingerprint)
	}
}
