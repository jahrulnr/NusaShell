package valueobject

import (
	"strings"
	"testing"
)

func TestRecordingNote(t *testing.T) {
	note := RecordingNote(&RecordingRef{ID: "rec_1", Status: "completed", Summary: "navigated the form\nsubmitted it"})
	for _, want := range []string{
		"<recording-context>", "rec_1", "completed", "context, not user text",
		"navigated the form", "browser_get_timeline", "</recording-context>",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q:\n%s", want, note)
		}
	}
	if got := RecordingNote(nil); got != "" {
		t.Errorf("nil ref note = %q", got)
	}
}

// A live recording with no summary is told it is still recording and
// where to look; a finished one without a summary says so plainly.
func TestRecordingNoteWithoutSummary(t *testing.T) {
	live := RecordingNote(&RecordingRef{ID: "rec_2", Status: "recording"})
	if !strings.Contains(live, "still live") || !strings.Contains(live, "browser_get_timeline") {
		t.Errorf("live note = %q", live)
	}
	done := RecordingNote(&RecordingRef{ID: "rec_3", Status: "completed"})
	if !strings.Contains(done, "No summary") || strings.Contains(done, "still live") {
		t.Errorf("finished note = %q", done)
	}
}

// The summary is bounded at 80 lines and 12 KiB so a long one cannot
// crowd the message itself.
func TestRecordingNoteBounds(t *testing.T) {
	var lines []string
	for i := 0; i < 200; i++ {
		lines = append(lines, strings.Repeat("x", 200))
	}
	summary := clipText(strings.Join(lines, "\n"), recordingNoteMaxLines, recordingNoteMaxBytes)
	if n := strings.Count(summary, "\n") + 1; n > recordingNoteMaxLines {
		t.Errorf("clip kept %d lines", n)
	}
	if len(summary) > recordingNoteMaxBytes+4 { // "…" may add a rune
		t.Errorf("clip kept %d bytes", len(summary))
	}
	note := RecordingNote(&RecordingRef{ID: "r", Status: "completed", Summary: strings.Join(lines, "\n")})
	if !strings.Contains(note, summary[:100]) {
		t.Error("note lacks the clipped summary head")
	}
}
