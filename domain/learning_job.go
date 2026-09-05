package domain

import "time"

// Learning job kinds. New work uses LearningJobLearner (one spawn, internal
// stages). Legacy kind strings remain so persisted jobs.jsonl still loads.
const (
	LearningJobLearner     = "learner"
	LearningJobConsolidate = "consolidate"
	LearningJobEvolveSkill = "evolve_skill"
	LearningJobEvaluate    = "evaluate"
	LearningJobRetire      = "retire_stale"
)

const (
	LearningJobQueued   = "queued"
	LearningJobRunning  = "running"
	LearningJobDone     = "done"
	LearningJobError    = "error"
	LearningJobRejected = "rejected"
)

const (
	LearningOpProposed = "proposed"
	LearningOpAccepted = "accepted"
	LearningOpRejected = "rejected"
)

const (
	OpMemoryUpsert     = "memory.upsert"
	OpMemoryMerge      = "memory.merge"
	OpMemoryStrengthen = "memory.strengthen"
	OpMemoryContradict = "memory.contradict"
	OpMemoryRetire     = "memory.retire"
	OpSkillCreate      = "skill.create"
	OpSkillRevise      = "skill.revise"
	OpSkillPromote     = "skill.promote"
	OpSkillRollback    = "skill.rollback"
	OpSkillRetire      = "skill.retire"
)

const (
	ActorLearner      = "background.learner"
	ActorConsolidator = "background.memory-consolidator"
	ActorSkillEvolver = "background.skill-evolution"
	ActorSkillEval    = "background.skill-evaluator"
	ActorUser         = "user"
	ActorRuntime      = "runtime"
)

const (
	MaxSkillRevisions = 5
)

// LearningJob is one queued background learning unit. Jobs are JSONL
// upsert-by-id. Spawn is Hermes-style: structural signals or a periodic
// unreviewed-turn / tool-iteration nudge, never keyword matching.
type LearningJob struct {
	ID           string           `json:"id"`
	Kind         string           `json:"kind"`
	ExperienceID string           `json:"experience_id,omitempty"`
	SkillID      string           `json:"skill_id,omitempty"`
	Reason       string           `json:"reason"`
	Priority     LearningPriority `json:"priority"`
	Status       string           `json:"status"`
	Error        string           `json:"error,omitempty"`
	CreatedAt    time.Time        `json:"created_at"`
	StartedAt    *time.Time       `json:"started_at,omitempty"`
	FinishedAt   *time.Time       `json:"finished_at,omitempty"`
	Revision     int              `json:"revision,omitempty"`
}

// LearningOperation is a typed write the LLM proposed. The runtime decides
// whether it is allowed; the model never owns the catalog files.
type LearningOperation struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	Status     string         `json:"status"`
	Actor      string         `json:"actor"`
	JobID      string         `json:"job_id,omitempty"`
	TargetID   string         `json:"target_id,omitempty"`
	TargetType string         `json:"target_type,omitempty"`
	Payload    map[string]any `json:"payload,omitempty"`
	Evidence   []string       `json:"evidence,omitempty"`
	Reason     string         `json:"reason,omitempty"`
	Risk       string         `json:"risk,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

// ValidLearningOpKind reports whether kind is a known typed operation.
func ValidLearningOpKind(kind string) bool {
	switch kind {
	case OpMemoryUpsert, OpMemoryMerge, OpMemoryStrengthen, OpMemoryContradict, OpMemoryRetire,
		OpSkillCreate, OpSkillRevise, OpSkillPromote, OpSkillRollback, OpSkillRetire:
		return true
	}
	return false
}

// CreatorMayPromote is always false: the learner never marks a skill trusted.
func CreatorMayPromote(actor string) bool {
	return false
}
