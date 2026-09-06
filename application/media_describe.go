package application

import (
	"context"
	"fmt"
	"strings"

	"nusashell/domain"
	"nusashell/resources"
)

func (a *App) describeMediaAttachment(ctx context.Context, providerID, model, prompt string, att domain.Attachment, maxTokens int) (string, error) {
	provider, apiKey, ok := a.resolveFallbackProvider(providerID)
	if !ok {
		return "", fmt.Errorf("provider %q not found or disabled", a.providerNameByID(providerID))
	}
	if a.Factory == nil {
		return "", fmt.Errorf("Failed to initialize %s fallback adapter.", describeKind(att.Type))
	}
	rawAdapter, err := a.Factory(ctx, provider, apiKey)
	if err != nil {
		return "", fmt.Errorf("Failed to initialize %s fallback adapter.", describeKind(att.Type))
	}
	adapter := NewProviderContext(provider, rawAdapter)
	switch att.Type {
	case "audio":
		return a.describeOneAudio(ctx, adapter, model, att, prompt)
	case "video":
		return a.describeOneVideo(ctx, adapter, model, att, prompt)
	default:
		return a.describeOneImage(ctx, adapter, a.providerNameByID(providerID), model, att, prompt, maxTokens)
	}
}

func describeKind(attType string) string {
	switch attType {
	case "audio":
		return "audio"
	case "video":
		return "video"
	default:
		return "vision"
	}
}

func (a *App) resolveFallbackProvider(providerID string) (*domain.Provider, string, bool) {
	if a == nil || a.Providers == nil {
		return nil, "", false
	}
	for _, p := range a.Providers.List() {
		if p.ID == providerID && p.Enabled {
			key := ""
			if a.Credentials != nil {
				key, _, _ = a.Credentials.Get(p.ID)
			}
			return p, key, true
		}
	}
	return nil, "", false
}

func (a *App) describeOneImage(ctx context.Context, adapter ProviderContext, providerName, model string, image domain.Attachment, prompt string, maxTokens int) (string, error) {
	if strings.TrimSpace(prompt) == "" {
		prompt = resources.DescribeImagePrompt()
	}
	if maxTokens <= 0 {
		maxTokens = domain.DefaultSettings().MaxOutputTokens
	}
	req := ChatRequest{
		Model:  model,
		System: resources.ImageVisionSystemPrompt(),
		Messages: []ChatMessage{
			{
				Role:        "user",
				Content:     prompt,
				Attachments: []domain.Attachment{image},
			},
		},
		MaxTokens: maxTokens,
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
		a.log("warn", "vision", "empty description from vision model %s/%s: stop_reason=%q content_len=%d reasoning_len=%d usage=%+v tool_calls=%d",
			providerName, model, resp.StopReason, len(resp.Content), len(resp.Reasoning), resp.Usage, len(resp.ToolCalls))
		return "", fmt.Errorf("empty description from vision model %s/%s (stop_reason=%q, content=%d chars, reasoning=%d chars, tool_calls=%d) — the model produced no usable output; check the model supports vision input and the max_output_tokens budget is sufficient",
			providerName, model, resp.StopReason, len(resp.Content), len(resp.Reasoning), len(resp.ToolCalls))
	}
	return description, nil
}

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

func (a *App) describeOneVideo(ctx context.Context, adapter ProviderContext, model string, video domain.Attachment, prompt string) (string, error) {
	req := ChatRequest{
		Model:  model,
		System: resources.VideoVisionSystemPrompt(),
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

func (a *App) enrichWithVisionDescriptions(ctx context.Context, conversation *domain.Conversation, _ string, settings domain.Settings) *domain.Conversation {
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
	pending := domain.UndescribedMediaIndexes(userMsg.Attachments, "image", mediaDescPrefixVision)
	if len(pending) == 0 {
		return conversation
	}

	a.log("info", "vision", "describing %d image(s) via fallback model %s/%s for non-vision chat model",
		len(pending), a.providerNameByID(settings.VisionProviderID), settings.VisionModelID)

	described := a.describeImagesWithFallback(ctx, settings, userMsg.Attachments)
	if len(described) <= len(userMsg.Attachments) {
		return conversation
	}

	repo, err := a.loadRepo(conversation.ID)
	if err != nil {
		a.log("warn", "vision", "failed to load conversation for image descriptions: %v", err)
		return conversation
	}
	a.updateMessage(repo.Conversation(), userMsg.ID, func(msg *domain.Message) {
		msg.Attachments = described
	})
	if err := repo.Save(); err != nil {
		a.log("warn", "vision", "failed to persist image descriptions: %v", err)
		return conversation
	}

	reloaded, err := a.Conversations.Get(conversation.ID)
	if err != nil {
		return conversation
	}
	return reloaded
}

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
	pending := domain.UndescribedMediaIndexes(userMsg.Attachments, "audio", mediaDescPrefixAudio)
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
	pending := domain.UndescribedMediaIndexes(userMsg.Attachments, "video", mediaDescPrefixVideo)
	if len(pending) == 0 {
		return conversation
	}

	a.log("info", "video", "describing %d video file(s) via fallback model %s/%s for non-video chat model",
		len(pending), a.providerNameByID(settings.VideoProviderID), settings.VideoModelID)

	described := a.describeVideosWithFallback(ctx, settings, userMsg.Attachments)
	if len(described) <= len(userMsg.Attachments) {
		return conversation
	}

	repo, err := a.loadRepo(conversation.ID)
	if err != nil {
		a.log("warn", "video", "failed to load conversation for video descriptions: %v", err)
		return conversation
	}
	a.updateMessage(repo.Conversation(), userMsg.ID, func(msg *domain.Message) {
		msg.Attachments = described
	})
	if err := repo.Save(); err != nil {
		a.log("warn", "video", "failed to persist video descriptions: %v", err)
		return conversation
	}

	reloaded, err := a.Conversations.Get(conversation.ID)
	if err != nil {
		return conversation
	}
	return reloaded
}
