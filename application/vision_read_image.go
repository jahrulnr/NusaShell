package application

import (
	"encoding/json"
	"fmt"
	"strings"

	"nusashell/domain"
)

// executeReadImage handles the read_image tool call. It finds the requested
// image attachment in the conversation history, then either:
//   - Native fast path (vision model): returns the image directly as a tool
//     result attachment so the model can see it in the next round.
//   - Fallback path (non-vision model + fallback configured): describes the
//     image via the vision fallback model and returns the text description.
//   - No fallback (non-vision model, no fallback): returns an error message
//     explaining that the model cannot see images and no fallback is configured.
func (a *App) executeReadImage(run *TurnRun, toolCall domain.ToolCall, supportsVision bool, settings domain.Settings) (string, []domain.Attachment, error) {
	var args struct {
		AttachmentName string `json:"attachment_name"`
		Question       string `json:"question"`
	}
	if err := json.Unmarshal([]byte(toolCall.Args), &args); err != nil {
		return "error: invalid arguments", nil, fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(args.AttachmentName) == "" {
		return "error: attachment_name is required", nil, fmt.Errorf("attachment_name is required")
	}

	conversation, err := a.Conversations.Get(run.ConversationID)
	if err != nil {
		return "error: conversation not found", nil, err
	}

	image, err := findImageAttachment(conversation, args.AttachmentName)
	if err != nil {
		return "error: " + err.Error(), nil, err
	}

	// Native fast path: vision model gets the image directly.
	if supportsVision {
		question := strings.TrimSpace(args.Question)
		summary := "Image loaded into your context."
		if question != "" {
			summary += " Question: " + question
		}
		return summary, []domain.Attachment{image}, nil
	}

	// Fallback path: describe via vision fallback model.
	if settings.VisionProviderID == "" || settings.VisionModelID == "" {
		return "This model does not support image input and no vision fallback model is configured. Ask the user to configure a vision fallback in settings, or switch to a vision-capable model.", nil, nil
	}

	provider, apiKey, ok := a.resolveVisionProvider(settings.VisionProviderID)
	if !ok {
		return "Vision fallback provider not found or disabled.", nil, fmt.Errorf("vision provider %q not found", settings.VisionProviderID)
	}
	adapter, err := a.Factory(run.Ctx, provider, apiKey)
	if err != nil {
		return "Failed to initialize vision fallback adapter.", nil, err
	}

	question := strings.TrimSpace(args.Question)
	if question == "" {
		question = "Describe this image concisely. Focus on visible objects, text, people, settings, and any notable details. Keep it factual and under 200 words."
	} else {
		question = "Describe this image and answer the following question:\n" + question
	}

	description, err := a.describeOneImage(run.Ctx, adapter, settings.VisionModelID, image)
	if err != nil {
		return "Image description failed: " + err.Error(), nil, err
	}

	return fmt.Sprintf("[Image description for %s]\n%s", image.Name, description), nil, nil
}

// findImageAttachment searches all user messages in the conversation for an
// image attachment matching the given name (case-insensitive).
func findImageAttachment(conversation *domain.Conversation, name string) (domain.Attachment, error) {
	target := strings.ToLower(strings.TrimSpace(name))
	for _, msg := range conversation.Messages {
		if msg.Role != domain.RoleUser {
			continue
		}
		for _, att := range msg.Attachments {
			if att.Type != "image" {
				continue
			}
			if strings.ToLower(att.Name) == target {
				return att, nil
			}
		}
	}
	return domain.Attachment{}, fmt.Errorf("image attachment %q not found in conversation", name)
}
