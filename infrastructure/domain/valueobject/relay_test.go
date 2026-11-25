package valueobject

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The head names the sender, and the notice that travels with it is the only
// place a model is told how to answer.
func TestRelayHeadNamesTheSender(t *testing.T) {
	cases := []struct {
		name  string
		relay Relay
		want  string
	}{
		{"label and title", NewRelay("c_1", "otter", "Planner"), "[From thread @otter Planner (c_1)]"},
		{"label only", NewRelay("c_1", "otter", ""), "[From thread @otter (c_1)]"},
		{"title only", NewRelay("c_1", "", "Planner"), "[From thread Planner (c_1)]"},
		{"id only", NewRelay("c_1", "", ""), "[From thread c_1 (c_1)]"},
		{"nothing to name", Relay{}, "[From another thread]"},
	}
	for _, tc := range cases {
		head := tc.relay.Head()
		first, rest, _ := strings.Cut(head, "\n")
		if first != tc.want {
			t.Fatalf("%s: first line = %q, want %q", tc.name, first, tc.want)
		}
		// The reply notice rides the head, and only a named sender can be
		// answered.
		if hasNotice := strings.Contains(rest, "Your answer here does not reach that thread"); hasNotice != (tc.relay.ID != "") {
			t.Fatalf("%s: notice = %v for id %q", tc.name, hasNotice, tc.relay.ID)
		}
		if !strings.HasSuffix(head, "\n\n") {
			t.Fatalf("%s: head must end with a blank line: %q", tc.name, head)
		}
	}
}

// A title that was itself a head is normalized once, at write time: one bounded
// line, no nested markers, no extra hop.
func TestNewRelayFoldsANestedHead(t *testing.T) {
	noisy := "[From thread @bear Nested (c_W1ABC)]\n[Your answer here does not reach that thread.]\n\n" +
		"A long prompt " + strings.Repeat("many ", 40) + "lines\nlast line"
	r := NewRelay("c_9", "", noisy)
	if strings.Contains(r.Title, "[From thread") || strings.Contains(r.Title, "[Your answer") || strings.Contains(r.Title, "\n") {
		t.Fatalf("title carries a head: %q", r.Title)
	}
	if utf8.RuneCountInString(r.Title) > relayNameRunes {
		t.Fatalf("title is not bounded: %d runes", utf8.RuneCountInString(r.Title))
	}
	if first, _, _ := strings.Cut(r.Head(), "\n"); strings.Count(first, "[From") != 1 {
		t.Fatalf("head echoes a hop: %q", first)
	}
}
