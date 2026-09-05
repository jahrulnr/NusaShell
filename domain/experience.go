package domain

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
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
	PriorityP0Teaching  LearningPriority = 0 // explicit teaching / correction
	PriorityP1Recovery  LearningPriority = 1 // verified failure + recovery / skill regression
	PriorityP2Recurring LearningPriority = 2 // repeated workflow
	PriorityP3Novel     LearningPriority = 3 // novel verified procedure
	PriorityP4Inferred  LearningPriority = 4 // low-confidence inference (unused in v1 rules)
)

// LearningTrigger is the decision to enqueue background learning.
type LearningTrigger struct {
	Enqueue  bool
	Reason   string
	Priority LearningPriority
}

const (
	teachPhraseRemember = "remember"
	teachPhraseLearn    = "learn this"
	teachPhraseSkill    = "make this a skill"
	teachPhraseCreate   = "create a skill"
	teachPhraseDontFgt  = "don't forget"
	teachPhraseDoNotFgt = "do not forget"
)

// DetectExplicitTeaching reports whether text is an explicit remember/learn/skill request.
func DetectExplicitTeaching(text string) bool {
	n := strings.ToLower(strings.TrimSpace(text))
	if n == "" {
		return false
	}
	switch {
	case strings.Contains(n, teachPhraseLearn),
		strings.Contains(n, teachPhraseSkill),
		strings.Contains(n, teachPhraseCreate),
		strings.Contains(n, teachPhraseDontFgt),
		strings.Contains(n, teachPhraseDoNotFgt):
		return true
	}
	return containsWord(n, teachPhraseRemember)
}

// DetectCorrectionHeuristic reports whether a user message is correcting
// the agent. Cues must start the message or a new sentence so prose like
// "I stopped by the office, no problem" is not a correction. Steer already
// counts separately in ExtractExperience.
func DetectCorrectionHeuristic(text string) bool {
	n := strings.ToLower(strings.TrimSpace(text))
	if n == "" {
		return false
	}
	if correctionCueAtStart(n) {
		return true
	}
	for i := 0; i < len(n); i++ {
		switch n[i] {
		case '\n':
			if correctionCueAtStart(n[i+1:]) {
				return true
			}
		case '.', '!', '?':
			if i+1 < len(n) && n[i+1] == ' ' && correctionCueAtStart(n[i+2:]) {
				return true
			}
		}
	}
	return false
}

func correctionCueAtStart(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, cue := range []string{"no,", "no.", "don't ", "do not ", "stop ", "not that", "i said"} {
		if strings.HasPrefix(s, cue) {
			return true
		}
	}
	if strings.HasPrefix(s, "wrong") {
		rest := s[len("wrong"):]
		if rest == "" {
			return true
		}
		r, _ := utf8.DecodeRuneInString(rest)
		return !unicode.IsLetter(r)
	}
	return false
}

func containsWord(haystack, word string) bool {
	start := 0
	for {
		i := strings.Index(haystack[start:], word)
		if i < 0 {
			return false
		}
		i += start
		beforeOK := i == 0 || !unicode.IsLetter(rune(haystack[i-1]))
		after := i + len(word)
		afterOK := after >= len(haystack) || !unicode.IsLetter(rune(haystack[after]))
		if beforeOK && afterOK {
			return true
		}
		start = i + len(word)
	}
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

// DecideLearningTrigger applies the MVP signal rules. Headless episodes
// are recorded but never enqueue a learner. Factual Q&A and a first-time
// successful multi-tool turn (no teaching, correction, recovery, or
// repeated procedure) do not enqueue.
func DecideLearningTrigger(exp Experience, history []Experience) LearningTrigger {
	if exp.Headless {
		return LearningTrigger{}
	}
	if exp.Signals.ExplicitTeaching {
		return LearningTrigger{Enqueue: true, Reason: "explicit_teaching", Priority: PriorityP0Teaching}
	}
	if exp.Signals.UserCorrections > 0 || len(exp.Corrections) > 0 {
		return LearningTrigger{Enqueue: true, Reason: "user_correction", Priority: PriorityP0Teaching}
	}
	if exp.Signals.RootCauseRecovered && exp.Outcome.Status == "success" {
		return LearningTrigger{Enqueue: true, Reason: "verified_recovery", Priority: PriorityP1Recovery}
	}
	if sig := strings.TrimSpace(exp.Signals.FailureSignature); sig != "" {
		n := 1
		for _, h := range history {
			if h.Signals.FailureSignature == sig {
				n++
			}
		}
		if n >= 2 {
			return LearningTrigger{Enqueue: true, Reason: "repeated_failure", Priority: PriorityP1Recovery}
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
			return LearningTrigger{Enqueue: true, Reason: "repeated_procedure", Priority: PriorityP2Recurring}
		}
	}
	return LearningTrigger{}
}
