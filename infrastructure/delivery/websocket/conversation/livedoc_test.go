package wsconversation

import (
	"strings"
	"testing"
)

// A retry replaces the attempt that failed: the document a reconnecting tab
// receives must hold the durable steps and nothing from the withdrawn
// attempt — text, tool calls, and their index slots alike.
func TestLiveDocTruncateTailWithdrawsFailedAttempt(t *testing.T) {
	d := newLiveDoc()
	d.text("text", "step one")
	d.markCommitted()
	d.text("text", "failed attempt")
	d.push(&livePart{Kind: "tool", CallID: "call_1", Status: "requested"})

	d.truncateTail()

	if len(d.parts) != 1 || d.parts[0].Text != "step one" {
		t.Fatalf("parts after truncate = %+v, want the committed step only", d.parts)
	}
	if _, ok := d.tools["call_1"]; ok {
		t.Fatal("a withdrawn call kept its index slot")
	}
	if d.dropped != 0 {
		t.Fatalf("dropped = %d, want 0 — withdrawn output is not partial recovery", d.dropped)
	}
	if want := partBytes(d.parts[0]); d.bytes != want {
		t.Fatalf("bytes = %d, want %d", d.bytes, want)
	}

	// The retried attempt's output is what the document records next.
	d.text("text", "the answer")
	if len(d.parts) != 2 || d.parts[1].Text != "the answer" {
		t.Fatalf("parts after the retry = %+v, want step one then the answer", d.parts)
	}
	if d.dropped != 0 {
		t.Fatalf("dropped = %d after the retry, want 0", d.dropped)
	}
}

// A steer line is persisted before it is announced, so a retry later in the
// turn keeps it: only the attempt's own output is withdrawn.
func TestLiveDocTruncateTailKeepsPersistedSteer(t *testing.T) {
	d := newLiveDoc()
	d.text("text", "step one")
	d.markCommitted()
	d.steer("req-1", "steer me", nil)
	d.markCommitted()
	d.text("text", "failed attempt")

	d.truncateTail()

	if len(d.parts) != 2 {
		t.Fatalf("parts after truncate = %+v, want the step and the steer", d.parts)
	}
	if p := d.parts[1]; p.Kind != "steer" || p.Text != "steer me" || p.InputID != "req-1" {
		t.Fatalf("steer part = %+v, want it kept", p)
	}
}

// Bound trimming removes from the front, so the durable boundary has to move
// with the slice — otherwise a retry would keep speculative output that the
// document no longer has a boundary before.
func TestLiveDocTruncateTailTracksFrontTrim(t *testing.T) {
	d := newLiveDoc()
	d.text("text", "step one")
	d.markCommitted()
	d.push(&livePart{Kind: "text", Text: strings.Repeat("x", liveDocMaxBytes+1024)})
	if len(d.parts) != 1 {
		t.Fatalf("parts = %d, want the committed step trimmed away under the bound", len(d.parts))
	}

	d.truncateTail()

	if len(d.parts) != 0 || d.bytes != 0 {
		t.Fatalf("parts after truncate = %+v (%d bytes), want none — all of it was speculative", d.parts, d.bytes)
	}
}
