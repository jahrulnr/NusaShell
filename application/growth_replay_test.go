package application

import (
	"testing"

	"nusashell/domain"
)

func TestGrowthReplaySetBKnowAndAct(t *testing.T) {
	records := []*domain.MemoryRecord{
		{
			ID:     "mem_go",
			Type:   domain.MemoryTypePreference,
			Body:   "prefer Go for backend",
			Scope:  domain.MemoryScope{Level: domain.MemoryScopeProject, Project: "nusashell"},
			Status: domain.MemoryStatusLearned,
		},
		{
			ID:     "mem_rust",
			Type:   domain.MemoryTypePreference,
			Body:   "use Rust in nusa-web",
			Scope:  domain.MemoryScope{Level: domain.MemoryScopeProject, Project: "nusa-web"},
			Status: domain.MemoryStatusLearned,
		},
	}
	apply := domain.BuildApplyBlock(records, 400)
	ids := make([]string, 0, len(records))
	for _, rec := range records {
		ids = append(ids, rec.ID)
	}
	goScore := domain.ScoreKnowAct(ids, "mem_go", apply, "prefer Go")
	if goScore.Label() != "know_and_act" {
		t.Fatalf("Go: %s apply=%q", goScore.Label(), apply)
	}
	rustScore := domain.ScoreKnowAct(ids, "mem_rust", apply, "nusa-web")
	if rustScore.Label() != "know_and_act" {
		t.Fatalf("Rust: %s apply=%q", rustScore.Label(), apply)
	}
	if !containsAll(apply, "nusashell", "nusa-web") {
		t.Fatalf("APPLY must keep project scope distinct: %s", apply)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !containsStrReplay(s, p) {
			return false
		}
	}
	return true
}

func containsStrReplay(s, p string) bool {
	return len(s) >= len(p) && (s == p || (len(p) > 0 && indexOf(s, p) >= 0))
}

func indexOf(s, p string) int {
	for i := 0; i+len(p) <= len(s); i++ {
		if s[i:i+len(p)] == p {
			return i
		}
	}
	return -1
}
