package valueobject

import "strings"

// UserInput is one user submission — send, queue, or in-flight steer —
// as a single unit: text plus the selections, uploads, live-page marks,
// and browser recording it carries. The three commands differ only in
// when the input runs; the payload is the same (a steer rejects a
// recording — it belongs to a new message).
type UserInput struct {
	Text        string
	References  []Reference
	Attachments []Attachment
	Annotations []Annotation
	Recording   *RecordingRef
}

// Empty reports whether the input carries nothing — no text, uploads,
// marks, or recording. An empty submission would mint an empty room and
// burn a turn on nothing, so transports reject it.
func (u UserInput) Empty() bool {
	return strings.TrimSpace(u.Text) == "" && len(u.Attachments) == 0 && len(u.Annotations) == 0 && u.Recording == nil
}
