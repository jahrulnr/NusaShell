package jsonstore

import (
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
