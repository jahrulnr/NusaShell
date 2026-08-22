package aiutil

import (
	"strings"

	"nusashell/domain"
)

// TextAttachmentContent renders a text attachment as inline content.
// The leading newline keeps the header visually separated from the user's
// message text even when a provider concatenates adjacent text blocks
// without a separator. The header explicitly states that the full content
// follows, so models do not mistake the attachment for a dangling reference
// to a file they still need to obtain.
func TextAttachmentContent(attachment domain.Attachment) string {
	return "\n[Attached text file: " + attachment.Name + " - full content included below]\n\n" + attachment.Content
}

// DocumentAttachmentContent renders a document attachment as a descriptive
// text marker (used by Chat Completions, which has no portable file part).
func DocumentAttachmentContent(attachment domain.Attachment) string {
	return "[Attached document: " + attachment.Name + " (" + attachment.MediaType + ")]"
}

// DataURLBase64 extracts the base64 payload from a data: URL.
func DataURLBase64(dataURL string) string {
	_, data, ok := strings.Cut(dataURL, ",")
	if !ok {
		return ""
	}
	return data
}

// InputAudioFormat maps a MIME type to the OpenAI input_audio format enum
// (mp3, wav, ogg, webm, flac). Unknown or empty media types default to mp3
// (the most common TTS output).
func InputAudioFormat(mediaType string) string {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "audio/wav", "audio/x-wav", "audio/wave", "audio/vnd.wave":
		return "wav"
	case "audio/ogg":
		return "ogg"
	case "audio/webm":
		return "webm"
	case "audio/flac":
		return "flac"
	default:
		return "mp3"
	}
}

// InputAudioBlock encodes an audio attachment as the OpenAI Responses API
// input_audio part: { type: "input_audio", input_audio: { data: <base64>,
// format: <mp3|wav|ogg|webm|flac> } }. This is the wire format OpenAI,
// Nvidia NIM, and OpenRouter expect for audio input on the Responses API.
// Sending audio as input_image (with a data:audio/... URL) causes providers
// to attempt image decoding and fail with "Failed to load image" errors.
func InputAudioBlock(att domain.Attachment) map[string]any {
	return map[string]any{
		"type": "input_audio",
		"input_audio": map[string]any{
			"data":   DataURLBase64(att.DataURL),
			"format": InputAudioFormat(att.MediaType),
		},
	}
}
