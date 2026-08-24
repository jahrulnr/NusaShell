package application

import (
	"context"
	"fmt"
	"strings"

	"nusashell/domain"
)

// describeImagesWithFallback converts image attachments into text descriptions
// using the configured vision fallback model. It is called when the active chat
// model does not support vision but the user has attached images and configured
// a VisionProviderID + VisionModelID in settings.
//
// The original image attachments are preserved on the message so that a later
// switch to a vision-capable model can still see them. The generated
// descriptions are appended as additional "text" attachments so the non-vision
// model receives the content via chatMessages (which strips images but keeps
// text attachments).
//
// If the fallback is not configured or fails, the original attachments are
// returned unchanged and the caller falls back to the placeholder behavior
// in chatMessages.
func (a *App) describeImagesWithFallback(ctx context.Context, settings domain.Settings, attachments []domain.Attachment) []domain.Attachment {
	if len(attachments) == 0 {
		return attachments
	}
	if settings.VisionProviderID == "" || settings.VisionModelID == "" {
		return attachments
	}
	// Collect image attachments; if none, nothing to do.
	var imageIdxs []int
	for i, att := range attachments {
		if att.Type == "image" {
			imageIdxs = append(imageIdxs, i)
		}
	}
	if len(imageIdxs) == 0 {
		return attachments
	}

	provider, apiKey, ok := a.resolveVisionProvider(settings.VisionProviderID)
	if !ok {
		a.log("warn", "vision", "vision fallback provider %q not found or disabled; skipping image description", a.providerNameByID(settings.VisionProviderID))
		return attachments
	}
	adapter, err := a.Factory(ctx, provider, apiKey)
	if err != nil {
		a.log("warn", "vision", "failed to build vision fallback adapter: %v", err)
		return attachments
	}

	out := make([]domain.Attachment, 0, len(attachments)+len(imageIdxs))
	out = append(out, attachments...)
	for _, idx := range imageIdxs {
		img := attachments[idx]
		description, err := a.describeOneImage(ctx, adapter, a.providerNameByID(settings.VisionProviderID), settings.VisionModelID, img, defaultDescribeImagePrompt)
		if err != nil {
			a.log("warn", "vision", "image description failed for %q: %v", img.Name, err)
			continue
		}
		out = append(out, domain.Attachment{
			Type:      "text",
			Name:      "vision:" + img.Name,
			MediaType: "text/plain",
			Content:   fmt.Sprintf("[Image description for %s]\n%s", img.Name, description),
		})
	}
	return out
}

func (a *App) resolveVisionProvider(providerID string) (*domain.Provider, string, bool) {
	return a.resolveFallbackProvider(providerID)
}

// resolveFallbackProvider looks up an enabled provider by ID and returns
// its API key. Used by all modality fallbacks (vision, audio, video).
func (a *App) resolveFallbackProvider(providerID string) (*domain.Provider, string, bool) {
	for _, p := range a.Providers.List() {
		if p.ID == providerID && p.Enabled {
			key, has, _ := a.Credentials.Get(p.ID)
			if !has && requiresKey(p.Kind) {
				return nil, "", false
			}
			return p, key, true
		}
	}
	return nil, "", false
}

// enrichWithVisionDescriptions finds the latest user message in the
// conversation, describes its image attachments via the vision fallback
// model, appends the descriptions as text attachments, persists the
// updated message, and returns a reloaded conversation so chatMessages
// sees the descriptions. If no images are present or description fails,
// the conversation is returned unchanged.
func (a *App) enrichWithVisionDescriptions(ctx context.Context, conversation *domain.Conversation, _ string, settings domain.Settings) *domain.Conversation {
	// Find the latest user message (the one just before the pending assistant message).
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
	hasImage := false
	for _, att := range userMsg.Attachments {
		if att.Type == "image" {
			hasImage = true
			break
		}
	}
	if !hasImage {
		return conversation
	}

	a.log("info", "vision", "describing %d image(s) via fallback model %s/%s for non-vision chat model",
		countImages(userMsg.Attachments), a.providerNameByID(settings.VisionProviderID), settings.VisionModelID)

	described := a.describeImagesWithFallback(ctx, settings, userMsg.Attachments)
	if len(described) <= len(userMsg.Attachments) {
		// No descriptions were added (fallback failed or not configured).
		return conversation
	}

	// Update the message in the conversation store.
	a.updateMessage(conversation, userMsg.ID, func(msg *domain.Message) {
		msg.Attachments = described
	})
	if err := a.Conversations.Save(conversation); err != nil {
		a.log("warn", "vision", "failed to persist image descriptions: %v", err)
		return conversation
	}

	// Reload so the in-memory conversation reflects the persisted state.
	reloaded, err := a.Conversations.Get(conversation.ID)
	if err != nil {
		return conversation
	}
	return reloaded
}

func countImages(atts []domain.Attachment) int {
	return countAttachmentsByType(atts, "image")
}

// defaultDescribeImagePrompt is the description request used when the image
// read/fallback path has no explicit question. Kept as a single constant so
// all call sites share one string.
const defaultDescribeImagePrompt = "Describe this image concisely. Focus on visible objects, text, people, settings, and any notable details. Keep it factual and under 200 words."

func (a *App) describeOneImage(ctx context.Context, adapter AIProvider, providerName, model string, image domain.Attachment, prompt string) (string, error) {
	if strings.TrimSpace(prompt) == "" {
		prompt = defaultDescribeImagePrompt
	}
	req := ChatRequest{
		Model:  model,
		System: "Describe the attached image accurately and concisely for a text-only model that cannot see it.",
		Messages: []ChatMessage{
			{
				Role:        "user",
				Content:     prompt,
				Attachments: []domain.Attachment{image},
			},
		},
		MaxTokens: 400,
	}
	resp, err := a.completeWithRetry(ctx, adapter, req)
	if err != nil {
		return "", err
	}
	// Reasoning models (e.g. dots-3-note, DeepSeek) may put the description
	// in the reasoning field instead of content. Fall back to reasoning when
	// content is empty so the vision fallback still produces a description.
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
