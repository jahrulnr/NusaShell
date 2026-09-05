package domain

import (
	"strings"
	"time"
)

// Memory record types. A record is descriptive knowledge, not a procedure.
const (
	MemoryTypeEpisode           = "episode"
	MemoryTypeFact              = "fact"
	MemoryTypePreference        = "preference"
	MemoryTypeConstraint        = "constraint"
	MemoryTypeProjectConvention = "project_convention"
	MemoryTypeEnvironmentFact   = "environment_fact"
	MemoryTypeBelief            = "belief"
)

// Memory record lifecycle. Retired records stay on disk for audit but are
// excluded from retrieval and hydration.
const (
	MemoryStatusCandidate  = "candidate"
	MemoryStatusLearned    = "learned"
	MemoryStatusStable     = "stable"
	MemoryStatusSuperseded = "superseded"
	MemoryStatusRetired    = "retired"
)

// Memory scope levels. Prefer the narrowest level that explains the evidence.
const (
	MemoryScopeUser        = "user"
	MemoryScopeDomain      = "domain"
	MemoryScopeProject     = "project"
	MemoryScopeRepo        = "repo"
	MemoryScopeEnvironment = "environment"
	MemoryScopeTask        = "task"
)

// MemoryRecord is a structured durable fact/preference/constraint. It is
// the catalog that replaced free-text fragments. user.md and soul.md remain
// separate always-injected documents and are never stored as MemoryRecords.
type MemoryRecord struct {
	ID                       string      `json:"id"`
	Type                     string      `json:"type"`
	Subject                  string      `json:"subject,omitempty"`
	Predicate                string      `json:"predicate,omitempty"`
	Object                   string      `json:"object,omitempty"`
	Body                     string      `json:"body"`
	Scope                    MemoryScope `json:"scope"`
	Confidence               float64     `json:"confidence"`
	Stability                float64     `json:"stability"`
	Utility                  float64     `json:"utility"`
	EvidenceCount            int         `json:"evidence_count"`
	SupportingExperiences    []string    `json:"supporting_experiences,omitempty"`
	ContradictingExperiences []string    `json:"contradicting_experiences,omitempty"`
	ValidFrom                time.Time   `json:"valid_from"`
	ValidUntil               *time.Time  `json:"valid_until,omitempty"`
	LastConfirmed            time.Time   `json:"last_confirmed"`
	Status                   string      `json:"status"`
	Source                   string      `json:"source"` // consolidator | user-delete
	CreatedAt                time.Time   `json:"created_at"`
	UpdatedAt                time.Time   `json:"updated_at"`
}

// MemoryScope locates a record. Empty Level defaults to user.
type MemoryScope struct {
	Level   string `json:"level"`
	Domain  string `json:"domain,omitempty"`
	Project string `json:"project,omitempty"`
	Repo    string `json:"repo,omitempty"`
	Task    string `json:"task,omitempty"`
}

// MemorySearchFilter AND-combines non-empty fields.
type MemorySearchFilter struct {
	Query          string
	Type           string
	Status         string
	Scope          string
	Project        string
	Limit          int
	IncludeRetired bool
}

// ValidMemoryType reports whether t is a known record type.
func ValidMemoryType(t string) bool {
	switch t {
	case MemoryTypeEpisode, MemoryTypeFact, MemoryTypePreference, MemoryTypeConstraint,
		MemoryTypeProjectConvention, MemoryTypeEnvironmentFact, MemoryTypeBelief:
		return true
	}
	return false
}

// ValidMemoryStatus reports whether s is a known lifecycle status.
func ValidMemoryStatus(s string) bool {
	switch s {
	case MemoryStatusCandidate, MemoryStatusLearned, MemoryStatusStable,
		MemoryStatusSuperseded, MemoryStatusRetired:
		return true
	}
	return false
}

// Retrievable reports whether the record may enter search/hydration.
func (m *MemoryRecord) Retrievable() bool {
	if m == nil {
		return false
	}
	return m.Status != MemoryStatusRetired && m.Status != MemoryStatusSuperseded
}

// Retire marks the record retired in place. Retirement is not deletion.
func (m *MemoryRecord) Retire(now time.Time) {
	if m == nil {
		return
	}
	m.Status = MemoryStatusRetired
	m.UpdatedAt = now
	m.Source = "user-delete"
}

// NormalizeMemoryRecord fills defaults required before persist.
func NormalizeMemoryRecord(m *MemoryRecord, now time.Time) {
	if m == nil {
		return
	}
	m.Type = strings.TrimSpace(m.Type)
	if !ValidMemoryType(m.Type) {
		m.Type = MemoryTypeBelief
	}
	if !ValidMemoryStatus(m.Status) {
		m.Status = MemoryStatusCandidate
	}
	if strings.TrimSpace(m.Scope.Level) == "" {
		m.Scope.Level = MemoryScopeUser
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	if m.ValidFrom.IsZero() {
		m.ValidFrom = now
	}
	if m.LastConfirmed.IsZero() {
		m.LastConfirmed = now
	}
	if m.EvidenceCount < 1 && len(m.SupportingExperiences) > 0 {
		m.EvidenceCount = len(m.SupportingExperiences)
	}
}

// PolicyRank is the hardcoded v1 selection order. Lower is stronger.
func PolicyRank(kind string) int {
	switch kind {
	case "explicit_local":
		return 0
	case "project_convention":
		return 1
	case "task_request":
		return 2
	case "stable_preference":
		return 3
	case "inferred_preference":
		return 4
	default:
		return 5
	}
}
