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

// VideoURLBlock encodes a video attachment as the OpenRouter video_url
// content part: { type: "video_url", video_url: { url: <data: URL> } }.
//
// video_url is an OpenRouter-specific extension. OpenAI's official API does
// NOT support video input natively (confirmed 2026-08-23: OpenAI FAQ says
// "No it can not handle videos. It currently supports processing static
// images only." — the only OpenAI-sanctioned workaround is extracting frames
// and sending them as input_image). Anthropic also lacks native video input.
//
// OpenRouter routes video_url to providers that support video input (e.g.
// Gemini, Stealth/ox-alpha). Sending video as image_url or input_image with
// a data:video/... URL causes providers to reject it with HTTP 400 because
// they attempt image decoding on a video payload.
//
// Capability gating: video attachments are stripped for non-video models
// before they reach the handler (see application.filterToolAttachmentsByCaps
// and application.chatMessages). This block is only called for models whose
// catalog entry has Video=true. The url field accepts both public URLs and
// base64 data URLs.
func VideoURLBlock(att domain.Attachment) map[string]any {
	return map[string]any{
		"type":      "video_url",
		"video_url": map[string]any{"url": att.DataURL},
	}
}
