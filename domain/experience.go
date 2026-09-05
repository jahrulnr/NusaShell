package domain

import (
	"strings"
	"time"
)

// Experience is a structured record of one interaction episode. It is the
// learning substrate: memory records and skills are projections of
// experiences, never the other way around. Experiences are append-only.
type Experience struct {
	ID             string             `json:"id"`
	ConversationID string             `json:"conversation_id"`
	TaskID         string             `json:"task_id,omitempty"`
	Timestamp      time.Time          `json:"timestamp"`
	Scope          ExperienceScope    `json:"scope"`
	Goal           string             `json:"goal"`
	Actions        []ExperienceAction `json:"actions"`
	Observations   []string           `json:"observations,omitempty"`
	Corrections    []UserCorrection   `json:"user_corrections"`
	Outcome        ExperienceOutcome  `json:"outcome"`
	Signals        ExperienceSignals  `json:"signals"`
	Headless       bool               `json:"headless,omitempty"`
}

// ExperienceScope is the narrowest context that fully explains the episode.
type ExperienceScope struct {
	Workspace   string `json:"workspace,omitempty"`
	Project     string `json:"project,omitempty"`
	Environment string `json:"environment,omitempty"`
}

// ExperienceAction is a coarse tool step (name + short args digest). Raw
// transcripts are never stored here.
type ExperienceAction struct {
	Name   string `json:"name"`
	Digest string `json:"digest,omitempty"`
	Failed bool   `json:"failed,omitempty"`
}

// UserCorrection is an explicit user override of agent behavior.
type UserCorrection struct {
	Type     string `json:"type"` // procedure | preference | fact | approach
	UserSaid string `json:"user_said"`
	Previous string `json:"previous_behavior,omitempty"`
	Desired  string `json:"desired_behavior,omitempty"`
	Explicit bool   `json:"explicit"`
}

// ExperienceOutcome is the episode result without dumping logs.
type ExperienceOutcome struct {
	Status       string   `json:"status"` // success | fail | unknown
	Verification []string `json:"verification,omitempty"`
}

// ExperienceSignals are cheap deterministic counts used by the trigger.
type ExperienceSignals struct {
	UserCorrections      int      `json:"user_corrections"`
	Retries              int      `json:"retries"`
	FailedActions        int      `json:"failed_actions"`
	VerifiedSuccess      bool     `json:"verified_success"`
	NovelWorkflow        bool     `json:"novel_workflow"`
	ExplicitTeaching     bool     `json:"explicit_teaching"`
	SkillIDs             []string `json:"skill_ids,omitempty"`
	FailureSignature     string   `json:"failure_signature,omitempty"`
	ProcedureFingerprint string   `json:"procedure_fingerprint,omitempty"`
	RootCauseRecovered   bool     `json:"root_cause_recovered"`
}

// LearningPriority is the job queue rank (P0 highest).
type LearningPriority int

const (
	PriorityP0Teaching  LearningPriority = 0 // steer / explicit correction
	PriorityP1Recovery  LearningPriority = 1 // verified failure + recovery / repeated failure
	PriorityP2Recurring LearningPriority = 2 // repeated workflow
	PriorityP3Novel     LearningPriority = 3 // novel verified procedure
	PriorityP4Inferred  LearningPriority = 4 // periodic review (Hermes-style nudge)
)

// Learning trigger reasons. Orchestrator spawn uses structural and periodic
// reasons only. explicit_teaching is classified inside the learner, never by
// matching words in any language.
const (
	TriggerExplicitTeaching  = "explicit_teaching"
	TriggerCorrection        = "correction"
	TriggerRecovery          = "recovery"
	TriggerRepeatedFailure   = "repeated_failure"
	TriggerRepeatedProcedure = "repeated_procedure"
	TriggerPeriodic          = "periodic"
)

// DefaultLearnerNudgeInterval is the Hermes-style periodic spawn gate:
// unreviewed user turns or tool-loop iterations. Zero disables periodic spawn.
const DefaultLearnerNudgeInterval = 10

// LearnerNudgeIntervalCap is the maximum configurable periodic interval.
const LearnerNudgeIntervalCap = 100

// EffectiveLearnerNudgeInterval resolves a settings value: nil or negative
// uses the product default, 0 disables periodic spawn, and values above the
// cap are clamped.
func EffectiveLearnerNudgeInterval(value *int) int {
	if value == nil || *value < 0 {
		return DefaultLearnerNudgeInterval
	}
	if *value > LearnerNudgeIntervalCap {
		return LearnerNudgeIntervalCap
	}
	return *value
}

// LearningTrigger is the decision to enqueue background learning.
type LearningTrigger struct {
	Enqueue  bool
	Reason   string
	Priority LearningPriority
}

// LearningReviewProgress is the cheap periodic spawn input. Interval 0
// disables the periodic gate so only structural signals enqueue.
type LearningReviewProgress struct {
	UnreviewedUserTurns int
	UnreviewedToolIters int
	Interval            int
}

// ProcedureFingerprint concatenates tool names for recurrence matching.
func ProcedureFingerprint(actions []ExperienceAction) string {
	if len(actions) == 0 {
		return ""
	}
	parts := make([]string, 0, len(actions))
	for _, a := range actions {
		name := strings.TrimSpace(a.Name)
		if name == "" {
			continue
		}
		parts = append(parts, name)
	}
	return strings.Join(parts, ">")
}

// CountUnreviewedLearningProgress counts user turns and assistant tool-loop
// rounds in messages[start:]. Tool iterations are assistant messages that
// issued at least one tool call, matching Hermes's per-API-round counter.
func CountUnreviewedLearningProgress(messages []Message, start int) (userTurns, toolIters int) {
	if start < 0 {
		start = 0
	}
	if start > len(messages) {
		return 0, 0
	}
	for _, msg := range messages[start:] {
		if IsHydrationMessage(msg) || (msg.Role == RoleUser && IsCompactionSummary(msg.Content)) {
			continue
		}
		switch msg.Role {
		case RoleUser:
			if strings.TrimSpace(msg.Content) != "" {
				userTurns++
			}
		case RoleAssistant:
			if len(msg.ToolCalls) > 0 {
				toolIters++
			}
		}
	}
	return userTurns, toolIters
}

// DecideLearningTrigger applies language-agnostic spawn rules. Headless
// episodes are recorded but never enqueue a learner. Factual Q&A and a
// first-time successful multi-tool turn do not enqueue unless the periodic
// nudge interval has elapsed.
func DecideLearningTrigger(exp Experience, history []Experience) LearningTrigger {
	return DecideLearningTriggerWith(exp, history, LearningReviewProgress{})
}

// DecideLearningTriggerWith is DecideLearningTrigger plus the Hermes periodic
// gate (every N unreviewed user turns or tool iterations).
func DecideLearningTriggerWith(exp Experience, history []Experience, progress LearningReviewProgress) LearningTrigger {
	if exp.Headless {
		return LearningTrigger{}
	}
	if exp.Signals.UserCorrections > 0 || len(exp.Corrections) > 0 {
		return LearningTrigger{Enqueue: true, Reason: TriggerCorrection, Priority: PriorityP0Teaching}
	}
	if exp.Signals.RootCauseRecovered && exp.Outcome.Status == "success" {
		return LearningTrigger{Enqueue: true, Reason: TriggerRecovery, Priority: PriorityP1Recovery}
	}
	if sig := strings.TrimSpace(exp.Signals.FailureSignature); sig != "" {
		n := 1
		for _, h := range history {
			if h.Signals.FailureSignature == sig {
				n++
			}
		}
		if n >= 2 {
			return LearningTrigger{Enqueue: true, Reason: TriggerRepeatedFailure, Priority: PriorityP1Recovery}
		}
	}
	if fp := strings.TrimSpace(exp.Signals.ProcedureFingerprint); fp != "" && strings.Count(fp, ">") >= 2 {
		n := 1
		for _, h := range history {
			if h.Signals.ProcedureFingerprint == fp {
				n++
			}
		}
		if n >= 3 {
			return LearningTrigger{Enqueue: true, Reason: TriggerRepeatedProcedure, Priority: PriorityP2Recurring}
		}
	}
	if progress.Interval > 0 &&
		(progress.UnreviewedUserTurns >= progress.Interval ||
			progress.UnreviewedToolIters >= progress.Interval) {
		return LearningTrigger{Enqueue: true, Reason: TriggerPeriodic, Priority: PriorityP4Inferred}
	}
	return LearningTrigger{}
}
