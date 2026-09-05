package domain

import (
	"testing"
	"time"
)

func TestMemoryRecordRetire(t *testing.T) {
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	m := &MemoryRecord{ID: "mem_1", Status: MemoryStatusLearned, Body: "prefers Go"}
	m.Retire(now)
	if m.Status != MemoryStatusRetired {
		t.Fatalf("status=%s", m.Status)
	}
	if m.Retrievable() {
		t.Fatal("retired records must not be retrievable")
	}
}

func TestNormalizeMemoryRecordDefaults(t *testing.T) {
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	m := &MemoryRecord{Body: "uses Rust in this repo", SupportingExperiences: []string{"exp_1"}}
	NormalizeMemoryRecord(m, now)
	if m.Type != MemoryTypeBelief {
		t.Fatalf("type=%s", m.Type)
	}
	if m.Status != MemoryStatusCandidate {
		t.Fatalf("status=%s", m.Status)
	}
	if m.Scope.Level != MemoryScopeUser {
		t.Fatalf("scope=%s", m.Scope.Level)
	}
	if m.EvidenceCount != 1 {
		t.Fatalf("evidence=%d", m.EvidenceCount)
	}
}

func TestPolicyRankOrder(t *testing.T) {
	if PolicyRank("explicit_local") >= PolicyRank("stable_preference") {
		t.Fatal("explicit local must outrank stable preference")
	}
	if PolicyRank("project_convention") >= PolicyRank("inferred_preference") {
		t.Fatal("project convention must outrank inferred preference")
	}
}
