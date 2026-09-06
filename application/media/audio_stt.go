package media

import (
	"context"
	"fmt"

	"nusashell/domain"
	"nusashell/pkg/yamlmd"
)

type audioRoute string

const (
	audioRouteChat           audioRoute = "chat"
	audioRouteTranscriptions audioRoute = "transcriptions"
)

func audioFallbackRoute(provider *domain.Provider, model string) audioRoute {
	m := provider.FindModel(model)
	if m != nil && m.Kind == domain.ModelKindSTT {
		return audioRouteTranscriptions
	}
	return audioRouteChat
}

func (s *Service) transcribeAudioViaSTT(ctx context.Context, provider *domain.Provider, apiKey, model, question string, att domain.Attachment) (string, []domain.Attachment, error) {
	if s.speechSTT == nil {
		msg := fmt.Sprintf("Audio fallback model %q is speech-transcription-only (kind stt) and needs /audio/transcriptions support, which is unavailable in this build. Pick an audio-capable chat model instead.", model)
		return msg, nil, fmt.Errorf("speech transcription factory is not wired")
	}
	transcriber, err := s.speechSTT(provider, apiKey)
	if err != nil {
		return "Failed to initialize speech transcription client.", nil, err
	}

	data, err := decodeAttachmentDataURL(att.DataURL)
	if err != nil {
		return "error: could not decode loaded audio", nil, fmt.Errorf("decode audio bytes: %w", err)
	}

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
