package config

// MediaConfig gates the media bridge. Assigning a vision or transcription
// model says who would read a file; this switch says whether files may be
// sent at all — two decisions the owner makes separately. It is off by
// default, and it covers both readings a blind model can ask for: an image
// description and an audio transcript.
type MediaConfig struct {
	// ReadingConsent lets read_media send a file the turn's model cannot take
	// to the model that reads it — models.vision for images,
	// models.transcription for audio. Off: the tool refuses before it reads
	// anything.
	ReadingConsent bool `json:"reading_consent"`
}

// Normalize leaves the switch as written; it has no default but false.
func (c *MediaConfig) Normalize() {
}
