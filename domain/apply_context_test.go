package domain

import "testing"

func TestBuildApplyBlockSkipsRetiredAndCaps(t *testing.T) {
	records := []*MemoryRecord{
		{Type: MemoryTypePreference, Body: "prefer Go for backend", Scope: MemoryScope{Level: MemoryScopeDomain, Domain: "backend"}, Status: MemoryStatusLearned},
		{Type: MemoryTypePreference, Body: "use Rust in nusa-web", Scope: MemoryScope{Level: MemoryScopeProject, Project: "nusa-web"}, Status: MemoryStatusLearned},
		{Type: MemoryTypePreference, Body: "secret retired", Status: MemoryStatusRetired},
	}
	got := BuildApplyBlock(records, 400)
	if got == "" {
		t.Fatal("empty apply block")
	}
	if !containsAll(got, "prefer Go", "nusa-web", "Rust") {
		t.Fatalf("block=%s", got)
	}
	if containsAll(got, "secret retired") {
		t.Fatal("retired record leaked into APPLY")
	}
}

func TestBuildApplyBlockEmptyWhenNothingRetrievable(t *testing.T) {
	if got := BuildApplyBlock([]*MemoryRecord{{Status: MemoryStatusRetired, Body: "x"}}, 400); got != "" {
		t.Fatalf("got %q", got)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !containsStr(s, p) {
			return false
		}
	}
	return true
}

func containsStr(s, p string) bool {
	return len(s) >= len(p) && (s == p || len(p) == 0 || (len(s) > 0 && stringIndex(s, p) >= 0))
}

func stringIndex(s, p string) int {
	for i := 0; i+len(p) <= len(s); i++ {
		if s[i:i+len(p)] == p {
			return i
		}
	}
	return -1
}
