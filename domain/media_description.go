package domain

// Media description naming convention.
//
// When a non-vision/audio/video-capable chat model receives media
// attachments, the modality fallback path describes/transcribes them via
// a fallback model and stores the result as a sibling text attachment
// whose Name is the media prefix + the original attachment name (e.g.
// "vision:cat.png"). This keeps the original media on the message so a
// later switch to a capable model can still see it, while the description
// is delivered to the non-capable model via the text channel.
//
// These constants and helpers are pure domain rules: they only inspect
// Attachment fields and carry no I/O. The fallback execution lives in
// the application layer.

const (
	// MediaDescPrefixVision is the text-attachment name prefix for an
	// image description produced by the vision fallback model.
	MediaDescPrefixVision = "vision:"
	// MediaDescPrefixAudio is the text-attachment name prefix for an
	// audio transcript produced by the audio fallback model.
	MediaDescPrefixAudio = "audio:"
	// MediaDescPrefixVideo is the text-attachment name prefix for a
	// video description produced by the video fallback model.
	MediaDescPrefixVideo = "video:"
)

// HasMediaDescription reports whether atts contains a text attachment
// whose Name is exactly prefix+name (e.g. "vision:cat.png"). Used to
// skip media that has already been described so retries do not re-call
// the fallback model.
func HasMediaDescription(atts []Attachment, prefix, name string) bool {
	want := prefix + name
	for _, att := range atts {
		if att.Type == "text" && att.Name == want {
			return true
		}
	}
	return false
}

// UndescribedMediaIndexes returns the indexes of attachments of mediaType
// that do not yet have a matching prefix+name text description (e.g.
// vision:cat.png). Returns nil when all media of the given type already
// have descriptions or when there are no attachments of that type.
func UndescribedMediaIndexes(atts []Attachment, mediaType, prefix string) []int {
	var idxs []int
	for i, att := range atts {
		if att.Type == mediaType && !HasMediaDescription(atts, prefix, att.Name) {
			idxs = append(idxs, i)
		}
	}
	return idxs
}
