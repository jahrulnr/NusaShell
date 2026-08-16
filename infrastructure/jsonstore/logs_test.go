package jsonstore

import (
	"path/filepath"
	"testing"

	"nusashell/domain"
)

func TestListLogsChronologicalOldestFirst(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Append in chronological order with ascending timestamps.
	for i, msg := range []string{"first", "second", "third"} {
		s.AppendLog(&domain.LogEntry{
			ID:      domain.NewID("log"),
			Level:   "info",
			Source:  "test",
			Message: msg,
			// Time left zero — ordering is by file position (append order),
			// which mirrors chronological append. The UI relies on the
			// returned slice order, not on sorting by Time in the browser.
		})
		_ = i
	}

	got := s.ListLogs("", 100)
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}
	// Oldest first so the UI can append newest at the bottom and
	// "Follow" (scroll to bottom) shows the latest entry.
	if got[0].Message != "first" || got[2].Message != "third" {
		t.Fatalf("expected [first, second, third] (oldest first), got [%s, %s, %s]",
			got[0].Message, got[1].Message, got[2].Message)
	}
}

func TestListLogsLevelFilterKeepsChronologicalOrder(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	msgs := []struct {
		level, msg string
	}{
		{"info", "i1"},
		{"warn", "w1"},
		{"info", "i2"},
		{"error", "e1"},
		{"info", "i3"},
	}
	for _, m := range msgs {
		s.AppendLog(&domain.LogEntry{ID: domain.NewID("log"), Level: m.level, Source: "test", Message: m.msg})
	}

	got := s.ListLogs("info", 100)
	if len(got) != 3 {
		t.Fatalf("expected 3 info entries, got %d", len(got))
	}
	if got[0].Message != "i1" || got[1].Message != "i2" || got[2].Message != "i3" {
		t.Fatalf("expected [i1, i2, i3] chronological, got [%s, %s, %s]",
			got[0].Message, got[1].Message, got[2].Message)
	}
}

func TestListLogsLimitReturnsMostRecentInChronologicalOrder(t *testing.T) {
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "sub"))
	if err != nil {
		t.Fatal(err)
	}

	for _, msg := range []string{"a", "b", "c", "d", "e"} {
		s.AppendLog(&domain.LogEntry{ID: domain.NewID("log"), Level: "info", Source: "test", Message: msg})
	}

	// limit=3 must return the 3 most recent (c, d, e) in chronological order,
	// not the 3 oldest and not newest-first.
	got := s.ListLogs("", 3)
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(got))
	}
	if got[0].Message != "c" || got[1].Message != "d" || got[2].Message != "e" {
		t.Fatalf("expected [c, d, e], got [%s, %s, %s]",
			got[0].Message, got[1].Message, got[2].Message)
	}
}
