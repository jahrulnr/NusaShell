package docs

import (
	"testing"
)

func TestReadAcceptsMdSuffixAlias(t *testing.T) {
	s, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := s.Read("ci")
	if err != nil {
		t.Fatalf("canonical read failed: %v", err)
	}
	aliased, err := s.Read("ci.md")
	if err != nil {
		t.Fatalf("aliased read failed: %v", err)
	}
	if canonical.ID != aliased.ID {
		t.Fatalf("alias resolved to %q, want %q", aliased.ID, canonical.ID)
	}
	if canonical.Content != aliased.Content {
		t.Fatal("alias content differs from canonical content")
	}
}

func TestReadTrimsWhitespace(t *testing.T) {
	s, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read("  ci  "); err != nil {
		t.Fatalf("whitespace-trimmed read failed: %v", err)
	}
}

func TestSearchRanksByRelevance(t *testing.T) {
	s := &Source{docs: []docEntry{
		{id: "a", title: "A", path: "p", content: "alpha alpha alpha beta"},
		{id: "b", title: "B", path: "p", content: "alpha gamma"},
	}}
	// Only doc a contains "beta".
	hits := s.Search("beta", 5)
	if len(hits) != 1 || hits[0].ID != "a" {
		t.Fatalf("beta hits = %+v, want only a", hits)
	}
	// Both contain "alpha"; BM25 ranks the denser match first.
	hits = s.Search("alpha", 5)
	if len(hits) != 2 || hits[0].ID != "a" || hits[1].ID != "b" {
		t.Fatalf("alpha hits = %+v, want a then b", hits)
	}
}

func TestArtifactsDocExists(t *testing.T) {
	s, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := s.Read("artifacts")
	if err != nil {
		t.Fatalf("artifacts doc not found: %v", err)
	}
	if doc.Title == "" {
		t.Error("artifacts doc has empty title")
	}
}
