package application

import (
	"testing"

	"nusashell/domain"
)

func TestExtractExperienceIncludesCorrections(t *testing.T) {
	conv := &domain.Conversation{
		ID:     "conv_1",
		Status: "idle",
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "implement a backend endpoint"},
			{Role: domain.RoleAssistant, Content: "I'll use Python", ToolCalls: []domain.ToolCall{
				{Name: "file_write", Status: domain.ToolOK, Args: `{"path":"main.py"}`},
			}},
			{Role: domain.RoleUser, Content: "Use Go.", Steer: true},
			{Role: domain.RoleAssistant, Content: "switching to Go"},
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
	trig := domain.DecideLearningTrigger(exp, nil)
	if !trig.Enqueue {
		t.Fatal("correction should enqueue")
	}
}

func TestExtractExperienceFactualQADoesNotEnqueue(t *testing.T) {
	conv := &domain.Conversation{
		ID:     "conv_2",
		Status: "idle",
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "what is HTTP 403?"},
			{Role: domain.RoleAssistant, Content: "Forbidden."},
		},
	}
	exp := ExtractExperience(conv, false)
	trig := domain.DecideLearningTrigger(exp, nil)
	if trig.Enqueue {
		t.Fatalf("enqueued factual Q&A: %+v %+v", exp, trig)
	}
}

func TestExtractExperienceCasualNoProblemIsNotACorrection(t *testing.T) {
	conv := &domain.Conversation{
		ID:     "conv_office",
		Status: "idle",
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "I stopped by the office, no problem"},
			{Role: domain.RoleAssistant, Content: "okay"},
		},
	}
	exp := ExtractExperience(conv, false)
	if len(exp.Corrections) != 0 || exp.Signals.UserCorrections != 0 {
		t.Fatalf("casual utterance recorded as correction: %+v", exp.Corrections)
	}
	if domain.DecideLearningTrigger(exp, nil).Enqueue {
		t.Fatalf("casual utterance enqueued: %+v", exp)
	}
}

func TestExtractExperienceThreeToolSuccessDoesNotEnqueue(t *testing.T) {
	conv := &domain.Conversation{
		ID:     "conv_tools",
		Status: "idle",
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "add a health endpoint"},
			{Role: domain.RoleAssistant, Content: "done", ToolCalls: []domain.ToolCall{
				{Name: "file_read", Status: domain.ToolOK, Args: `{"path":"main.go"}`},
				{Name: "file_patch", Status: domain.ToolOK, Args: `{"path":"main.go"}`},
				{Name: "exec", Status: domain.ToolOK, Args: `{"command":"go test"}`},
			}},
		},
	}
	exp := ExtractExperience(conv, false)
	if !exp.Signals.VerifiedSuccess {
		t.Fatalf("expected verified success signal: %+v", exp.Signals)
	}
	trig := domain.DecideLearningTrigger(exp, nil)
	if trig.Enqueue {
		t.Fatalf("3-tool success without teaching/failure enqueued: %+v %+v", exp.Signals, trig)
	}
}

func TestExtractExperienceHeadlessFlag(t *testing.T) {
	conv := &domain.Conversation{
		ID: "conv_3",
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "remember I prefer Go"},
		},
	}
	exp := ExtractExperience(conv, true)
	if !exp.Headless {
		t.Fatal("headless")
	}
	if !exp.Signals.ExplicitTeaching {
		t.Fatal("teaching signal")
	}
	if domain.DecideLearningTrigger(exp, nil).Enqueue {
		t.Fatal("headless must not enqueue")
	}
}
