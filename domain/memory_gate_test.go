package domain

import "testing"

func TestMemorySimilarity(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want float64
	}{
		{"identical", "use pnpm in this repo", "use pnpm in this repo", 1.0},
		{"punctuation variants", "Untuk integrasi Cursor di 9router, yang dibutuhkan adalah versi IDE", "Untuk integrasi Cursor di 9router: yang dibutuhkan adalah versi IDE dong", 1.0},
		{"case insensitive", "USER PREFERS GO", "user prefers go", 1.0},
		{"partial overlap", "switch this repo to pnpm", "use pnpm for dependencies in this repo", 0.6},
		{"unrelated", "hatch a pet mascot", "audit nusashell growth stores", 0.0},
		{"too short to judge", "geser", "geser kiri", 0.0},
		{"empty", "", "use pnpm", 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MemorySimilarity(tt.a, tt.b)
			if got < tt.want-0.001 || got > tt.want+0.001 {
				t.Fatalf("MemorySimilarity(%q, %q) = %v, want ~%v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestValidDurableMemoryBody(t *testing.T) {
	userTexts := []string{
		"kalo tambah 1 hydration tool lagi? udah ada `file_list` kan ?",
		"sebelum push, perbaiki ini: di tab Experience (frontend) itemnya ascending",
		"I prefer Go for backend work",
	}
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"legit preference", "User prefers Go for backend work because the team is Go-only", true},
		{"question echoed verbatim", "kalo tambah 1 hydration tool lagi? udah ada file_list kan ?", false},
		{"any question is rejected", "Should we use SSE here?", false},
		{"verbatim echo of user goal", "I prefer Go for backend work", false},
		{"task instruction echo", "sebelum push, perbaiki ini: di tab Experience (frontend) itemnya ascending", false},
		{"empty", "", false},
		{"too short", "geser", false},
		{"two tokens", "pakai SSE", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidDurableMemoryBody(tt.body, userTexts); got != tt.want {
				t.Fatalf("ValidDurableMemoryBody(%q) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}

func TestBodyLooksInterrogative(t *testing.T) {
	if !BodyLooksInterrogative("udah ada file_list kan ?") {
		t.Fatal("trailing question mark must be detected")
	}
	if !BodyLooksInterrogative("Kenapa tidak pakai SSE?") {
		t.Fatal("inline question mark must be detected")
	}
	if BodyLooksInterrogative("User prefers SSE for one-way streams") {
		t.Fatal("declarative statement must not look interrogative")
	}
}

func TestUserTextsForExperience(t *testing.T) {
	exp := &Experience{
		Goal: "remember I prefer dark mode",
		Corrections: []UserCorrection{
			{UserSaid: "bukan gitu, pakai file_list", Explicit: true},
			{UserSaid: "", Explicit: true},
		},
	}
	got := UserTextsForExperience(exp)
	if len(got) != 2 {
		t.Fatalf("got %d texts, want 2 (goal + one non-empty correction)", len(got))
	}
	if got[0] != "remember I prefer dark mode" || !containsStrTest(got[1], "file_list") {
		t.Fatalf("texts = %v", got)
	}
}

func containsStrTest(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || (len(sub) > 0 && indexStr(s, sub) >= 0))
}

func indexStr(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
