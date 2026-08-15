package application

import (
	"context"
	"testing"

	"nusashell/domain"
)

func TestReviewTurnWritesNewMemory(t *testing.T) {
	mem := &fakeMemoryStore{}
	r := NewLearningReviewer(mem, nil)
	r.ReviewTurn(context.Background(), "remember that the deploy command is make deploy", "")
	entries := mem.List()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Source != "user" {
		t.Errorf("source = %q, want user", entries[0].Source)
	}
}

func TestReviewTurnDeduplicatesExact(t *testing.T) {
	mem := &fakeMemoryStore{entries: []*domain.MemoryEntry{
		{ID: "mem_1", Content: "Decision: use postgres for the database", Source: "user"},
	}}
	r := NewLearningReviewer(mem, nil)
	r.ReviewTurn(context.Background(), "let's use postgres for the database", "")
	if len(mem.List()) != 1 {
		t.Fatalf("expected 1 entry (deduplicated), got %d", len(mem.List()))
	}
}

func TestReviewTurnDeduplicatesFuzzy(t *testing.T) {
	mem := &fakeMemoryStore{entries: []*domain.MemoryEntry{
		{ID: "mem_1", Content: "Preference: tabs over spaces for formatting", Source: "user"},
	}}
	r := NewLearningReviewer(mem, nil)
	r.ReviewTurn(context.Background(), "I prefer tabs over spaces for formatting code", "")
	if len(mem.List()) != 1 {
		t.Fatalf("expected 1 entry (fuzzy dedup), got %d", len(mem.List()))
	}
}

func TestReviewTurnRejectsLowWeight(t *testing.T) {
	mem := &fakeMemoryStore{}
	r := NewLearningReviewer(mem, nil)
	// "hello world" → no signals extracted → nothing written
	r.ReviewTurn(context.Background(), "hello world", "hi there")
	if len(mem.List()) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(mem.List()))
	}
}

func TestReviewTurnRespectsCapacity(t *testing.T) {
	// Pre-fill to capacity.
	entries := make([]*domain.MemoryEntry, MaxMemoryEntries)
	for i := range entries {
		entries[i] = &domain.MemoryEntry{ID: "mem_" + string(rune('a'+i%26)), Content: "existing"}
	}
	mem := &fakeMemoryStore{entries: entries}
	r := NewLearningReviewer(mem, nil)
	r.ReviewTurn(context.Background(), "remember that this is a new fact", "")
	// Should not write beyond capacity.
	if len(mem.List()) > MaxMemoryEntries {
		t.Fatalf("memory exceeded capacity: %d", len(mem.List()))
	}
}

func TestShouldWriteGate(t *testing.T) {
	tests := []struct {
		obs  ExtractedObservation
		want bool
	}{
		{ExtractedObservation{Weight: 0.2, Content: "too weak"}, false},
		{ExtractedObservation{Weight: 0.8, Content: "short"}, false},
		{ExtractedObservation{Weight: 0.8, Content: "this is long enough"}, true},
	}
	for _, tc := range tests {
		got := shouldWrite(tc.obs)
		if got != tc.want {
			t.Errorf("shouldWrite(%+v) = %v, want %v", tc.obs, got, tc.want)
		}
	}
}

func TestIsFuzzyMatch(t *testing.T) {
	if !isFuzzyMatch("prefer tabs over spaces", "prefer tabs over spaces for code") {
		t.Error("expected fuzzy match for similar strings")
	}
	if isFuzzyMatch("completely different content here", "totally unrelated text now") {
		t.Error("expected no fuzzy match for dissimilar strings")
	}
}
