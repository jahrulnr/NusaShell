package application

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"nusashell/domain"
)

// executeReadVideo handles the read_video tool call. It finds the requested
// video attachment in the conversation history, then either:
//   - Native fast path (video-capable model): returns the video directly as
//     a tool result attachment so the model can see it in the next round.
//   - Fallback path (non-video model + fallback configured): describes the
//     video via the video fallback model and returns the text description.
//   - No fallback (non-video model, no fallback): returns an error message
//     explaining that the model cannot see video and no fallback is configured.
func (a *App) executeReadVideo(run *TurnRun, toolCall domain.ToolCall, caps ModelCapabilities, settings domain.Settings) (string, []domain.Attachment, error) {
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

	video, err := findVideoAttachmentByPath(conversation, path)
	if err != nil {
		return "error: " + err.Error(), nil, err
	}

	// Native fast path: video-capable model gets the video directly.
	if caps.Video {
		question := strings.TrimSpace(args.Question)
		summary := "Video loaded into your context."
		if video.FilePath != "" {
			summary += " File path: " + video.FilePath
		}
		if question != "" {
			summary += " Question: " + question
		}
		return summary, []domain.Attachment{video}, nil
	}

	// Fallback path: describe via video fallback model.
	if settings.VideoProviderID == "" || settings.VideoModelID == "" {
		msg := "This model does not support video input and no video fallback model is configured. Ask the user to configure a video fallback in settings, or switch to a video-capable model."
		if video.FilePath != "" {
			msg += " The video file is saved at: " + video.FilePath
		}
		return msg, nil, nil
	}

	provider, apiKey, ok := a.resolveFallbackProvider(settings.VideoProviderID)
	if !ok {
		return "Video fallback provider not found or disabled.", nil, fmt.Errorf("video provider %q not found", settings.VideoProviderID)
	}
	adapter, err := a.Factory(run.Ctx, provider, apiKey)
	if err != nil {
		return "Failed to initialize video fallback adapter.", nil, err
	}

	question := strings.TrimSpace(args.Question)
	if question == "" {
		question = "Describe this video concisely. Focus on visible actions, people, settings, text on screen, and notable events. Keep it factual and under 300 words."
	} else {
		question = "Describe this video and answer the following question:\n" + question
	}

	description, err := a.describeOneVideo(run.Ctx, adapter, settings.VideoModelID, video, question)
	if err != nil {
		return "Video description failed: " + err.Error(), nil, err
	}

	result := fmt.Sprintf("[Video description for %s]\n%s", video.Name, description)
	if video.FilePath != "" {
		result += "\n\nFile path: " + video.FilePath
	}
	return result, nil, nil
}

// findVideoAttachmentByPath searches all user messages in the conversation
// for a video attachment matching the given absolute file path
// (case-insensitive).
func findVideoAttachmentByPath(conversation *domain.Conversation, path string) (domain.Attachment, error) {
	target := strings.ToLower(strings.TrimSpace(path))
	for _, msg := range conversation.Messages {
		if msg.Role != domain.RoleUser {
			continue
		}
		for _, att := range msg.Attachments {
			if att.Type != "video" {
				continue
			}
			if att.FilePath != "" && strings.ToLower(att.FilePath) == target {
				return att, nil
			}
		}
	}
	return domain.Attachment{}, fmt.Errorf("video attachment with file_path %q not found in conversation", path)
}

// describeOneVideo sends a video attachment to a multimodal chat model and
// returns the text description. The fallback model must support video input
// (e.g. Gemini 2.5+, Qwen-VL).
func (a *App) describeOneVideo(ctx context.Context, adapter AIProvider, model string, video domain.Attachment, prompt string) (string, error) {
	req := ChatRequest{
		Model:  model,
		System: "You are a video analysis assistant. Describe videos accurately and concisely for a text-only model that cannot see them.",
		Messages: []ChatMessage{
			{
				Role:        "user",
				Content:     prompt,
				Attachments: []domain.Attachment{video},
			},
		},
		MaxTokens: 1000,
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
		return "", fmt.Errorf("empty description from video fallback model")
	}
	return description, nil
}

// describeVideosWithFallback converts video attachments into text
// descriptions using the configured video fallback model. It is called when
// the active chat model does not support video but the user attached video
// and configured a VideoProviderID + VideoModelID in settings.
//
// The original video attachments are preserved on the message so that a
// later switch to a video-capable model can still see them. The generated
// descriptions are appended as additional "text" attachments so the
// non-video model receives the content via chatMessages (which strips
// video but keeps text attachments).
func (a *App) describeVideosWithFallback(ctx context.Context, settings domain.Settings, attachments []domain.Attachment) []domain.Attachment {
	if len(attachments) == 0 {
		return attachments
	}
	if settings.VideoProviderID == "" || settings.VideoModelID == "" {
		return attachments
	}
	var videoIdxs []int
	for i, att := range attachments {
		if att.Type == "video" {
			videoIdxs = append(videoIdxs, i)
		}
	}
	if len(videoIdxs) == 0 {
		return attachments
	}

	provider, apiKey, ok := a.resolveFallbackProvider(settings.VideoProviderID)
	if !ok {
		a.log("warn", "video", "video fallback provider %q not found or disabled; skipping video description", settings.VideoProviderID)
		return attachments
	}
	adapter, err := a.Factory(ctx, provider, apiKey)
	if err != nil {
		a.log("warn", "video", "failed to build video fallback adapter: %v", err)
		return attachments
	}

	out := make([]domain.Attachment, 0, len(attachments)+len(videoIdxs))
	out = append(out, attachments...)
	for _, idx := range videoIdxs {
		vid := attachments[idx]
		prompt := "Describe this video concisely. Focus on visible actions, people, settings, text on screen, and notable events. Keep it factual and under 300 words."
		description, err := a.describeOneVideo(ctx, adapter, settings.VideoModelID, vid, prompt)
		if err != nil {
			a.log("warn", "video", "video description failed for %q: %v", vid.Name, err)
			continue
		}
		out = append(out, domain.Attachment{
			Type:      "text",
			Name:      "video:" + vid.Name,
			MediaType: "text/plain",
			Content:   fmt.Sprintf("[Video description for %s]\n%s", vid.Name, description),
		})
	}
	return out
}

// enrichWithVideoDescriptions finds the latest user message in the
// conversation, describes its video attachments via the video fallback
// model, appends the descriptions as text attachments, persists the updated
// message, and returns a reloaded conversation so chatMessages sees the
// descriptions. If no video is present or description fails, the
// conversation is returned unchanged.
func (a *App) enrichWithVideoDescriptions(ctx context.Context, conversation *domain.Conversation, _ string, settings domain.Settings) *domain.Conversation {
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
	hasVideo := false
	for _, att := range userMsg.Attachments {
		if att.Type == "video" {
			hasVideo = true
			break
		}
	}
	if !hasVideo {
		return conversation
	}

	a.log("info", "video", "describing %d video file(s) via fallback model %s/%s for non-video chat model",
		countAttachmentsByType(userMsg.Attachments, "video"), settings.VideoProviderID, settings.VideoModelID)

	described := a.describeVideosWithFallback(ctx, settings, userMsg.Attachments)
	if len(described) <= len(userMsg.Attachments) {
		return conversation
	}

	a.updateMessage(conversation, userMsg.ID, func(msg *domain.Message) {
		msg.Attachments = described
	})
	if err := a.Conversations.Save(conversation); err != nil {
		a.log("warn", "video", "failed to persist video descriptions: %v", err)
		return conversation
	}

	reloaded, err := a.Conversations.Get(conversation.ID)
	if err != nil {
		return conversation
	}
	return reloaded
}
