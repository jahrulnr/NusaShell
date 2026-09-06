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

func TestMemoryQueryMatchesAllTokens(t *testing.T) {
	hay := "file_patch cannot apply a phantom hunk; use git rollback instead"
	if !MemoryQueryMatches(hay, "phantom patch rollback") {
		t.Fatal("multi-word query must match when every token appears, even non-contiguously")
	}
	if MemoryQueryMatches(hay, "phantom patch spaceship") {
		t.Fatal("AND match must reject a missing token")
	}
	if !MemoryQueryMatches(hay, "phantom hunk") {
		t.Fatal("contiguous phrase must still match")
	}
	if MemoryQueryMatches(hay, "golang") {
		t.Fatal("unrelated single token must not match")
	}
	if !MemoryQueryMatches(hay, "") {
		t.Fatal("empty query is unconstrained")
	}
}

func TestMemoryRecordMatchesQueryTokens(t *testing.T) {
	rec := &MemoryRecord{
		ID:     "mem_1",
		Type:   MemoryTypeFact,
		Body:   "file_patch cannot apply a phantom hunk; use git rollback instead",
		Status: MemoryStatusLearned,
	}
	if !rec.Matches(MemorySearchFilter{Query: "phantom patch rollback"}) {
		t.Fatal("retrievable record must match token AND query")
	}
	retired := &MemoryRecord{ID: "mem_r", Body: "phantom patch rollback", Status: MemoryStatusRetired}
	if retired.Matches(MemorySearchFilter{Query: "phantom"}) {
		t.Fatal("retired records must stay out of default search")
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
