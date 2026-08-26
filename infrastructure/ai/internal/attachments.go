package aiutil

import (
	"strings"

	"nusashell/domain"
)

// TextAttachmentContent, DocumentAttachmentContent, and DataURLBase64 have
// been moved to the domain package. The aiutil wrappers in util.go delegate
// to the domain implementations for backward compatibility.

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

// InputImageBlock encodes an image attachment as the OpenAI Responses API
// input_image part: { type: "input_image", image_url: <data: URL>,
// detail: "auto" }.
//
// The `detail` field is REQUIRED by the Responses API spec
// (ResponseInputImageParam marks it as Required[ImageDetail]). Omitting
// it causes HTTP 400 "Field required" from strict providers — observed
// when switching to a Responses-API-compatible model mid-conversation
// with image history (the image is replayed as input_image without
// detail, and the provider rejects it).
//
// Valid values: "auto", "low", "high", "original". We send "auto" (the
// spec default) to let the provider choose the resolution.
func InputImageBlock(att domain.Attachment) map[string]any {
	return map[string]any{
		"type":      "input_image",
		"image_url": att.DataURL,
		"detail":    "auto",
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
