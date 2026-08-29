package application

import (
	"strings"
	"testing"

	"nusashell/application/internal/service/tooloutput"
	"nusashell/domain"
)

// TestFilterToolAttachmentsByCapsNotesInsideUntrustedEnvelope verifies that
// capability-filter notes (e.g. "[Audio ... was loaded but cannot be played]")
// are appended to the tool content BEFORE wrapping in the untrusted envelope,
// not after. If notes land outside </untrusted_tool_result>, a malicious tool
// could craft an attachment file path containing injection instructions that
// the model would treat as trusted/system-level content.
func TestFilterToolAttachmentsByCapsNotesInsideUntrustedEnvelope(t *testing.T) {
	atts := []domain.Attachment{
		{Type: "audio", Name: "speech.wav", FilePath: "/tmp/speech.wav"},
	}
	caps := ModelCapabilities{Audio: false} // audio not supported → note appended
	content := "Speech generated and saved."
	filtered, result := filterToolAttachmentsByCaps(atts, content, caps)
	if len(filtered) != 0 {
		t.Fatalf("audio attachment should be stripped, got %d attachments", len(filtered))
	}
	wrapped := tooloutput.WrapToolOutput("generate_media", result)
	// The note must be INSIDE the envelope, not after </untrusted_tool_result>.
	closeIdx := strings.Index(wrapped, "</untrusted_tool_result>")
	noteIdx := strings.Index(wrapped, "[Audio")
	if closeIdx < 0 {
		t.Fatalf("missing closing tag in: %s", wrapped)
	}
	if noteIdx < 0 {
		t.Fatalf("missing audio note in: %s", wrapped)
	}
	if noteIdx > closeIdx {
		t.Fatalf("audio note is OUTSIDE the untrusted envelope (note at %d, close at %d):\n%s", noteIdx, closeIdx, wrapped)
	}
}
