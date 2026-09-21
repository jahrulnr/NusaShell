package tools

import (
	"strings"

	"nusashell/application"
)

// read_media and generate_media are advertised here but executed by the
// agent layer (application/agent/agent_round_tools.go): they return
// attachments and need model capabilities the toolbox does not hold, so
// their handler stays nil.
func (t *Toolbox) readMediaToolEntry() toolEntry {
	return toolEntry{
		info: application.ToolInfo{Name: "read_media", Description: "Load a media file (image, audio, video, or PDF document) from disk into your context. The media type is auto-detected from binary magic bytes — no need to specify whether it is an image, audio, video, or PDF. When your active model supports the kind natively, the file attaches to your context directly. For non-capable models, a fallback model transcribes/describes the content and returns the text, or a placeholder note with the file path for documents.", InputSchema: obj("object", props("file_path", str("Absolute path of the media file on disk"), "question", str("Optional question about the media content")), "file_path")},
	}
}

func (t *Toolbox) generateMediaToolEntry() toolEntry {
	return toolEntry{
		info: application.ToolInfo{
			Name:        "generate_media",
			Description: "Generate media from a prompt and save it for the user to view/play. media_type selects the generator: \"image\" (text→PNG/JPEG/WebP; referenced images enable editing), \"speech\" (text→spoken audio), \"video\" (text→short mp4 clip; async upstream — tens of seconds to minutes is expected; referenced images enable image-to-video). Only modes configured in Settings can serve a request. The result is already delivered to the user — do not re-render it as Markdown.",
			InputSchema: obj("object", props(
				"media_type", strEnum("Which generator to use", "image", "speech", "video"),
				"prompt", str("Text input for the chosen generator: scene description (image/video; max 4000 chars for video) or the text to speak aloud (max 20000 chars)."),
				"size", strEnum("Image only: output size", "auto", "1024x1024", "1536x1024", "1024x1536"),
				"quality", strEnum("Image only: rendering quality", "auto", "low", "medium", "high"),
				"background", strEnum("Image only: background treatment", "auto", "transparent", "opaque"),
				"n", map[string]any{"type": "integer", "description": "Image only: number of images to generate (1-4, default 1).", "minimum": 1, "maximum": 4},
				"referenced_image_paths", map[string]any{
					"type":        "array",
					"description": "Image or video: absolute paths of source images. For image media_type, enables image-to-image editing. For video media_type, the first image becomes the first frame (image-to-video); additional images are style references. Models without i2i/i2v support will reject upstream — check the model picker badges. Max 5.",
					"items":       map[string]any{"type": "string"},
					"maxItems":    5,
				},
				"voice", str("Speech only: voice id (provider-specific, e.g. alloy). Omit for default."),
				"format", strEnum("Speech only: audio format", "mp3", "wav", "opus"),
				"speed", map[string]any{"type": "number", "description": "Speech only: speed 0.25-4.0 (default 1.0)."},
				"duration_seconds", map[string]any{"type": "integer", "description": "Video only: clip length in seconds. Provider minimums apply and are reported verbatim on rejection (e.g. 'Supported durations: 4, 6, 8s')."},
				"resolution", strEnum("Video only: output resolution", "480p", "720p", "1080p"),
			), "media_type", "prompt"),
		},
		enabled: (*Toolbox).mediaGenerationAnyConfigured,
	}
}

// mediaGenerationAnyConfigured reports whether at least one generate_media
// mode has a configured backend (the unified tool is advertised once for all
// modes; unconfigured modes are rejected at execution time with guidance).
func (t *Toolbox) mediaGenerationAnyConfigured() bool {
	imageConfigured := false
	if t.Settings != nil {
		s := t.Settings.Get()
		imageConfigured = mediaGenerationConfigured(s.ImageProviderID, s.ImageModelID)
	}
	return imageConfigured || t.speechGenerationConfigured() || t.videoGenerationConfigured()
}

// mediaGenerationConfigured is the shared gate for all generate_* tools:
// both provider and model must be set (non-empty).
func mediaGenerationConfigured(providerID, modelID string) bool {
	return strings.TrimSpace(providerID) != "" && strings.TrimSpace(modelID) != ""
}

// speechGenerationConfigured reports whether generate_speech can serve:
// an online TTS model is picked, OR offline piper is wired (the toolbox
// learns this via the SpeechGenerationAvailable flag set at composition).
func (t *Toolbox) speechGenerationConfigured() bool {
	if t.Settings == nil {
		return false
	}
	s := t.Settings.Get()
	if mediaGenerationConfigured(s.TTSProviderID, s.TTSModelID) {
		return true
	}
	return t.SpeechOfflineAvailable
}

// videoGenerationConfigured reports whether generate_video can serve:
// an online video model must be picked in Settings (no offline fallback).
func (t *Toolbox) videoGenerationConfigured() bool {
	if t.Settings == nil {
		return false
	}
	s := t.Settings.Get()
	return mediaGenerationConfigured(s.VideoGenProviderID, s.VideoGenModelID)
}
