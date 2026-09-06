package media

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"nusashell/application/service/mediaread"
	"nusashell/domain"
	"nusashell/pkg/yamlmd"
)

// ExecuteReadImage handles the read_media tool call when the sniffed kind
// is "image". Native fast path returns the attachment; fallback describes
// via the injected Describe port.
func (s *Service) ExecuteReadImage(call Call, toolCall domain.ToolCall, caps Caps, settings domain.Settings) (string, []domain.Attachment, error) {
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

	image, err := mediaread.LoadMediaAttachment("image", path)
	if err != nil {
		return "error: " + err.Error(), nil, err
	}

	if caps.Vision {
		summary := "Image loaded."
		if image.FilePath != "" {
			summary = image.FilePath
		}
		return summary, []domain.Attachment{image}, nil
	}

	if settings.VisionProviderID == "" || settings.VisionModelID == "" {
		msg := "This model does not support image input and no vision fallback model is configured. Ask the user to configure a vision fallback in settings, or switch to a vision-capable model."
		if image.FilePath != "" {
			msg += " The image is saved at: " + image.FilePath
		}
		return msg, nil, nil
	}

	provider, _, ok := s.resolve(settings.VisionProviderID)
	if !ok {
		return "Vision fallback provider not found or disabled.", nil, fmt.Errorf("vision provider %q not found", s.name(settings.VisionProviderID))
	}

	question := strings.TrimSpace(args.Question)
	if question != "" {
		question = "Describe this image and answer the following question:\n" + question
	}

	maxOut := domain.ResolveMaxOutput(provider, settings.VisionModelID, settings)
	description, err := s.describe(call.ctx(), settings.VisionProviderID, settings.VisionModelID, question, image, maxOut)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "initialize") {
			return "Failed to initialize vision fallback adapter.", nil, err
		}
		return "Image description failed: " + err.Error(), nil, err
	}

	result := fmt.Sprintf("[Image description for %s]\n%s", image.Name, description)
	if image.FilePath != "" {
		result += "\n\nFile path: " + image.FilePath
	}
	meta := map[string]any{"type": "image", "name": image.Name}
	if image.FilePath != "" {
		meta["file_path"] = image.FilePath
	}
	return yamlmd.MD(meta, result), nil, nil
}
