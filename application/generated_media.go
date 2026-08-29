package application

import (
	"encoding/json"
	"strings"

	"nusashell/application/service/generatedmedia"
	"nusashell/domain"
)

// saveGeneratedMedia is the thin App wrapper around generatedmedia.Save.
// It delegates to the leaf package so the media generators depend on a
// small package instead of the whole application package.
func (a *App) saveGeneratedMedia(conversationID, baseName, kind string, data []byte, inline bool) (domain.Attachment, string, error) {
	return generatedmedia.Save(a.Attachments, conversationID, baseName, kind, data, inline)
}

// executeGenerateMedia routes the unified generate_media tool call to the
// mode-specific executor based on media_type. Legacy tool names from older
// conversations (generate_image / generate_speech / generate_video) remain
// accepted as aliases so replayed history keeps running.
func (a *App) executeGenerateMedia(run *TurnRun, toolCall domain.ToolCall, settings domain.Settings) (string, []domain.Attachment, error) {
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
		return a.executeGenerateSpeech(run, toolCall, settings)
	case "video":
		return a.executeGenerateVideo(run, toolCall, settings)
	default:
		return a.executeGenerateImage(run, toolCall, settings)
	}
}
