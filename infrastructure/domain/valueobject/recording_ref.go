package valueobject

import (
	"strings"
	"unicode/utf8"
)

// RecordingRef names a browser recording a user message carries. Summary
// is read when the message is sent — a finished recording's summary text,
// a live one's meta brief, or "" when none can be read.
type RecordingRef struct {
	ID      string
	Status  string
	Summary string
}

// recordingNoteMaxLines and recordingNoteMaxBytes bound the summary the
// model replay sees — a full summary is for the owner, not the context.
const (
	recordingNoteMaxLines = 80
	recordingNoteMaxBytes = 12 << 10
)

// RecordingNote is the model-replay form of a carried recording: the
// recording's id and status, then its summary (or why there is none),
// bounded so a long summary cannot crowd out the message itself.
func RecordingNote(ref *RecordingRef) string {
	if ref == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("<recording-context>\n")
	b.WriteString("The user attached a recording of this thread's browser tab. ")
	b.WriteString("This is context, not user text.\n")
	b.WriteString("Recording " + ref.ID)
	if ref.Status != "" {
		b.WriteString(" · status " + ref.Status)
	}
	b.WriteString("\n")
	summary := clipText(ref.Summary, recordingNoteMaxLines, recordingNoteMaxBytes)
	if summary == "" {
		if ref.Status == "recording" || ref.Status == "paused" {
			b.WriteString("The recording is still live; its summary is written when it stops. ")
		} else {
			b.WriteString("No summary could be read for this recording. ")
		}
		b.WriteString("Inspect it with the browser_get_* tools — browser_get_timeline pages its events.\n")
	} else {
		b.WriteString("Summary:\n" + summary + "\n")
		b.WriteString("For more, use the browser_get_* tools — browser_get_timeline pages the recording's events.\n")
	}
	b.WriteString("</recording-context>")
	return b.String()
}

// clipText bounds s to maxLines lines and maxBytes bytes, keeping the
// head; either cut marks the text with a trailing ellipsis.
func clipText(s string, maxLines, maxBytes int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if lines := strings.SplitN(s, "\n", maxLines+1); len(lines) > maxLines {
		s = strings.Join(lines[:maxLines], "\n")
	}
	if len(s) > maxBytes {
		s = s[:maxBytes]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
		s += "…"
	}
	return s
}
