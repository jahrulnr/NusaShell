package jsonstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nusashell/domain"
)

func TestGrowthExperienceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	exp := &domain.Experience{
		ID:             "exp_1",
		ConversationID: "conv_1",
		Timestamp:      time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC),
		Goal:           "use Go",
		Corrections:    []domain.UserCorrection{{UserSaid: "Use Go", Explicit: true}},
		Signals:        domain.ExperienceSignals{UserCorrections: 1},
		Outcome:        domain.ExperienceOutcome{Status: "success"},
	}
	store := &Experiences{S: st}
	if err := store.Save(exp); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get("exp_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Goal != "use Go" || got.Signals.UserCorrections != 1 {
		t.Fatalf("%+v", got)
	}
	reopened, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	again := (&Experiences{S: reopened}).ListByConversation("conv_1")
	if len(again) != 1 {
		t.Fatalf("len=%d", len(again))
	}
}

func TestGrowthBulkDeleteRewritesCatalogOnceAndPersists(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	experiences := &Experiences{S: st}
	for _, id := range []string{"exp_keep", "exp_drop_a", "exp_drop_b"} {
		if err := experiences.Save(&domain.Experience{ID: id, Timestamp: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := experiences.DeleteMany([]string{"exp_drop_a", "exp_drop_b"}); err != nil {
		t.Fatal(err)
	}
	if got := experiences.List(); len(got) != 1 || got[0].ID != "exp_keep" {
		t.Fatalf("after bulk delete: %+v", got)
	}
	reopened, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := (&Experiences{S: reopened}).List(); len(got) != 1 || got[0].ID != "exp_keep" {
		t.Fatalf("after reopen: %+v", got)
	}
}

func TestGrowthMemoryRecordUpsertAndRetire(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	recs := &MemoryRecords{S: st}
	m := &domain.MemoryRecord{ID: "mem_1", Type: domain.MemoryTypePreference, Body: "prefer Go", Status: domain.MemoryStatusCandidate}
	if err := recs.Save(m); err != nil {
		t.Fatal(err)
	}
	m.Body = "prefer Go for backend"
	m.Status = domain.MemoryStatusLearned
	if err := recs.Save(m); err != nil {
		t.Fatal(err)
	}
	list := recs.List()
	if len(list) != 1 || list[0].Body != "prefer Go for backend" {
		t.Fatalf("%+v", list)
	}
}

// TestUpsertJSONLInsertAppendsUpdateRewrites pins the file-shape contract of
// the insert fast path: new IDs land as a single appended line while an
// update of an existing ID still rewrites the file — an appended update
// would leave a stale duplicate that loadJSONL would surface twice.
func TestUpsertJSONLInsertAppendsUpdateRewrites(t *testing.T) {
	dir := t.TempDir()
	st, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	experiences := &Experiences{S: st}
	path := filepath.Join(dir, growthExperiencesFile)

	lineCount := func() int {
		t.Helper()
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		return len(transcriptFileLines(t, path))
	}
	readBody := func() string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return string(b)
	}

	if err := experiences.Save(&domain.Experience{ID: "exp_1", Timestamp: time.Now(), Goal: "one"}); err != nil {
		t.Fatal(err)
	}
	if n := lineCount(); n != 1 {
		t.Fatalf("after first insert lines = %d, want 1", n)
	}
	if err := experiences.Save(&domain.Experience{ID: "exp_2", Timestamp: time.Now(), Goal: "two"}); err != nil {
		t.Fatal(err)
	}
	if n := lineCount(); n != 2 {
		t.Fatalf("insert must append one line, got %d lines", n)
	}

	// Update an existing ID: the file must be rewritten, not appended — line
	// count stays the same and the updated content replaces the old line.
	if err := experiences.Save(&domain.Experience{ID: "exp_1", Timestamp: time.Now(), Goal: "one (updated)"}); err != nil {
		t.Fatal(err)
	}
	if n := lineCount(); n != 2 {
		t.Fatalf("update must rewrite (no duplicate IDs), got %d lines", n)
	}
	body := readBody()
	if !strings.Contains(body, "one (updated)") || strings.Count(body, `"id":"exp_1"`) != 1 {
		t.Fatalf("updated file mismatch:\n%s", body)
	}

	// Reload: exactly two rows, in insert order, with the update applied.
	reopened, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := (&Experiences{S: reopened}).List()
	if len(got) != 2 || got[0].ID != "exp_1" || got[0].Goal != "one (updated)" || got[1].ID != "exp_2" {
		t.Fatalf("reload mismatch: %+v", got)
	}
}
