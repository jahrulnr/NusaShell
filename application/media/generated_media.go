package media

import (
	"encoding/json"
	"strings"

	"nusashell/application/service/generatedmedia"
	"nusashell/domain"
)

// SaveGenerated is the thin wrapper around generatedmedia.Save.
func (s *Service) SaveGenerated(conversationID, baseName, kind string, data []byte, inline bool) (domain.Attachment, string, error) {
	return generatedmedia.Save(s.attachments, conversationID, baseName, kind, data, inline)
}

// ExecuteGenerateMedia routes the unified generate_media tool call to the
// mode-specific executor based on media_type. Legacy tool names from older
// conversations (generate_image / generate_speech / generate_video) remain
// accepted as aliases so replayed history keeps running.
func (s *Service) ExecuteGenerateMedia(call Call, toolCall domain.ToolCall, settings domain.Settings) (string, []domain.Attachment, error) {
	var head struct {
		MediaType string `json:"media_type"`
	}
	_ = json.Unmarshal([]byte(toolCall.Args), &head)
	mode := strings.ToLower(strings.TrimSpace(head.MediaType))
	if mode == "" {
		switch toolCall.Name { // legacy alias without an explicit media_type
		case "generate_video":
			mode = "video"
		case "generate_speech":
			mode = "speech"
		default:
			mode = "image"
		}
	}
	switch mode {
	case "speech":
		return s.ExecuteGenerateSpeech(call, toolCall, settings)
	case "video":
		return s.ExecuteGenerateVideo(call, toolCall, settings)
	default:
		return s.ExecuteGenerateImage(call, toolCall, settings)
	}
}
