package jsonstore

import "testing"

func TestBM25_ExactMatch(t *testing.T) {
	docs := []BM25Doc{
		{ID: "a", Text: "git rebase tutorial"},
		{ID: "b", Text: "docker build guide"},
		{ID: "c", Text: "python venv setup"},
	}
	bm25 := NewBM25(docs)
	results := bm25.Search("git rebase", 3)
	if len(results) == 0 {
		t.Fatal("expected results for 'git rebase'")
	}
	if results[0].ID != "a" {
		t.Errorf("expected top result 'a', got '%s'", results[0].ID)
	}
}

func TestBM25_SeparatorTokens(t *testing.T) {
	// Hyphenated/underscore-joined identifiers must match their parts:
	// "read_file" should rank for the query "read file".
	docs := []BM25Doc{
		{ID: "tool", Text: "read_file guide"},
		{ID: "skill", Text: "git-rebase tutorial"},
		{ID: "other", Text: "unrelated content"},
	}
	bm25 := NewBM25(docs)
	results := bm25.Search("read file", 3)
	if len(results) == 0 || results[0].ID != "tool" {
		t.Errorf("expected 'tool' to match 'read file', got %+v", results)
	}
	results = bm25.Search("rebase", 3)
	if len(results) == 0 || results[0].ID != "skill" {
		t.Errorf("expected 'skill' to match 'rebase', got %+v", results)
	}
}

func TestBM25_NoMatch(t *testing.T) {
	docs := []BM25Doc{
		{ID: "a", Text: "git rebase tutorial"},
	}
	bm25 := NewBM25(docs)
	results := bm25.Search("kubernetes deploy", 3)
	if len(results) != 0 {
		t.Errorf("expected no results, got %d", len(results))
	}
}

func TestBM25_Ranking(t *testing.T) {
	docs := []BM25Doc{
		{ID: "rare", Text: "unique keyword appears once"},
		{ID: "frequent", Text: "keyword keyword keyword appears many times"},
		{ID: "irrelevant", Text: "completely different content"},
	}
	bm25 := NewBM25(docs)
	results := bm25.Search("keyword", 3)
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}
	if results[0].ID != "frequent" {
		t.Errorf("expected 'frequent' first, got '%s'", results[0].ID)
	}
	if results[1].ID != "rare" {
		t.Errorf("expected 'rare' second, got '%s'", results[1].ID)
	}
}

func TestBM25_TopK(t *testing.T) {
	docs := []BM25Doc{
		{ID: "a", Text: "alpha beta"},
		{ID: "b", Text: "alpha gamma"},
		{ID: "c", Text: "alpha delta"},
	}
	bm25 := NewBM25(docs)
	results := bm25.Search("alpha", 2)
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}
