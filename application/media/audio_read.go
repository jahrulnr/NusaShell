package media

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"nusashell/application/service/mediaread"
	"nusashell/domain"
	"nusashell/pkg/yamlmd"
	"nusashell/resources"
)

var defaultTranscribeAudioPrompt = resources.TranscribeAudioPrompt()

// ExecuteReadAudio handles the read_media tool call when the sniffed kind
// is "audio". Native fast path returns the attachment; fallback transcribes
// via STT, chat Describe, or the offline engine.
func (s *Service) ExecuteReadAudio(call Call, toolCall domain.ToolCall, caps Caps, settings domain.Settings) (string, []domain.Attachment, error) {
	var args struct {
		FilePath string `json:"file_path"`
		Question string `json:"question"`
	}
	if err := json.Unmarshal([]byte(toolCall.Args), &args); err != nil {
		return "error: invalid arguments", nil, fmt.Errorf("invalid args: %w", err)
	}
	path := strings.TrimSpace(args.FilePath)
	if path == "" {
		return "error: file_path is required", nil, fmt.Errorf("file_path is required")
	}
	if !filepath.IsAbs(path) {
		return "error: file_path must be an absolute path", nil, fmt.Errorf("file_path must be absolute, got %q", path)
	}

	audio, err := mediaread.LoadMediaAttachment("audio", path)
	if err != nil {
		return "error: " + err.Error(), nil, err
	}

	if caps.Audio {
		summary := "Audio loaded into your context."
		if audio.FilePath != "" {
			summary += " File path: " + audio.FilePath
		}
		return summary, []domain.Attachment{audio}, nil
	}

	if settings.AudioProviderID != "" && settings.AudioModelID != "" {
		out, atts, cerr := s.transcribeAudioViaCloudRoute(call, settings, audio, strings.TrimSpace(args.Question))
		if cerr == nil {
			return out, atts, nil
		}
		s.write("warn", "audio", "cloud audio fallback failed (%v); trying offline engine", cerr)
	}

	if out, ok := s.transcribeAudioOffline(audio); ok {
		return out, nil, nil
	}

	if settings.AudioProviderID != "" && settings.AudioModelID != "" {
		msg, _, cerr := s.transcribeAudioViaCloudRoute(call, settings, audio, strings.TrimSpace(args.Question))
		if cerr != nil {
			return msg, nil, cerr
		}
		return msg, nil, nil
	}

	msg := "This model does not support audio input and no audio fallback model is configured. Ask the user to configure an audio fallback in settings, or switch to an audio-capable model."
	if audio.FilePath != "" {
		msg += " The audio file is saved at: " + audio.FilePath
	}
	return msg, nil, nil
}

func (s *Service) transcribeAudioViaCloudRoute(call Call, settings domain.Settings, audio domain.Attachment, question string) (string, []domain.Attachment, error) {
	provider, apiKey, ok := s.resolve(settings.AudioProviderID)
	if !ok {
		return "", nil, fmt.Errorf("audio provider %q not found or disabled", s.name(settings.AudioProviderID))
	}
	if audioFallbackRoute(provider, settings.AudioModelID) == audioRouteTranscriptions {
		return s.transcribeAudioViaSTT(call.ctx(), provider, apiKey, settings.AudioModelID, question, audio)
	}
	return s.transcribeAudioViaChat(call, settings, audio, question)
}

func (s *Service) transcribeAudioOffline(audio domain.Attachment) (string, bool) {
	if s.offlineSTT == nil {
		return "", false
	}
	eng, err := s.offlineSTT()
	if err != nil || eng == nil {
		return "", false
	}
	if status, ok := eng.(OfflineTranscriberStatus); !ok || !status.OfflineSTTAvailable() {
		return "", false
	}
	data, err := decodeAttachmentDataURL(audio.DataURL)
	if err != nil {
		s.write("warn", "audio", "offline stt: decode audio bytes: %v", err)
		return "", false
	}
	text, err := eng.TranscribeOffline(context.Background(), OfflineSTTRequest{
		Data: data, MaxSeconds: 600,
	})
	if err != nil {
		s.write("warn", "audio", "offline stt failed: %v", err)
		return "", false
	}
	result := fmt.Sprintf("[Audio transcript for %s]\n%s", audio.Name, text)
	if audio.FilePath != "" {
		result += "\n\nFile path: " + audio.FilePath
	}
	meta := map[string]any{
		"type": "audio", "name": audio.Name, "route": "offline",
	}
	if audio.FilePath != "" {
		meta["file_path"] = audio.FilePath
	}
	return yamlmd.MD(meta, result), true
}

func (s *Service) transcribeAudioViaChat(call Call, settings domain.Settings, audio domain.Attachment, question string) (string, []domain.Attachment, error) {
	if question == "" {
		question = defaultTranscribeAudioPrompt
	} else {
		question = "Transcribe/describe this audio and answer the following question:\n" + question
	}

	description, err := s.describe(call.ctx(), settings.AudioProviderID, settings.AudioModelID, question, audio, 2000)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "initialize") {
			return "Failed to initialize audio fallback adapter.", nil, err
		}
		return "Audio transcription failed: " + err.Error(), nil, err
	}

	result := fmt.Sprintf("[Audio transcript for %s]\n%s", audio.Name, description)
	if audio.FilePath != "" {
		result += "\n\nFile path: " + audio.FilePath
	}
	meta := map[string]any{"type": "audio", "name": audio.Name, "route": string(audioRouteChat)}
	if audio.FilePath != "" {
		meta["file_path"] = audio.FilePath
	}
	return yamlmd.MD(meta, result), nil, nil
}

// DescribeAudiosWithFallback converts audio attachments into text
// transcripts using the configured audio fallback model.
func (s *Service) DescribeAudiosWithFallback(ctx context.Context, settings domain.Settings, atts []domain.Attachment) []domain.Attachment {
	if len(atts) == 0 {
		return atts
	}
	if settings.AudioProviderID == "" || settings.AudioModelID == "" {
		return atts
	}
	audioIdxs := domain.UndescribedMediaIndexes(atts, "audio", domain.MediaDescPrefixAudio)
	if len(audioIdxs) == 0 {
		return atts
	}

	if _, _, ok := s.resolve(settings.AudioProviderID); !ok {
		s.write("warn", "audio", "audio fallback provider %q not found or disabled; skipping audio transcription", s.name(settings.AudioProviderID))
		return atts
	}

	out := make([]domain.Attachment, 0, len(atts)+len(audioIdxs))
	out = append(out, atts...)
	for _, idx := range audioIdxs {
		aud := atts[idx]
		prompt := defaultTranscribeAudioPrompt
		description, err := s.describe(ctx, settings.AudioProviderID, settings.AudioModelID, prompt, aud, 2000)
		if err != nil {
			s.write("warn", "audio", "audio transcription failed for %q: %v", aud.Name, err)
			continue
		}
		out = append(out, domain.Attachment{
			Type:      "text",
			Name:      domain.MediaDescPrefixAudio + aud.Name,
			MediaType: "text/plain",
			Content:   fmt.Sprintf("[Audio transcript for %s]\n%s", aud.Name, description),
		})
	}
	return out
}
