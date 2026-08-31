package application

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

// defaultTranscribeAudioPrompt is the transcription/description request
// sent to the audio fallback model when no explicit question is provided.
// Loaded from resources/agent/prompts/user/transcribe-audio.md so all
// call sites share one source of truth.
var defaultTranscribeAudioPrompt = resources.TranscribeAudioPrompt()

// executeReadAudio handles the read_media tool call when the sniffed kind
// is "audio". It loads the audio
// directly from disk by absolute path, then either:
//   - Native fast path (audio-capable model): returns the audio directly as
//     a tool result attachment so the model can hear it in the next round.
//   - Fallback path (non-audio model + fallback configured): transcribes/
//     describes the audio via the audio fallback model and returns the text.
//   - No fallback (non-audio model, no fallback): returns an error message
//     explaining that the model cannot hear audio and no fallback is configured.
func (a *App) executeReadAudio(run *TurnRun, toolCall domain.ToolCall, caps ModelCapabilities, settings domain.Settings) (string, []domain.Attachment, error) {
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

	// Native fast path: audio-capable model gets the audio directly.
	if caps.Audio {
		summary := "Audio loaded into your context."
		if audio.FilePath != "" {
			summary += " File path: " + audio.FilePath
		}
		return summary, []domain.Attachment{audio}, nil
	}

	// Fallback path: transcribe/describe via a configured cloud route first
	// (explicit user configuration wins), then degrade to the local offline
	// engine when available, and only then surface the legacy guidance.
	if settings.AudioProviderID != "" && settings.AudioModelID != "" {
		out, atts, cerr := a.transcribeAudioViaCloudRoute(run, caps, settings, audio, strings.TrimSpace(args.Question))
		if cerr == nil {
			return out, atts, nil
		}
		a.log("warn", "audio", "cloud audio fallback failed (%v); trying offline engine", cerr)
	}

	if out, ok := a.transcribeAudioOffline(audio); ok {
		return out, nil, nil
	}

	if settings.AudioProviderID != "" && settings.AudioModelID != "" {
		// Cloud was attempted and failed; offline unavailable. Surface the
		// cloud error message so the misconfiguration is visible.
		msg, _, cerr := a.transcribeAudioViaCloudRoute(run, caps, settings, audio, strings.TrimSpace(args.Question))
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

// transcribeAudioViaCloudRoute dispatches to the configured cloud fallback
// (stt-kind → /audio/transcriptions, otherwise multimodal chat input_audio)
// and returns its formatted output. Kept separate so the offline degradation
// ladder in executeReadAudio stays linear.
func (a *App) transcribeAudioViaCloudRoute(run *TurnRun, caps ModelCapabilities, settings domain.Settings, audio domain.Attachment, question string) (string, []domain.Attachment, error) {
	provider, apiKey, ok := a.resolveFallbackProvider(settings.AudioProviderID)
	if !ok {
		return "", nil, fmt.Errorf("audio provider %q not found or disabled", a.providerNameByID(settings.AudioProviderID))
	}
	if audioFallbackRoute(provider, settings.AudioModelID) == audioRouteTranscriptions {
		return a.transcribeAudioViaSTT(run.Ctx, provider, apiKey, settings.AudioModelID, question, audio)
	}
	return a.transcribeAudioViaChat(run, caps, settings, provider, apiKey, audio, question)
}

// transcribeAudioOffline runs the local engine when configured. The
// OfflineTranscriberFactory is resolved per call (degradation contract:
// availability decides per-read_media, never per-boot); ok=false means
// "no offline route" — callers keep their existing guidance.
func (a *App) transcribeAudioOffline(audio domain.Attachment) (string, bool) {
	if a.OfflineTranscriberFactory == nil {
		return "", false
	}
	eng, err := a.OfflineTranscriberFactory()
	if err != nil || eng == nil {
		return "", false
	}
	if status, ok := eng.(OfflineTranscriberStatus); !ok || !status.OfflineSTTAvailable() {
		return "", false
	}
	data, err := decodeAttachmentDataURL(audio.DataURL)
	if err != nil {
		a.log("warn", "audio", "offline stt: decode audio bytes: %v", err)
		return "", false
	}
	text, err := eng.TranscribeOffline(context.Background(), OfflineSTTRequest{
		Data: data, MaxSeconds: 600,
	})
	if err != nil {
		a.log("warn", "audio", "offline stt failed: %v", err)
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

// transcribeAudioViaChat is the legacy multimodal chat fallback: send the
// audio attachment as an input_audio block to an audio-capable chat model.
func (a *App) transcribeAudioViaChat(run *TurnRun, caps ModelCapabilities, settings domain.Settings, provider *domain.Provider, apiKey string, audio domain.Attachment, question string) (string, []domain.Attachment, error) {
	rawAdapter, err := a.Factory(run.Ctx, provider, apiKey)
	if err != nil {
		return "Failed to initialize audio fallback adapter.", nil, err
	}
	adapter := NewProviderContext(provider, rawAdapter)

	if question == "" {
		question = defaultTranscribeAudioPrompt
	} else {
		question = "Transcribe/describe this audio and answer the following question:\n" + question
	}

	description, err := a.describeOneAudio(run.Ctx, adapter, settings.AudioModelID, audio, question)
	if err != nil {
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

// describeOneAudio sends an audio attachment to a multimodal chat model and
// returns the text response (transcript or description). The fallback model
// must support audio input (e.g. Gemini, Qwen-VL).
func (a *App) describeOneAudio(ctx context.Context, adapter ProviderContext, model string, audio domain.Attachment, prompt string) (string, error) {
	req := ChatRequest{
		Model:  model,
		System: resources.AudioVisionSystemPrompt(),
		Messages: []ChatMessage{
			{
				Role:        "user",
				Content:     prompt,
				Attachments: []domain.Attachment{audio},
			},
		},
		MaxTokens: 2000,
	}
	resp, err := a.completeWithRetry(ctx, adapter, req)
	if err != nil {
		return "", err
	}
	description := strings.TrimSpace(resp.Content)
	if description == "" {
		description = strings.TrimSpace(resp.Reasoning)
	}
	if description == "" {
		return "", fmt.Errorf("empty transcript from audio fallback model")
	}
	return description, nil
}

// describeAudiosWithFallback converts audio attachments into text
// transcripts using the configured audio fallback model. It is called when
// the active chat model does not support audio but the user attached audio
// and configured an AudioProviderID + AudioModelID in settings.
//
// The original audio attachments are preserved on the message so that a
// later switch to an audio-capable model can still hear them. The generated
// transcripts are appended as additional "text" attachments so the
// non-audio model receives the content via chatMessages (which strips
// audio but keeps text attachments).
func (a *App) describeAudiosWithFallback(ctx context.Context, settings domain.Settings, attachments []domain.Attachment) []domain.Attachment {
	if len(attachments) == 0 {
		return attachments
	}
	if settings.AudioProviderID == "" || settings.AudioModelID == "" {
		return attachments
	}
	audioIdxs := undescribedMediaIndexes(attachments, "audio", mediaDescPrefixAudio)
	if len(audioIdxs) == 0 {
		return attachments
	}

	provider, apiKey, ok := a.resolveFallbackProvider(settings.AudioProviderID)
	if !ok {
		a.log("warn", "audio", "audio fallback provider %q not found or disabled; skipping audio transcription", a.providerNameByID(settings.AudioProviderID))
		return attachments
	}
	rawAdapter, err := a.Factory(ctx, provider, apiKey)
	if err != nil {
		a.log("warn", "audio", "failed to build audio fallback adapter: %v", err)
		return attachments
	}
	adapter := NewProviderContext(provider, rawAdapter)

	out := make([]domain.Attachment, 0, len(attachments)+len(audioIdxs))
	out = append(out, attachments...)
	for _, idx := range audioIdxs {
		aud := attachments[idx]
		prompt := defaultTranscribeAudioPrompt
		description, err := a.describeOneAudio(ctx, adapter, settings.AudioModelID, aud, prompt)
		if err != nil {
			a.log("warn", "audio", "audio transcription failed for %q: %v", aud.Name, err)
			continue
		}
		out = append(out, domain.Attachment{
			Type:      "text",
			Name:      mediaDescPrefixAudio + aud.Name,
			MediaType: "text/plain",
			Content:   fmt.Sprintf("[Audio transcript for %s]\n%s", aud.Name, description),
		})
	}
	return out
}

// enrichWithAudioDescriptions finds the latest user message in the
// conversation, transcribes its audio attachments via the audio fallback
// model, appends the transcripts as text attachments, persists the updated
// message, and returns a reloaded conversation so chatMessages sees the
// transcripts. If no audio is present or transcription fails, the
// conversation is returned unchanged.
func (a *App) enrichWithAudioDescriptions(ctx context.Context, conversation *domain.Conversation, _ string, settings domain.Settings) *domain.Conversation {
	var userMsgIdx int = -1
	for i := len(conversation.Messages) - 1; i >= 0; i-- {
		if conversation.Messages[i].Role == domain.RoleUser {
			userMsgIdx = i
			break
		}
	}
	if userMsgIdx < 0 {
		return conversation
	}
	userMsg := &conversation.Messages[userMsgIdx]
	pending := undescribedMediaIndexes(userMsg.Attachments, "audio", mediaDescPrefixAudio)
	if len(pending) == 0 {
		return conversation
	}

	a.log("info", "audio", "transcribing %d audio file(s) via fallback model %s/%s for non-audio chat model",
		len(pending), a.providerNameByID(settings.AudioProviderID), settings.AudioModelID)

	described := a.describeAudiosWithFallback(ctx, settings, userMsg.Attachments)
	if len(described) <= len(userMsg.Attachments) {
		return conversation
	}

	repo, err := a.loadRepo(conversation.ID)
	if err != nil {
		a.log("warn", "audio", "failed to load conversation for audio transcripts: %v", err)
		return conversation
	}
	a.updateMessage(repo.Conversation(), userMsg.ID, func(msg *domain.Message) {
		msg.Attachments = described
	})
	if err := repo.Save(); err != nil {
		a.log("warn", "audio", "failed to persist audio transcripts: %v", err)
		return conversation
	}

	reloaded, err := a.Conversations.Get(conversation.ID)
	if err != nil {
		return conversation
	}
	return reloaded
}

const (
	mediaDescPrefixVision = domain.MediaDescPrefixVision
	mediaDescPrefixAudio  = domain.MediaDescPrefixAudio
	mediaDescPrefixVideo  = domain.MediaDescPrefixVideo
)

// undescribedMediaIndexes returns attachments of mediaType that do not yet
// have a matching prefix+name text description (e.g. vision:cat.png).
func undescribedMediaIndexes(atts []domain.Attachment, mediaType, prefix string) []int {
	return domain.UndescribedMediaIndexes(atts, mediaType, prefix)
}
