package learn

import (
	"fmt"
	"strings"
	"testing"

	"nusashell/domain"
)

type scopeRecordStore struct {
	records []*domain.MemoryRecord
}

func (s *scopeRecordStore) List() []*domain.MemoryRecord { return s.records }
func (s *scopeRecordStore) Get(id string) (*domain.MemoryRecord, error) {
	for _, record := range s.records {
		if record != nil && record.ID == id {
			return record, nil
		}
	}
	return nil, fmt.Errorf("record %s not found", id)
}
func (s *scopeRecordStore) Save(record *domain.MemoryRecord) error { return nil }
func (s *scopeRecordStore) Delete(id string) error                 { return nil }

func TestOpsFromLearnerConsolidateDefaultsToSourceProject(t *testing.T) {
	stage := &learnerConsolidate{
		Action: "write",
		Entry: &learnerEntry{
			Type:     domain.MemoryTypeFact,
			Content:  "NusaShell uses a single provider adapter",
			Evidence: "source conversation",
		},
	}
	ops := opsFromLearnerConsolidate(stage, "job", "exp", "NusaShell")
	if len(ops) != 1 {
		t.Fatalf("ops=%d, want 1", len(ops))
	}
	if got := PayloadString(ops[0].Payload, "scope"); got != domain.MemoryScopeProject {
		t.Fatalf("scope=%q, want %q", got, domain.MemoryScopeProject)
	}
	if got := PayloadString(ops[0].Payload, "project"); got != "NusaShell" {
		t.Fatalf("project=%q, want NusaShell", got)
	}
}

func TestBuildLearnerPacketFallsBackToExperienceProject(t *testing.T) {
	service := &Service{}
	packet := service.BuildLearnerPacketAt(
		&domain.Experience{Scope: domain.ExperienceScope{Project: "NusaShell"}},
		LearningSource{},
		"periodic",
		0,
	)
	if !strings.Contains(packet, "project_label: NusaShell") {
		t.Fatalf("learner packet missing experience project label:\n%s", packet)
	}
}

func TestOpsFromLearnerConsolidateProjectScopeNeedsSourceProject(t *testing.T) {
	stage := &learnerConsolidate{
		Action: "write",
		Entry: &learnerEntry{
			Type:     domain.MemoryTypeFact,
			Content:  "project-only fact",
			Evidence: "source conversation",
			Scope:    domain.MemoryScopeProject,
		},
	}
	if ops := opsFromLearnerConsolidate(stage, "job", "exp", ""); len(ops) != 0 {
		t.Fatalf("project-scoped entry without source project must not write: %+v", ops)
	}
}

func TestOpsFromLearnerConsolidateRejectsInventedProjectLabel(t *testing.T) {
	stage := &learnerConsolidate{
		Action: "write",
		Entry: &learnerEntry{
			Type:     domain.MemoryTypeFact,
			Content:  "project-only fact",
			Evidence: "source conversation",
			Scope:    domain.MemoryScopeProject,
			Project:  "OtherProject",
		},
	}
	if ops := opsFromLearnerConsolidate(stage, "job", "exp", "NusaShell"); len(ops) != 0 {
		t.Fatalf("mismatched project label must not write: %+v", ops)
	}
}

func TestOpsFromLearnerConsolidateHonorsExplicitUserScope(t *testing.T) {
	stage := &learnerConsolidate{
		Action: "write",
		Entry: &learnerEntry{
			Type:     domain.MemoryTypePreference,
			Content:  "prefers concise answers",
			Evidence: "standing preference",
			Scope:    domain.MemoryScopeUser,
		},
	}
	ops := opsFromLearnerConsolidate(stage, "job", "exp", "NusaShell")
	if len(ops) != 1 {
		t.Fatalf("ops=%d, want 1", len(ops))
	}
	if got := PayloadString(ops[0].Payload, "scope"); got != domain.MemoryScopeUser {
		t.Fatalf("scope=%q, want %q", got, domain.MemoryScopeUser)
	}
	if got := PayloadString(ops[0].Payload, "project"); got != "" {
		t.Fatalf("user-scoped entry must not carry project=%q", got)
	}
}

func TestTeachingOpsUsesSourceProjectScope(t *testing.T) {
	exp := &domain.Experience{
		ID:    "exp",
		Scope: domain.ExperienceScope{Project: "NusaShell"},
		Corrections: []domain.UserCorrection{{
			Type:    "fact",
			Desired: "use the adapter boundary",
		}},
	}
	ops := TeachingOps(exp, "job")
	if len(ops) != 1 {
		t.Fatalf("ops=%d, want 1", len(ops))
	}
	if got := PayloadString(ops[0].Payload, "scope"); got != domain.MemoryScopeProject {
		t.Fatalf("scope=%q, want %q", got, domain.MemoryScopeProject)
	}
	if got := PayloadString(ops[0].Payload, "project"); got != "NusaShell" {
		t.Fatalf("project=%q, want NusaShell", got)
	}
}

func TestFindMemoryMatchKeepsUserAndProjectScopesSeparate(t *testing.T) {
	store := &scopeRecordStore{records: []*domain.MemoryRecord{
		{ID: "user", Type: domain.MemoryTypeFact, Body: "same fact", Scope: domain.MemoryScope{Level: domain.MemoryScopeUser}, Status: domain.MemoryStatusLearned},
		{ID: "project", Type: domain.MemoryTypeFact, Body: "same fact", Scope: domain.MemoryScope{Level: domain.MemoryScopeProject, Project: "NusaShell"}, Status: domain.MemoryStatusLearned},
	}}
	if got, _ := findMemoryMatch(store, "same fact", domain.MemoryTypeFact, domain.MemoryScopeUser, ""); got != "user" {
		t.Fatalf("user match=%q, want user", got)
	}
	if got, _ := findMemoryMatch(store, "same fact", domain.MemoryTypeFact, domain.MemoryScopeProject, "NusaShell"); got != "project" {
		t.Fatalf("project match=%q, want project", got)
	}
}
