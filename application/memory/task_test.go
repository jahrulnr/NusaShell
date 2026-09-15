package memory

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"nusashell/domain"
)

// fakeSearcher is a test double for the Searcher port. It returns a fixed
// list of result IDs, ignoring the query.
type fakeSearcher struct {
	ids    []string
	called bool
}

func (f *fakeSearcher) SearchMemory(_ context.Context, _ string, topK int) ([]MemorySearchResult, error) {
	f.called = true
	if topK <= 0 || topK > len(f.ids) {
		topK = len(f.ids)
	}
	out := make([]MemorySearchResult, topK)
	for i := 0; i < topK; i++ {
		out[i] = MemorySearchResult{ID: f.ids[i], Score: float64(len(f.ids) - i)}
	}
	return out, nil
}

func trivialTestService(records []*domain.MemoryRecord, searcher Searcher) *Service {
	return New(Deps{
		Records:  &fakeRecordStore{items: records},
		Searcher: searcher,
	})
}

// --- Trivial prompt detection ---

func TestIsTrivialPromptGreetings(t *testing.T) {
	cases := []string{
		"hi", "hello", "hey", "hai", "halo",
		"thanks", "thank you", "thx",
		"terima kasih", "makasih",
		"sip", "ok", "oke", "okay",
	}
	for _, c := range cases {
		if !IsTrivialPrompt(c) {
			t.Errorf("IsTrivialPrompt(%q) = false, want true", c)
		}
	}
}

func TestIsTrivialPromptShortAck(t *testing.T) {
	// <=2 effective words (minLen 3) are trivial.
	if !IsTrivialPrompt("ya oke") {
		t.Errorf("IsTrivialPrompt(%q) = false, want true (2 effective words)", "ya oke")
	}
	if !IsTrivialPrompt("sip done") {
		t.Errorf("IsTrivialPrompt(%q) = false, want true (2 effective words)", "sip done")
	}
}

func TestIsTrivialPromptNonTrivial(t *testing.T) {
	cases := []string{
		"tolong buatkan saya tests",
		"fix the memory announcement queue",
		"can you review this pull request",
	}
	for _, c := range cases {
		if IsTrivialPrompt(c) {
			t.Errorf("IsTrivialPrompt(%q) = true, want false (real request)", c)
		}
	}
}

func TestIsTrivialPromptEmpty(t *testing.T) {
	if !IsTrivialPrompt("") {
		t.Errorf("IsTrivialPrompt(\"\") = false, want true")
	}
}

// --- MaybeAnnounceTaskMemory skips trivial prompt ---

func TestMaybeAnnounceTaskMemorySkipsTrivialPrompt(t *testing.T) {
	now := time.Now()
	conv := &domain.Conversation{
		ID:    "c1",
		Title: "Fix memory announcement queue",
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "hi"},
			{Role: domain.RoleAssistant, Content: "hello there"},
		},
	}
	recs := []*domain.MemoryRecord{{
		ID:            "rec-1",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          "Fix memory announcement queue nusashell",
		LastConfirmed: now,
	}}
	searcher := &fakeSearcher{ids: []string{"rec-1"}}
	svc := trivialTestService(recs, searcher)

	svc.MaybeAnnounceTaskMemory(conv, func() time.Time { return now })

	if searcher.called {
		t.Fatal("searcher must not be called when last user prompt is trivial")
	}
	if len(conv.LastAnnouncedRecords) != 0 {
		t.Fatalf("LastAnnouncedRecords = %+v, want empty (trivial prompt skipped)", conv.LastAnnouncedRecords)
	}
}

func TestMaybeAnnounceTaskMemoryDoesNotSkipNonTrivialPrompt(t *testing.T) {
	now := time.Now()
	conv := &domain.Conversation{
		ID:    "c1",
		Title: "Fix memory announcement queue",
		Messages: []domain.Message{
			{Role: domain.RoleUser, Content: "fix the memory announcement queue please"},
			{Role: domain.RoleAssistant, Content: "done"},
		},
	}
	recs := []*domain.MemoryRecord{{
		ID:            "rec-1",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          "Fix memory announcement queue nusashell",
		LastConfirmed: now,
	}}
	searcher := &fakeSearcher{ids: []string{"rec-1"}}
	svc := trivialTestService(recs, searcher)

	svc.MaybeAnnounceTaskMemory(conv, func() time.Time { return now })

	if !searcher.called {
		t.Fatal("searcher must be called when last user prompt is non-trivial")
	}
	if len(conv.LastAnnouncedRecords) != 1 || conv.LastAnnouncedRecords[0].ID != "rec-1" {
		t.Fatalf("LastAnnouncedRecords = %+v, want [rec-1]", conv.LastAnnouncedRecords)
	}
}

func TestMaybeAnnounceTaskMemoryNoUserMessageDoesNotSkip(t *testing.T) {
	now := time.Now()
	// No user messages at all — must NOT skip (preserves existing behavior
	// for tests and conversations without messages).
	conv := &domain.Conversation{
		ID:    "c1",
		Title: "Fix memory announcement queue",
	}
	recs := []*domain.MemoryRecord{{
		ID:            "rec-1",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          "Fix memory announcement queue nusashell",
		LastConfirmed: now,
	}}
	searcher := &fakeSearcher{ids: []string{"rec-1"}}
	svc := trivialTestService(recs, searcher)

	svc.MaybeAnnounceTaskMemory(conv, func() time.Time { return now })

	if !searcher.called {
		t.Fatal("searcher must be called when there is no user message")
	}
	if len(conv.LastAnnouncedRecords) != 1 {
		t.Fatalf("LastAnnouncedRecords = %+v, want [rec-1]", conv.LastAnnouncedRecords)
	}
}

// --- Searcher-based selection ---

func TestMaybeAnnounceTaskMemoryUsesSearcherRanking(t *testing.T) {
	now := time.Now()
	conv := &domain.Conversation{
		ID:    "c1",
		Title: "docker compose deployment",
	}
	// rec-strong is highly relevant; rec-weak is only tangentially related.
	recs := []*domain.MemoryRecord{
		{
			ID:            "rec-weak",
			Type:          domain.MemoryTypeFact,
			Status:        domain.MemoryStatusLearned,
			Body:          "docker is a container tool",
			LastConfirmed: now,
		},
		{
			ID:            "rec-strong",
			Type:          domain.MemoryTypeFact,
			Status:        domain.MemoryStatusLearned,
			Body:          "docker compose deployment for production services",
			LastConfirmed: now,
		},
	}
	// Searcher returns only rec-strong (BM25 ranked it above rec-weak).
	searcher := &fakeSearcher{ids: []string{"rec-strong"}}
	svc := trivialTestService(recs, searcher)

	svc.MaybeAnnounceTaskMemory(conv, func() time.Time { return now })

	if len(conv.LastAnnouncedRecords) != 1 || conv.LastAnnouncedRecords[0].ID != "rec-strong" {
		t.Fatalf("LastAnnouncedRecords = %+v, want [rec-strong] (searcher-selected)", conv.LastAnnouncedRecords)
	}
}

func TestMaybeAnnounceTaskMemoryIgnoresRecordNotReturnedBySearcher(t *testing.T) {
	now := time.Now()
	conv := &domain.Conversation{
		ID:    "c1",
		Title: "docker compose",
	}
	recs := []*domain.MemoryRecord{
		{
			ID:            "rec-shared-word",
			Type:          domain.MemoryTypeFact,
			Status:        domain.MemoryStatusLearned,
			Body:          "docker compose is great",
			LastConfirmed: now,
		},
		{
			ID:            "rec-unrelated",
			Type:          domain.MemoryTypeFact,
			Status:        domain.MemoryStatusLearned,
			Body:          "completely different topic here",
			LastConfirmed: now,
		},
	}
	// Searcher returns only rec-shared-word, NOT rec-unrelated (even though
	// rec-unrelated is retrievable and recent — the old word-share loop
	// might have picked it up if it shared a word; the searcher must not).
	searcher := &fakeSearcher{ids: []string{"rec-shared-word"}}
	svc := trivialTestService(recs, searcher)

	svc.MaybeAnnounceTaskMemory(conv, func() time.Time { return now })

	if len(conv.LastAnnouncedRecords) != 1 || conv.LastAnnouncedRecords[0].ID != "rec-shared-word" {
		t.Fatalf("LastAnnouncedRecords = %+v, want [rec-shared-word]", conv.LastAnnouncedRecords)
	}
}

func TestMaybeAnnounceTaskMemorySearcherPostFiltersRecency(t *testing.T) {
	now := time.Now()
	conv := &domain.Conversation{
		ID:    "c1",
		Title: "docker compose",
	}
	recs := []*domain.MemoryRecord{
		{
			ID:            "rec-old",
			Type:          domain.MemoryTypeFact,
			Status:        domain.MemoryStatusLearned,
			Body:          "docker compose old config",
			LastConfirmed: now.Add(-200 * time.Hour), // beyond 72h
		},
		{
			ID:            "rec-fresh",
			Type:          domain.MemoryTypeFact,
			Status:        domain.MemoryStatusLearned,
			Body:          "docker compose fresh config",
			LastConfirmed: now,
		},
	}
	// Searcher returns both, but rec-old is beyond the 72h recency window.
	searcher := &fakeSearcher{ids: []string{"rec-old", "rec-fresh"}}
	svc := trivialTestService(recs, searcher)

	svc.MaybeAnnounceTaskMemory(conv, func() time.Time { return now })

	if len(conv.LastAnnouncedRecords) != 1 || conv.LastAnnouncedRecords[0].ID != "rec-fresh" {
		t.Fatalf("LastAnnouncedRecords = %+v, want [rec-fresh] (rec-old filtered by 72h)", conv.LastAnnouncedRecords)
	}
}

func TestMaybeAnnounceTaskMemorySearcherPostFiltersDedup(t *testing.T) {
	now := time.Now()
	conv := &domain.Conversation{
		ID:                   "c1",
		Title:                "docker compose",
		LastAnnouncedRecords: domain.AnnouncedRecords{{ID: "rec-already"}},
	}
	recs := []*domain.MemoryRecord{
		{
			ID:            "rec-already",
			Type:          domain.MemoryTypeFact,
			Status:        domain.MemoryStatusLearned,
			Body:          "docker compose already announced",
			LastConfirmed: now,
		},
		{
			ID:            "rec-new",
			Type:          domain.MemoryTypeFact,
			Status:        domain.MemoryStatusLearned,
			Body:          "docker compose new finding",
			LastConfirmed: now,
		},
	}
	searcher := &fakeSearcher{ids: []string{"rec-already", "rec-new"}}
	svc := trivialTestService(recs, searcher)

	svc.MaybeAnnounceTaskMemory(conv, func() time.Time { return now })

	if len(conv.LastAnnouncedRecords) != 2 {
		t.Fatalf("LastAnnouncedRecords = %+v, want 2 entries", conv.LastAnnouncedRecords)
	}
	if conv.LastAnnouncedRecords[1].ID != "rec-new" {
		t.Fatalf("new record must be appended, got %+v", conv.LastAnnouncedRecords)
	}
}

// --- Fallback: searcher nil, minLen 3 ---

func TestMaybeAnnounceTaskMemoryFallbackMinLen3RejectsShortWord(t *testing.T) {
	now := time.Now()
	conv := &domain.Conversation{
		ID:        "c1",
		Title:     "go go power rangers",
		Workspace: "/w/project",
	}
	// Record shares only "go" (2 chars) with the query. With minLen 1
	// (old behavior) this would match. With minLen 3 it must NOT match.
	recs := []*domain.MemoryRecord{{
		ID:            "rec-short",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          "go is a programming language",
		LastConfirmed: now,
	}}
	svc := trivialTestService(recs, nil) // no searcher → fallback

	svc.MaybeAnnounceTaskMemory(conv, func() time.Time { return now })

	if len(conv.LastAnnouncedRecords) != 0 {
		t.Fatalf("LastAnnouncedRecords = %+v, want empty (short word must not match with minLen 3)", conv.LastAnnouncedRecords)
	}
}

func TestMaybeAnnounceTaskMemoryFallbackMinLen3MatchesLongWord(t *testing.T) {
	now := time.Now()
	conv := &domain.Conversation{
		ID:        "c1",
		Title:     "docker deployment guide",
		Workspace: "/w/project",
	}
	// "docker" (6 chars) is long enough for minLen 3.
	recs := []*domain.MemoryRecord{{
		ID:            "rec-docker",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          "docker deployment for production",
		LastConfirmed: now,
	}}
	svc := trivialTestService(recs, nil) // no searcher → fallback

	svc.MaybeAnnounceTaskMemory(conv, func() time.Time { return now })

	if len(conv.LastAnnouncedRecords) != 1 || conv.LastAnnouncedRecords[0].ID != "rec-docker" {
		t.Fatalf("LastAnnouncedRecords = %+v, want [rec-docker]", conv.LastAnnouncedRecords)
	}
}

func TestMaybeAnnounceTaskMemoryFallbackRespectsRecency(t *testing.T) {
	now := time.Now()
	conv := &domain.Conversation{
		ID:        "c1",
		Title:     "docker deployment guide",
		Workspace: "/w/project",
	}
	recs := []*domain.MemoryRecord{{
		ID:            "rec-old",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          "docker deployment for production",
		LastConfirmed: now.Add(-200 * time.Hour), // beyond 72h
	}}
	svc := trivialTestService(recs, nil)

	svc.MaybeAnnounceTaskMemory(conv, func() time.Time { return now })

	if len(conv.LastAnnouncedRecords) != 0 {
		t.Fatalf("old record must not be announced in fallback, got %+v", conv.LastAnnouncedRecords)
	}
}

func TestMaybeAnnounceTaskMemoryFallbackRespectsDedup(t *testing.T) {
	now := time.Now()
	conv := &domain.Conversation{
		ID:                   "c1",
		Title:                "docker deployment guide",
		Workspace:            "/w/project",
		LastAnnouncedRecords: domain.AnnouncedRecords{{ID: "rec-docker"}},
	}
	recs := []*domain.MemoryRecord{{
		ID:            "rec-docker",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          "docker deployment for production",
		LastConfirmed: now,
	}}
	svc := trivialTestService(recs, nil)

	svc.MaybeAnnounceTaskMemory(conv, func() time.Time { return now })

	if len(conv.LastAnnouncedRecords) != 1 {
		t.Fatalf("already-announced record must not re-announce, got %+v", conv.LastAnnouncedRecords)
	}
}

// --- Snippet cap ---

func TestMaybeAnnounceTaskMemoryCapsAtThreeHits(t *testing.T) {
	now := time.Now()
	conv := &domain.Conversation{
		ID:    "c1",
		Title: "docker compose",
	}
	recs := []*domain.MemoryRecord{
		{ID: "r1", Type: domain.MemoryTypeFact, Status: domain.MemoryStatusLearned, Body: "docker one", LastConfirmed: now},
		{ID: "r2", Type: domain.MemoryTypeFact, Status: domain.MemoryStatusLearned, Body: "docker two", LastConfirmed: now},
		{ID: "r3", Type: domain.MemoryTypeFact, Status: domain.MemoryStatusLearned, Body: "docker three", LastConfirmed: now},
		{ID: "r4", Type: domain.MemoryTypeFact, Status: domain.MemoryStatusLearned, Body: "docker four", LastConfirmed: now},
	}
	searcher := &fakeSearcher{ids: []string{"r1", "r2", "r3", "r4"}}
	svc := trivialTestService(recs, searcher)

	svc.MaybeAnnounceTaskMemory(conv, func() time.Time { return now })

	if len(conv.LastAnnouncedRecords) != 3 {
		t.Fatalf("LastAnnouncedRecords = %+v, want 3 (cap)", conv.LastAnnouncedRecords)
	}
}

// Helper to verify snippet truncation in announce args.
func TestMaybeAnnounceTaskMemoryTruncatesSnippet(t *testing.T) {
	now := time.Now()
	conv := &domain.Conversation{ID: "c1", Title: "docker compose"}
	longBody := strings.Repeat("docker compose deployment ", 100)
	recs := []*domain.MemoryRecord{{
		ID:            "rec-long",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          longBody,
		LastConfirmed: now,
	}}
	svc := trivialTestService(recs, &fakeSearcher{ids: []string{"rec-long"}})

	svc.MaybeAnnounceTaskMemory(conv, func() time.Time { return now })

	if len(conv.PendingAnnouncements) != 1 {
		t.Fatalf("PendingAnnouncements = %+v, want 1", conv.PendingAnnouncements)
	}
	args := conv.PendingAnnouncements[0].Args
	if !strings.Contains(args, "rec-long") {
		t.Fatalf("args must contain record id, got %s", args)
	}
	// Snippet is capped at taskMemoryHitChars (1000 runes) + ellipsis.
	// The content field in JSON should not exceed ~1000 runes of body.
	if !strings.Contains(args, "…") {
		t.Fatalf("args must contain truncation marker, got %s", args)
	}
}

// announceHitsContent decodes a task_memory announcement Args payload and
// returns the content of the first hit, failing the test when the payload
// is malformed or has no hits.
func announceHitsContent(t *testing.T, args string) string {
	t.Helper()
	var payload struct {
		Type string `json:"type"`
		Hits []struct {
			ID      string `json:"id"`
			Content string `json:"content"`
		} `json:"hits"`
	}
	if err := json.Unmarshal([]byte(args), &payload); err != nil {
		t.Fatalf("unmarshal args: %v (args=%s)", err, args)
	}
	if len(payload.Hits) == 0 {
		t.Fatalf("args has no hits: %s", args)
	}
	return payload.Hits[0].Content
}

// TestMaybeAnnounceTaskMemorySearcherFullBodyUnderCap proves a body under the
// per-hit rune cap is delivered in full, with no truncation marker.
func TestMaybeAnnounceTaskMemorySearcherFullBodyUnderCap(t *testing.T) {
	now := time.Now()
	conv := &domain.Conversation{ID: "c1", Title: "docker compose"}
	body := strings.Repeat("a", 999) // under the 1000-rune cap
	recs := []*domain.MemoryRecord{{
		ID:            "rec-under",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          body,
		LastConfirmed: now,
	}}
	svc := trivialTestService(recs, &fakeSearcher{ids: []string{"rec-under"}})

	svc.MaybeAnnounceTaskMemory(conv, func() time.Time { return now })

	content := announceHitsContent(t, conv.PendingAnnouncements[0].Args)
	if content != body {
		t.Fatalf("under-cap body must be delivered in full: got %d runes, want %d", len([]rune(content)), len([]rune(body)))
	}
	if strings.Contains(content, "…") {
		t.Fatalf("under-cap body must not be truncated, got %q", content)
	}
}

// TestMaybeAnnounceTaskMemorySearcherFullBodyAtCap proves a body exactly at
// the per-hit rune cap is delivered in full, with no truncation marker.
func TestMaybeAnnounceTaskMemorySearcherFullBodyAtCap(t *testing.T) {
	now := time.Now()
	conv := &domain.Conversation{ID: "c1", Title: "docker compose"}
	body := strings.Repeat("a", 1000) // exactly at the 1000-rune cap
	recs := []*domain.MemoryRecord{{
		ID:            "rec-at",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          body,
		LastConfirmed: now,
	}}
	svc := trivialTestService(recs, &fakeSearcher{ids: []string{"rec-at"}})

	svc.MaybeAnnounceTaskMemory(conv, func() time.Time { return now })

	content := announceHitsContent(t, conv.PendingAnnouncements[0].Args)
	if content != body {
		t.Fatalf("at-cap body must be delivered in full: got %d runes, want %d", len([]rune(content)), len([]rune(body)))
	}
	if strings.Contains(content, "…") {
		t.Fatalf("at-cap body must not be truncated, got %q", content)
	}
}

// TestMaybeAnnounceTaskMemorySearcherTruncatesOverCap proves a body over the
// per-hit rune cap is truncated to exactly the cap runes plus an ellipsis.
func TestMaybeAnnounceTaskMemorySearcherTruncatesOverCap(t *testing.T) {
	now := time.Now()
	conv := &domain.Conversation{ID: "c1", Title: "docker compose"}
	body := strings.Repeat("a", 1500) // over the 1000-rune cap
	recs := []*domain.MemoryRecord{{
		ID:            "rec-over",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          body,
		LastConfirmed: now,
	}}
	svc := trivialTestService(recs, &fakeSearcher{ids: []string{"rec-over"}})

	svc.MaybeAnnounceTaskMemory(conv, func() time.Time { return now })

	content := announceHitsContent(t, conv.PendingAnnouncements[0].Args)
	want := strings.Repeat("a", 1000) + "…"
	if content != want {
		t.Fatalf("over-cap body must be 1000 runes + ellipsis: got %d runes, want %d", len([]rune(content)), len([]rune(want)))
	}
}

// TestMaybeAnnounceTaskMemorySearcherMultibyteTruncation proves multibyte
// input over the cap is truncated on a rune boundary (no split surrogate or
// broken UTF-8) and the result is valid UTF-8.
func TestMaybeAnnounceTaskMemorySearcherMultibyteTruncation(t *testing.T) {
	now := time.Now()
	conv := &domain.Conversation{ID: "c1", Title: "docker compose"}
	// "é" is 2 UTF-8 bytes but 1 rune; 1500 runes over the 1000-rune cap.
	body := strings.Repeat("é", 1500)
	recs := []*domain.MemoryRecord{{
		ID:            "rec-multi",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          body,
		LastConfirmed: now,
	}}
	svc := trivialTestService(recs, &fakeSearcher{ids: []string{"rec-multi"}})

	svc.MaybeAnnounceTaskMemory(conv, func() time.Time { return now })

	content := announceHitsContent(t, conv.PendingAnnouncements[0].Args)
	if !utf8.ValidString(content) {
		t.Fatalf("truncated multibyte content is not valid UTF-8: %q", content)
	}
	want := strings.Repeat("é", 1000) + "…"
	if content != want {
		t.Fatalf("multibyte over-cap must be 1000 runes + ellipsis: got %d runes, want %d", len([]rune(content)), len([]rune(want)))
	}
}

// TestMaybeAnnounceTaskMemoryFallbackTruncatesOverCap proves the fallback
// (no searcher) path truncates a body over the cap to the cap runes plus an
// ellipsis, matching the searcher path's per-hit cap.
func TestMaybeAnnounceTaskMemoryFallbackTruncatesOverCap(t *testing.T) {
	now := time.Now()
	conv := &domain.Conversation{
		ID:        "c1",
		Title:     "docker deployment guide",
		Workspace: "/w/project",
	}
	body := strings.Repeat("docker deployment ", 100) // 1800 runes, over cap
	recs := []*domain.MemoryRecord{{
		ID:            "rec-fb",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          body,
		LastConfirmed: now,
	}}
	svc := trivialTestService(recs, nil) // no searcher → fallback

	svc.MaybeAnnounceTaskMemory(conv, func() time.Time { return now })

	content := announceHitsContent(t, conv.PendingAnnouncements[0].Args)
	wantRunes := 1000
	gotRunes := len([]rune(content))
	// Content is cap runes + ellipsis.
	if gotRunes != wantRunes+1 {
		t.Fatalf("fallback over-cap body must be %d runes + ellipsis: got %d runes", wantRunes, gotRunes)
	}
	if !strings.HasSuffix(content, "…") {
		t.Fatalf("fallback over-cap body must end with ellipsis, got %q", content)
	}
}

// TestMaybeAnnounceTaskMemoryFallbackMultibyteTruncation proves the fallback
// path truncates multibyte input on a rune boundary with valid UTF-8.
func TestMaybeAnnounceTaskMemoryFallbackMultibyteTruncation(t *testing.T) {
	now := time.Now()
	conv := &domain.Conversation{
		ID:        "c1",
		Title:     "docker deployment guide",
		Workspace: "/w/project",
	}
	body := strings.Repeat("é", 1500) // over cap, multibyte
	recs := []*domain.MemoryRecord{{
		ID:            "rec-fbm",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          body,
		Subject:       "docker", // shares a token with the query so fallback selects it
		LastConfirmed: now,
	}}
	svc := trivialTestService(recs, nil) // no searcher → fallback

	svc.MaybeAnnounceTaskMemory(conv, func() time.Time { return now })

	content := announceHitsContent(t, conv.PendingAnnouncements[0].Args)
	if !utf8.ValidString(content) {
		t.Fatalf("fallback truncated multibyte content is not valid UTF-8: %q", content)
	}
	want := strings.Repeat("é", 1000) + "…"
	if content != want {
		t.Fatalf("fallback multibyte over-cap must be 1000 runes + ellipsis: got %d runes, want %d", len([]rune(content)), len([]rune(want)))
	}
}

// TestMaybeAnnounceTaskMemoryQueuesDirectlyOntoConversation proves the scan
// queues the announcement directly onto the conversation's pending queue
// (not via a separate load-modify-save callback), so AddTurnMessages can
// drain it in the same turn — no 1–2 turn lag.
func TestMaybeAnnounceTaskMemoryQueuesDirectlyOntoConversation(t *testing.T) {
	now := time.Now()
	conv := &domain.Conversation{
		ID:    "c1",
		Title: "Fix memory announcement queue",
	}
	recs := []*domain.MemoryRecord{{
		ID:            "rec-1",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          "Fix memory announcement queue nusashell",
		LastConfirmed: now,
	}}
	svc := trivialTestService(recs, &fakeSearcher{ids: []string{"rec-1"}})

	svc.MaybeAnnounceTaskMemory(conv, func() time.Time { return now })

	if len(conv.PendingAnnouncements) != 1 {
		t.Fatalf("PendingAnnouncements = %+v, want 1 task_memory announcement queued directly", conv.PendingAnnouncements)
	}
	pa := conv.PendingAnnouncements[0]
	if pa.Type != TaskMemoryAnnounceType {
		t.Fatalf("type = %q, want %q", pa.Type, TaskMemoryAnnounceType)
	}
	if !strings.Contains(pa.Args, "rec-1") {
		t.Fatalf("args missing record id: %s", pa.Args)
	}
	if !strings.Contains(pa.Message, "task memory") {
		t.Fatalf("message = %q, want task memory text", pa.Message)
	}
}

// --- Change-aware dedup (P2.2) ---

// TestMaybeAnnounceTaskMemoryReAnnouncesAfterReconfirm proves a record that
// was announced and then re-confirmed (LastConfirmed advanced) becomes
// eligible for re-announce — the announcement text says "new or updated".
func TestMaybeAnnounceTaskMemoryReAnnouncesAfterReconfirm(t *testing.T) {
	now := time.Now()
	conv := &domain.Conversation{ID: "c1", Title: "docker compose"}
	recs := []*domain.MemoryRecord{{
		ID:            "rec-1",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          "docker compose deployment",
		LastConfirmed: now,
	}}
	searcher := &fakeSearcher{ids: []string{"rec-1"}}
	svc := trivialTestService(recs, searcher)

	// First announce: queues the announcement and stores the dedup marker
	// with LastConfirmedAt = now.
	svc.MaybeAnnounceTaskMemory(conv, func() time.Time { return now })
	if len(conv.PendingAnnouncements) != 1 {
		t.Fatalf("first announce: pending = %d, want 1", len(conv.PendingAnnouncements))
	}
	// Drain (simulates AddTurnMessages drain).
	conv.DrainPendingAnnouncements()
	if len(conv.LastAnnouncedRecords) != 1 || conv.LastAnnouncedRecords[0].LastConfirmedAt.IsZero() {
		t.Fatalf("dedup marker = %+v, want non-zero LastConfirmedAt", conv.LastAnnouncedRecords)
	}

	// Re-confirm: LastConfirmed advances past the stored timestamp.
	recs[0].LastConfirmed = now.Add(time.Hour)

	// Second announce: must re-announce because LastConfirmed advanced.
	svc.MaybeAnnounceTaskMemory(conv, func() time.Time { return now })
	if len(conv.PendingAnnouncements) != 1 {
		t.Fatalf("after re-confirm: pending = %d, want 1 (re-announced)", len(conv.PendingAnnouncements))
	}
}

// TestMaybeAnnounceTaskMemoryDoesNotReAnnounceUnchanged proves a record that
// was announced and NOT re-confirmed is NOT re-announced — the dedup marker
// blocks it because LastConfirmed has not advanced.
func TestMaybeAnnounceTaskMemoryDoesNotReAnnounceUnchanged(t *testing.T) {
	now := time.Now()
	conv := &domain.Conversation{ID: "c1", Title: "docker compose"}
	recs := []*domain.MemoryRecord{{
		ID:            "rec-1",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          "docker compose deployment",
		LastConfirmed: now,
	}}
	searcher := &fakeSearcher{ids: []string{"rec-1"}}
	svc := trivialTestService(recs, searcher)

	// First announce.
	svc.MaybeAnnounceTaskMemory(conv, func() time.Time { return now })
	conv.DrainPendingAnnouncements()

	// Do NOT change LastConfirmed.

	// Second announce: must NOT re-announce.
	svc.MaybeAnnounceTaskMemory(conv, func() time.Time { return now })
	if len(conv.PendingAnnouncements) != 0 {
		t.Fatalf("unchanged record: pending = %d, want 0 (not re-announced)", len(conv.PendingAnnouncements))
	}
}

// --- Race safety (P2.3) ---

// TestMaybeAnnounceTaskMemoryConcurrentNoLostUpdate proves the P2.3 race
// fix: the old PersistAnnounced path did load-modify-save OUTSIDE the
// announcementLock, so a concurrent PublishAnnouncement or
// DrainAnnouncements (inside the lock) could lose the dedup markers or
// the pending announcement. The new design updates both
// PendingAnnouncements and LastAnnouncedRecords in-place on the same
// conversation object — a single repo.Save() persists both atomically.
//
// This test runs the scan (queue + dedup update) and drain concurrently,
// serialized by a mutex (simulating the turn lock in AddTurnMessages), and
// verifies the dedup markers survive every drain. Run with -race to
// confirm no data race.
func TestMaybeAnnounceTaskMemoryConcurrentNoLostUpdate(t *testing.T) {
	now := time.Now()
	conv := &domain.Conversation{ID: "c1", Title: "docker compose"}
	recs := []*domain.MemoryRecord{{
		ID:            "rec-1",
		Type:          domain.MemoryTypeFact,
		Status:        domain.MemoryStatusLearned,
		Body:          "docker compose deployment",
		LastConfirmed: now,
	}}
	svc := trivialTestService(recs, &fakeSearcher{ids: []string{"rec-1"}})

	// Simulate the turn lock: all mutations of conv are serialized.
	var mu sync.Mutex

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		// Goroutine A: task_memory scan (in-place queue + dedup update).
		go func() {
			defer wg.Done()
			mu.Lock()
			defer mu.Unlock()
			svc.MaybeAnnounceTaskMemory(conv, func() time.Time { return now })
		}()
		// Goroutine B: drain pending announcements (simulates
		// AddTurnMessages drain). The old PersistAnnounced race could
		// lose the dedup markers here; the new in-place design cannot.
		go func() {
			defer wg.Done()
			mu.Lock()
			defer mu.Unlock()
			conv.DrainPendingAnnouncements()
		}()
	}
	wg.Wait()

	// The dedup markers must be present — the scan ran at least once and
	// the in-place update was not lost to a concurrent drain.
	if len(conv.LastAnnouncedRecords) == 0 {
		t.Fatal("LastAnnouncedRecords is empty — dedup markers lost to concurrent drain (race not fixed)")
	}
	if conv.LastAnnouncedRecords[0].ID != "rec-1" {
		t.Fatalf("LastAnnouncedRecords[0].ID = %q, want rec-1", conv.LastAnnouncedRecords[0].ID)
	}
}
