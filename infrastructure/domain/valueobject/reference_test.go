package valueobject

import "testing"

func TestCheckReferences(t *testing.T) {
	text := "café @room and /a.go"
	room, _ := NewReference("conversation", "c_1", "@room", 6, 11)
	file, _ := NewReference("file", "/w/a.go", "/a.go", 16, 21)
	if err := CheckReferences(text, []Reference{room, file}); err != nil {
		t.Fatalf("valid selections rejected: %v", err)
	}
	if err := CheckReferences(text, []Reference{file, room}); err == nil {
		t.Fatal("out-of-order selections accepted")
	}
	shifted, _ := NewReference("file", "/w/a.go", "/a.go", 15, 20)
	if err := CheckReferences(text, []Reference{shifted}); err == nil {
		t.Fatal("a label that does not match its span was accepted")
	}
	past, _ := NewReference("file", "/w/a.go", "/a.go", 20, 25)
	if err := CheckReferences(text, []Reference{past}); err == nil {
		t.Fatal("a span past the text was accepted")
	}
	if _, err := NewReference("thing", "x", "x", 0, 1); err == nil {
		t.Fatal("unknown kind accepted")
	}
	if _, err := NewReference("file", "x", "x", 2, 2); err == nil {
		t.Fatal("empty span accepted")
	}
	if note := ReferenceNote([]Reference{room}); note != "Selected references:\n@room refers to the NusaShell conversation c_1; the conversation tools can read it or message it." {
		t.Fatalf("note = %q", note)
	}
}
