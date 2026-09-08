package domain

import (
	"strings"
	"unicode/utf8"

	clock "nusashell/pkg/time"
)

const (
	maxGoalChars       = 400
	maxCorrectionChars = 400
	maxDigestChars     = 80
	maxActionSteps     = 120
)

// harnessToolNames are injected runtime/harness tool calls that are never
// agent-initiated work. They must not shape experiences, fingerprints, or
// review progress. Hydration checkpoint calls are already excluded by their
// "hydrate-" call-id prefix; the name list additionally covers transcripts
// recorded before the prefix existed, plus announcement injections that
// never carry the prefix (but still land in the persisted transcript).
var harnessToolNames = map[string]bool{
	"runtime_context": true,
	"announcement":    true,
	"mcp_list":        true,
	"tool_list":       true,
}

// IsHarnessToolCall reports whether a tool call is injected runtime
// activity (hydration checkpoint or harness announcement) rather than agent
// work, independent of its call id.
func IsHarnessToolCall(name string) bool {
	return harnessToolNames[strings.TrimSpace(name)]
}

// ExtractExperience builds a cheap structured episode from a finished turn.
// It never calls an LLM. Hidden hydration checkpoint tools (call IDs with
// HydrateToolCallPrefix) are not agent work: they are omitted from Actions,
// SkillIDs, and ProcedureFingerprint so they cannot look like a repeated
// procedure. Actions are taken from the current user turn (messages since
// the latest non-steer user prompt), capped at maxActionSteps.
func ExtractExperience(conv *Conversation, headless bool) Experience {
	now := clock.NewTime().Time()
	exp := Experience{
		ID:             NewULID(IDPrefixExp),
		ConversationID: "",
		Timestamp:      now,
		Headless:       headless,
		Outcome:        ExperienceOutcome{Status: "unknown"},
		Actions:        []ExperienceAction{},
		Corrections:    []UserCorrection{},
	}
	if conv == nil {
		return exp
	}
	exp.ConversationID = conv.ID
	exp.Scope = ExperienceScope{
		Workspace: conv.Workspace,
		Project:   workspaceProject(conv.Workspace),
	}
	var lastUser string
	var lastAssistant string
	failed := 0
	var failSig string
	skillIDs := []string{}
	start := experienceTurnStart(conv.Messages)
	for _, msg := range conv.Messages[start:] {
		switch msg.Role {
		case RoleUser:
			text := strings.TrimSpace(msg.Content)
			if text == "" || IsCompactionSummary(text) {
				continue
			}
			if msg.Steer {
				exp.Corrections = append(exp.Corrections, UserCorrection{
					Type:     "approach",
					UserSaid: clip(text, maxCorrectionChars),
					Explicit: true,
				})
			}
			if !msg.Steer {
				lastUser = text
			}
		case RoleAssistant:
			if IsHydrationMessage(msg) {
				continue
			}
			if strings.TrimSpace(msg.Content) != "" {
				lastAssistant = msg.Content
			}
			if msg.Status == StatusError {
				exp.Outcome.Status = "fail"
			}
			for _, tc := range msg.ToolCalls {
				if IsHydrationCallID(tc.ID) {
					continue
				}
				if IsHarnessToolCall(tc.Name) {
					continue
				}
				if len(exp.Actions) >= maxActionSteps {
					break
				}
				act := ExperienceAction{
					Name:   tc.Name,
					Digest: clip(tc.Args, maxDigestChars),
					Failed: tc.Status == ToolFailed,
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
	exp.Signals.ProcedureFingerprint = ProcedureFingerprint(exp.Actions)
	exp.Signals.SkillIDs = skillIDs
	exp.Signals.Retries = failed
	if exp.Outcome.Status != "fail" {
		if conv.Status == "idle" || lastAssistant != "" {
			exp.Outcome.Status = "success"
		}
	}
	if exp.Outcome.Status == "success" && sameToolFailedThenSucceeded(exp.Actions) {
		exp.Signals.RootCauseRecovered = true
		exp.Outcome.Verification = []string{"same tool failed then succeeded"}
	}
	return exp
}

// experienceTurnStart is the index of the latest non-steer user message.
// Actions, fingerprints, and recovery signals are taken from that turn
// onward so a long conversation's first 120 tools cannot hide later work.
func experienceTurnStart(messages []Message) int {
	start := 0
	for i, msg := range messages {
		if msg.Role == RoleUser && !msg.Steer && strings.TrimSpace(msg.Content) != "" && !IsCompactionSummary(msg.Content) {
			start = i
		}
	}
	return start
}

func sameToolFailedThenSucceeded(actions []ExperienceAction) bool {
	failed := make(map[string]bool)
	for _, act := range actions {
		name := strings.TrimSpace(act.Name)
		if name == "" {
			continue
		}
		if act.Failed {
			failed[name] = true
			continue
		}
		if failed[name] {
			return true
		}
	}
	return false
}

func workspaceProject(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	workspace = strings.TrimRight(workspace, `/\`)
	if workspace == "" {
		return ""
	}
	if i := strings.LastIndexAny(workspace, `/\`); i >= 0 {
		return workspace[i+1:]
	}
	return workspace
}

func clip(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max])
}
