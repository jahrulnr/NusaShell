package contracts

import (
	"nusashell/domain"
	"time"
)

const MemoryTierRecord = "record"

type ExperienceDTO struct {
	ID             string                    `json:"id"`
	ConversationID string                    `json:"conversation_id"`
	Timestamp      string                    `json:"timestamp"`
	Goal           string                    `json:"goal,omitempty"`
	Headless       bool                      `json:"headless,omitempty"`
	Scope          domain.ExperienceScope    `json:"scope"`
	Actions        []domain.ExperienceAction `json:"actions,omitempty"`
	Observations   []string                  `json:"observations,omitempty"`
	Corrections    []domain.UserCorrection   `json:"user_corrections,omitempty"`
	Outcome        domain.ExperienceOutcome  `json:"outcome"`
	Signals        domain.ExperienceSignals  `json:"signals"`
}

type ExperienceListResult struct {
	Experiences []ExperienceDTO `json:"experiences"`
}

type ExperienceIDRequest struct {
	ID string `json:"id"`
}

type ExperienceGetResult struct {
	Experience ExperienceDTO `json:"experience"`
}

func ExperienceDTOFromDomain(e *domain.Experience) ExperienceDTO {
	if e == nil {
		return ExperienceDTO{}
	}
	return ExperienceDTO{
		ID:             e.ID,
		ConversationID: e.ConversationID,
		Timestamp:      e.Timestamp.UTC().Format(time.RFC3339),
		Goal:           e.Goal,
		Headless:       e.Headless,
		Scope:          e.Scope,
		Actions:        e.Actions,
		Observations:   e.Observations,
		Corrections:    e.Corrections,
		Outcome:        e.Outcome,
		Signals:        e.Signals,
	}
}

type MemoryRecordDTO struct {
	ID        string             `json:"id"`
	Type      string             `json:"type,omitempty"`
	Subject   string             `json:"subject,omitempty"`
	Predicate string             `json:"predicate,omitempty"`
	Object    string             `json:"object,omitempty"`
	Body      string             `json:"body"`
	Content   string             `json:"content"` // same as body; UI catalog title
	Scope     domain.MemoryScope `json:"scope"`
	Status    string             `json:"status"`
	Source    string             `json:"source,omitempty"`
	Project   string             `json:"project,omitempty"`
	CreatedAt string             `json:"created_at"`
	UpdatedAt string             `json:"updated_at"`
	Tier      string             `json:"tier"`
}

func MemoryRecordDTOFromDomain(m *domain.MemoryRecord) MemoryRecordDTO {
	if m == nil {
		return MemoryRecordDTO{}
	}
	return MemoryRecordDTO{
		ID:        m.ID,
		Type:      m.Type,
		Subject:   m.Subject,
		Predicate: m.Predicate,
		Object:    m.Object,
		Body:      m.Body,
		Content:   m.Body,
		Scope:     m.Scope,
		Status:    m.Status,
		Source:    m.Source,
		Project:   m.Scope.Project,
		CreatedAt: m.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: m.UpdatedAt.UTC().Format(time.RFC3339),
		Tier:      MemoryTierRecord,
	}
}

type LearningJobDTO struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	ExperienceID string `json:"experience_id,omitempty"`
	SkillID      string `json:"skill_id,omitempty"`
	Reason       string `json:"reason,omitempty"`
	Priority     int    `json:"priority"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
	CreatedAt    string `json:"created_at"`
	Revision     int    `json:"revision,omitempty"`
}

type LearningJobListResult struct {
	Jobs []LearningJobDTO `json:"jobs"`
}

type LearningJobStatusRequest struct {
	ID string `json:"id"`
}

type LearningJobEvent struct {
	JobID          string `json:"job_id"`
	Kind           string `json:"kind,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
}

func LearningJobDTOFromDomain(j *domain.LearningJob) LearningJobDTO {
	if j == nil {
		return LearningJobDTO{}
	}
	return LearningJobDTO{
		ID:           j.ID,
		Kind:         j.Kind,
		ExperienceID: j.ExperienceID,
		SkillID:      j.SkillID,
		Reason:       j.Reason,
		Priority:     int(j.Priority),
		Status:       j.Status,
		Error:        j.Error,
		CreatedAt:    j.CreatedAt.UTC().Format(time.RFC3339),
		Revision:     j.Revision,
	}
}

type SkillPromoteRequest struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by,omitempty"`
}

type SkillRollbackRequest struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
	OwnedBy string `json:"owned_by,omitempty"`
}
