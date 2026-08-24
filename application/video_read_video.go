package application

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"nusashell/domain"
)

// executeReadVideo handles the read_media tool call when the sniffed kind
// is "video". It loads the video
// directly from disk by absolute path, then either:
//   - Native fast path (video-capable model): returns the video directly as
//     a tool result attachment so the model can see it in the next round.
//   - Fallback path (non-video model + fallback configured): describes the
//     video via the video fallback model and returns the text description.
//   - No fallback (non-video model, no fallback): returns an error message
//     explaining that the model cannot see video and no fallback is configured.
//
// defaultDescribeVideoPrompt is the description request used when the video
// read/fallback path has no explicit question. Kept as a single constant so
// both call sites share one string.
const defaultDescribeVideoPrompt = "Describe this video concisely. Focus on visible actions, people, settings, text on screen, and notable events. Keep it factual and under 300 words."

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

	video, err := loadMediaAttachment("video", path)
	if err != nil {
		return "error: " + err.Error(), nil, err
	}

	// Native fast path: video-capable model gets the video directly.
	if caps.Video {
		summary := "Video loaded into your context."
		if video.FilePath != "" {
			summary += " File path: " + video.FilePath
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
		return "Video fallback provider not found or disabled.", nil, fmt.Errorf("video provider %q not found", a.providerNameByID(settings.VideoProviderID))
	}
	adapter, err := a.Factory(run.Ctx, provider, apiKey)
	if err != nil {
		return "Failed to initialize video fallback adapter.", nil, err
	}

	question := strings.TrimSpace(args.Question)
	if question == "" {
		question = defaultDescribeVideoPrompt
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
	meta := map[string]any{"type": "video", "name": video.Name}
	if video.FilePath != "" {
		meta["file_path"] = video.FilePath
	}
	return yamlMDApp(meta, result), nil, nil
}

// describeOneVideo sends a video attachment to a multimodal chat model and
// returns the text description. The fallback model must support video input
// (e.g. Gemini 2.5+, Qwen-VL).
func (a *App) describeOneVideo(ctx context.Context, adapter AIProvider, model string, video domain.Attachment, prompt string) (string, error) {
	req := ChatRequest{
		Model:  model,
		System: "Describe videos accurately and concisely for a text-only model that cannot see them.",
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
		a.log("warn", "video", "video fallback provider %q not found or disabled; skipping video description", a.providerNameByID(settings.VideoProviderID))
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
		prompt := defaultDescribeVideoPrompt
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
		countAttachmentsByType(userMsg.Attachments, "video"), a.providerNameByID(settings.VideoProviderID), settings.VideoModelID)

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
