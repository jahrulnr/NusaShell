package application

import (
	"path/filepath"
	"strings"
	"unicode/utf8"

	"nusashell/domain"
	clock "nusashell/pkg/time"
)

const (
	maxGoalChars       = 400
	maxCorrectionChars = 400
	maxDigestChars     = 80
	maxActionSteps     = 40
)

// ExtractExperience builds a cheap structured episode from a finished turn.
// It never calls an LLM.
func ExtractExperience(conv *domain.Conversation, headless bool) domain.Experience {
	now := clock.NewTime().Time()
	exp := domain.Experience{
		ID:             domain.NewULID(domain.IDPrefixExp),
		ConversationID: "",
		Timestamp:      now,
		Headless:       headless,
		Outcome:        domain.ExperienceOutcome{Status: "unknown"},
		Actions:        []domain.ExperienceAction{},
		Corrections:    []domain.UserCorrection{},
	}
	if conv == nil {
		return exp
	}
	exp.ConversationID = conv.ID
	exp.Scope = domain.ExperienceScope{
		Workspace: conv.Workspace,
		Project:   workspaceProject(conv.Workspace),
	}
	var lastUser string
	var lastAssistant string
	failed := 0
	var failSig string
	skillIDs := []string{}
	for _, msg := range conv.Messages {
		switch msg.Role {
		case domain.RoleUser:
			text := strings.TrimSpace(msg.Content)
			if text == "" {
				continue
			}
			if msg.Steer || domain.DetectCorrectionHeuristic(text) {
				exp.Corrections = append(exp.Corrections, domain.UserCorrection{
					Type:     "approach",
					UserSaid: clip(text, maxCorrectionChars),
					Explicit: msg.Steer || domain.DetectExplicitTeaching(text),
				})
			}
			if domain.DetectExplicitTeaching(text) {
				exp.Signals.ExplicitTeaching = true
			}
			if !msg.Steer {
				lastUser = text
			}
		case domain.RoleAssistant:
			if strings.TrimSpace(msg.Content) != "" {
				lastAssistant = msg.Content
			}
			if msg.Status == domain.StatusError {
				exp.Outcome.Status = "fail"
			}
			for _, tc := range msg.ToolCalls {
				if len(exp.Actions) >= maxActionSteps {
					break
				}
				act := domain.ExperienceAction{
					Name:   tc.Name,
					Digest: clip(tc.Args, maxDigestChars),
					Failed: tc.Status == domain.ToolFailed,
				}
				exp.Actions = append(exp.Actions, act)
				if act.Failed {
					failed++
					if failSig == "" {
						failSig = tc.Name
					}
				}
				if tc.Name == "skill" {
					skillIDs = append(skillIDs, clip(tc.Args, 40))
				}
			}
		}
	}
	exp.Goal = clip(lastUser, maxGoalChars)
	exp.Signals.UserCorrections = len(exp.Corrections)
	exp.Signals.FailedActions = failed
	exp.Signals.FailureSignature = failSig
	exp.Signals.ProcedureFingerprint = domain.ProcedureFingerprint(exp.Actions)
	exp.Signals.SkillIDs = skillIDs
	exp.Signals.Retries = failed
	if exp.Outcome.Status != "fail" {
		if conv.Status == "idle" || lastAssistant != "" {
			exp.Outcome.Status = "success"
		}
	}
	if exp.Outcome.Status == "success" && failed > 0 && len(exp.Actions) > failed {
		exp.Signals.RootCauseRecovered = true
		exp.Outcome.Verification = []string{"turn completed after failed tool"}
	}
	if exp.Outcome.Status == "success" && len(exp.Actions) >= 3 {
		exp.Signals.VerifiedSuccess = true
	}
	return exp
}

func workspaceProject(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return ""
	}
	return filepath.Base(workspace)
}

func clip(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max])
}
