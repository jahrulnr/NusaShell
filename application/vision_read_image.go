package application

import (
	"encoding/json"
	"fmt"
	"path/filepath"
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
func (a *App) executeReadImage(run *TurnRun, toolCall domain.ToolCall, caps ModelCapabilities, settings domain.Settings) (string, []domain.Attachment, error) {
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

	image, err := findImageAttachmentByPath(conversation, path)
	if err != nil {
		return "error: " + err.Error(), nil, err
	}

	// Native fast path: vision model gets the image directly.
	if caps.Vision {
		question := strings.TrimSpace(args.Question)
		summary := "Image loaded into your context."
		if image.FilePath != "" {
			summary += " File path: " + image.FilePath
		}
		if question != "" {
			summary += " Question: " + question
		}
		return summary, []domain.Attachment{image}, nil
	}

	// Fallback path: describe via vision fallback model.
	if settings.VisionProviderID == "" || settings.VisionModelID == "" {
		msg := "This model does not support image input and no vision fallback model is configured. Ask the user to configure a vision fallback in settings, or switch to a vision-capable model."
		if image.FilePath != "" {
			msg += " The image is saved at: " + image.FilePath
		}
		return msg, nil, nil
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

	result := fmt.Sprintf("[Image description for %s]\n%s", image.Name, description)
	if image.FilePath != "" {
		result += "\n\nFile path: " + image.FilePath
	}
	return result, nil, nil
}

// findImageAttachmentByPath searches all user messages in the conversation for
// an image attachment matching the given absolute file path (case-insensitive).
func findImageAttachmentByPath(conversation *domain.Conversation, path string) (domain.Attachment, error) {
	target := strings.ToLower(strings.TrimSpace(path))
	for _, msg := range conversation.Messages {
		if msg.Role != domain.RoleUser {
			continue
		}
		for _, att := range msg.Attachments {
			if att.Type != "image" {
				continue
			}
			if att.FilePath != "" && strings.ToLower(att.FilePath) == target {
				return att, nil
			}
		}
	}
	return domain.Attachment{}, fmt.Errorf("image attachment with file_path %q not found in conversation", path)
}
