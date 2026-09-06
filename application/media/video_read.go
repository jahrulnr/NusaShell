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

var defaultDescribeVideoPrompt = resources.DescribeVideoPrompt()

// ExecuteReadVideo handles the read_media tool call when the sniffed kind
// is "video". Native fast path returns the attachment; fallback describes
// via the injected Describe port.
func (s *Service) ExecuteReadVideo(call Call, toolCall domain.ToolCall, caps Caps, settings domain.Settings) (string, []domain.Attachment, error) {
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

	video, err := mediaread.LoadMediaAttachment("video", path)
	if err != nil {
		return "error: " + err.Error(), nil, err
	}

	if caps.Video {
		summary := "Video loaded into your context."
		if video.FilePath != "" {
			summary += " File path: " + video.FilePath
		}
		return summary, []domain.Attachment{video}, nil
	}

	if settings.VideoProviderID == "" || settings.VideoModelID == "" {
		msg := "This model does not support video input and no video fallback model is configured. Ask the user to configure a video fallback in settings, or switch to a video-capable model."
		if video.FilePath != "" {
			msg += " The video file is saved at: " + video.FilePath
		}
		return msg, nil, nil
	}

	if _, _, ok := s.resolve(settings.VideoProviderID); !ok {
		return "Video fallback provider not found or disabled.", nil, fmt.Errorf("video provider %q not found", s.name(settings.VideoProviderID))
	}

	question := strings.TrimSpace(args.Question)
	if question == "" {
		question = defaultDescribeVideoPrompt
	} else {
		question = "Describe this video and answer the following question:\n" + question
	}

	description, err := s.describe(call.ctx(), settings.VideoProviderID, settings.VideoModelID, question, video, 1000)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "initialize") {
			return "Failed to initialize video fallback adapter.", nil, err
		}
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
	return yamlmd.MD(meta, result), nil, nil
}

// DescribeVideosWithFallback converts video attachments into text
// descriptions using the configured video fallback model.
func (s *Service) DescribeVideosWithFallback(ctx context.Context, settings domain.Settings, atts []domain.Attachment) []domain.Attachment {
	if len(atts) == 0 {
		return atts
	}
	if settings.VideoProviderID == "" || settings.VideoModelID == "" {
		return atts
	}
	videoIdxs := domain.UndescribedMediaIndexes(atts, "video", domain.MediaDescPrefixVideo)
	if len(videoIdxs) == 0 {
		return atts
	}

	if _, _, ok := s.resolve(settings.VideoProviderID); !ok {
		s.write("warn", "video", "video fallback provider %q not found or disabled; skipping video description", s.name(settings.VideoProviderID))
		return atts
	}

	out := make([]domain.Attachment, 0, len(atts)+len(videoIdxs))
	out = append(out, atts...)
	for _, idx := range videoIdxs {
		vid := atts[idx]
		prompt := defaultDescribeVideoPrompt
		description, err := s.describe(ctx, settings.VideoProviderID, settings.VideoModelID, prompt, vid, 1000)
		if err != nil {
			s.write("warn", "video", "video description failed for %q: %v", vid.Name, err)
			continue
		}
		out = append(out, domain.Attachment{
			Type:      "text",
			Name:      domain.MediaDescPrefixVideo + vid.Name,
			MediaType: "text/plain",
			Content:   fmt.Sprintf("[Video description for %s]\n%s", vid.Name, description),
		})
	}
	return out
}
