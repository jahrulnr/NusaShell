package application

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"nusashell/domain"
)

// executeReadAudio handles the read_audio tool call. It finds the requested
// audio attachment in the conversation history, then either:
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

	conversation, err := a.Conversations.Get(run.ConversationID)
	if err != nil {
		return "error: conversation not found", nil, err
	}

	audio, err := findAudioAttachmentByPath(conversation, path)
	if err != nil {
		return "error: " + err.Error(), nil, err
	}

	// Native fast path: audio-capable model gets the audio directly.
	if caps.Audio {
		question := strings.TrimSpace(args.Question)
		summary := "Audio loaded into your context."
		if audio.FilePath != "" {
			summary += " File path: " + audio.FilePath
		}
		if question != "" {
			summary += " Question: " + question
		}
		return summary, []domain.Attachment{audio}, nil
	}

	// Fallback path: transcribe/describe via audio fallback model.
	if settings.AudioProviderID == "" || settings.AudioModelID == "" {
		msg := "This model does not support audio input and no audio fallback model is configured. Ask the user to configure an audio fallback in settings, or switch to an audio-capable model."
		if audio.FilePath != "" {
			msg += " The audio file is saved at: " + audio.FilePath
		}
		return msg, nil, nil
	}

	provider, apiKey, ok := a.resolveFallbackProvider(settings.AudioProviderID)
	if !ok {
		return "Audio fallback provider not found or disabled.", nil, fmt.Errorf("audio provider %q not found", settings.AudioProviderID)
	}
	adapter, err := a.Factory(run.Ctx, provider, apiKey)
	if err != nil {
		return "Failed to initialize audio fallback adapter.", nil, err
	}

	question := strings.TrimSpace(args.Question)
	if question == "" {
		question = "Transcribe this audio. If there is speech, provide a full transcript. If there is music or ambient sound, describe it concisely."
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
	return result, nil, nil
}

// findAudioAttachmentByPath searches all user messages in the conversation
// for an audio attachment matching the given absolute file path
// (case-insensitive).
func findAudioAttachmentByPath(conversation *domain.Conversation, path string) (domain.Attachment, error) {
	target := strings.ToLower(strings.TrimSpace(path))
	for _, msg := range conversation.Messages {
		if msg.Role != domain.RoleUser {
			continue
		}
		for _, att := range msg.Attachments {
			if att.Type != "audio" {
				continue
			}
			if att.FilePath != "" && strings.ToLower(att.FilePath) == target {
				return att, nil
			}
		}
	}
	return domain.Attachment{}, fmt.Errorf("audio attachment with file_path %q not found in conversation", path)
}

// describeOneAudio sends an audio attachment to a multimodal chat model and
// returns the text response (transcript or description). The fallback model
// must support audio input (e.g. Gemini, Qwen-VL).
func (a *App) describeOneAudio(ctx context.Context, adapter AIProvider, model string, audio domain.Attachment, prompt string) (string, error) {
	req := ChatRequest{
		Model:  model,
		System: "You are an audio transcription assistant. Transcribe speech accurately and describe non-speech audio concisely.",
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
	var audioIdxs []int
	for i, att := range attachments {
		if att.Type == "audio" {
			audioIdxs = append(audioIdxs, i)
		}
	}
	if len(audioIdxs) == 0 {
		return attachments
	}

	provider, apiKey, ok := a.resolveFallbackProvider(settings.AudioProviderID)
	if !ok {
		a.log("warn", "audio", "audio fallback provider %q not found or disabled; skipping audio transcription", settings.AudioProviderID)
		return attachments
	}
	adapter, err := a.Factory(ctx, provider, apiKey)
	if err != nil {
		a.log("warn", "audio", "failed to build audio fallback adapter: %v", err)
		return attachments
	}

	out := make([]domain.Attachment, 0, len(attachments)+len(audioIdxs))
	out = append(out, attachments...)
	for _, idx := range audioIdxs {
		aud := attachments[idx]
		prompt := "Transcribe this audio. If there is speech, provide a full transcript. If there is music or ambient sound, describe it concisely."
		description, err := a.describeOneAudio(ctx, adapter, settings.AudioModelID, aud, prompt)
		if err != nil {
			a.log("warn", "audio", "audio transcription failed for %q: %v", aud.Name, err)
			continue
		}
		out = append(out, domain.Attachment{
			Type:      "text",
			Name:      "audio:" + aud.Name,
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
	hasAudio := false
	for _, att := range userMsg.Attachments {
		if att.Type == "audio" {
			hasAudio = true
			break
		}
	}
	if !hasAudio {
		return conversation
	}

	a.log("info", "audio", "transcribing %d audio file(s) via fallback model %s/%s for non-audio chat model",
		countAttachmentsByType(userMsg.Attachments, "audio"), settings.AudioProviderID, settings.AudioModelID)

	described := a.describeAudiosWithFallback(ctx, settings, userMsg.Attachments)
	if len(described) <= len(userMsg.Attachments) {
		return conversation
	}

	a.updateMessage(conversation, userMsg.ID, func(msg *domain.Message) {
		msg.Attachments = described
	})
	if err := a.Conversations.Save(conversation); err != nil {
		a.log("warn", "audio", "failed to persist audio transcripts: %v", err)
		return conversation
	}

	reloaded, err := a.Conversations.Get(conversation.ID)
	if err != nil {
		return conversation
	}
	return reloaded
}

func countAttachmentsByType(atts []domain.Attachment, typ string) int {
	n := 0
	for _, a := range atts {
		if a.Type == typ {
			n++
		}
	}
	return n
}
