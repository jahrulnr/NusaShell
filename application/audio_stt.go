package application

import (
	"context"
	"fmt"

	"nusashell/domain"
	"nusashell/pkg/yamlmd"
)

// Audio fallback API routes. Probe-verified 2026-08-23
// (.experimental/audio-probe): catalog models of kind "stt"
// (gpt-4o-mini-transcribe, whisper-1) are served ONLY by the dedicated
// /audio/transcriptions endpoint — Chat Completions input_audio rejects
// them and the Responses API has no audio transport at all. Every other
// kind keeps the multimodal chat input_audio fallback.
type audioRoute string

const (
	audioRouteChat           audioRoute = "chat"
	audioRouteTranscriptions audioRoute = "transcriptions"
)

// audioFallbackRoute decides how the configured fallback model is reached.
// Unknown models default to the chat route (backward compatible).
func audioFallbackRoute(provider *domain.Provider, model string) audioRoute {
	m := provider.FindModel(model)
	if m != nil && m.Kind == domain.ModelKindSTT {
		return audioRouteTranscriptions
	}
	return audioRouteChat
}

// transcribeAudioViaSTT runs the dedicated speech-transcription path for
// stt-kind fallback models and formats the result identically to the chat
// transcript fallback so callers render one consistent shape.
func (a *App) transcribeAudioViaSTT(ctx context.Context, provider *domain.Provider, apiKey, model, question string, att domain.Attachment) (string, []domain.Attachment, error) {
	if a.SpeechTranscriberFactory == nil {
		msg := fmt.Sprintf("Audio fallback model %q is speech-transcription-only (kind stt) and needs /audio/transcriptions support, which is unavailable in this build. Pick an audio-capable chat model instead.", model)
		return msg, nil, fmt.Errorf("speech transcription factory is not wired")
	}
	transcriber, err := a.SpeechTranscriberFactory(provider, apiKey)
	if err != nil {
		return "Failed to initialize speech transcription client.", nil, err
	}

	data, err := decodeAttachmentDataURL(att.DataURL)
	if err != nil {
		return "error: could not decode loaded audio", nil, fmt.Errorf("decode audio bytes: %w", err)
	}

	// Transcription endpoints take a spelling/style hint, not questions.
	req := STTRequest{
		Model:    model,
		Data:     data,
		Filename: att.Name,
		Prompt:   question,
	}
	text, err := transcriber.Transcribe(ctx, req)
	if err != nil {
		return "Audio transcription failed: " + err.Error(), nil, err
	}

	result := fmt.Sprintf("[Audio transcript for %s]\n%s", att.Name, text)
	if att.FilePath != "" {
		result += "\n\nFile path: " + att.FilePath
	}
	meta := map[string]any{
		"type": "audio", "name": att.Name, "route": string(audioRouteTranscriptions),
	}
	if att.FilePath != "" {
		meta["file_path"] = att.FilePath
	}
	return yamlmd.MD(meta, result), nil, nil
}
