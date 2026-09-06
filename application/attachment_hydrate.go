package application

import (
	"encoding/base64"
	"fmt"
	"strings"

	"nusashell/application/service/attachments"
	"nusashell/domain"
)

func decodeAttachmentDataURL(dataURL string) ([]byte, error) {
	_, data, ok := strings.Cut(dataURL, ",")
	if !ok {
		return nil, fmt.Errorf("invalid data URL")
	}
	return base64.StdEncoding.DecodeString(data)
}

func (a *App) hydrateAttachmentDataURLs(atts []domain.Attachment) []domain.Attachment {
	if a == nil || a.Attachments == nil || len(atts) == 0 {
		return atts
	}
	out := make([]domain.Attachment, len(atts))
	copy(out, atts)
	for i := range out {
		if out[i].DataURL != "" || out[i].FilePath == "" {
			continue
		}
		data, err := a.Attachments.ReadFile(out[i].FilePath)
		if err != nil || len(data) == 0 {
			continue
		}
		mediaType := out[i].MediaType
		if mediaType == "" {
			mediaType = attachments.SniffMediaType(data)
		}
		if mediaType == "" {
			continue
		}
		out[i].MediaType = mediaType
		out[i].DataURL = "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data)
	}
	return out
}

func (a *App) chatMessagesForProvider(c *domain.Conversation, pendingMsgID string, caps ModelCapabilities) []ChatMessage {
	msgs := chatMessages(c, pendingMsgID, caps)
	if a == nil {
		return msgs
	}
	for i := range msgs {
		if len(msgs[i].Attachments) > 0 {
			msgs[i].Attachments = a.hydrateAttachmentDataURLs(msgs[i].Attachments)
		}
		if msgs[i].ToolResult != nil && len(msgs[i].ToolResult.Attachments) > 0 {
			msgs[i].ToolResult.Attachments = a.hydrateAttachmentDataURLs(msgs[i].ToolResult.Attachments)
		}
	}
	return msgs
}
