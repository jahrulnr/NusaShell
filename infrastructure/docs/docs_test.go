package docs

import (
	"testing"
)

func TestReadAcceptsMdSuffixAlias(t *testing.T) {
	s, err := New("")
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := s.Read("automation")
	if err != nil {
		t.Fatalf("canonical read failed: %v", err)
	}
	aliased, err := s.Read("automation.md")
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
	if _, err := s.Read("  automation  "); err != nil {
		t.Fatalf("whitespace-trimmed read failed: %v", err)
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
