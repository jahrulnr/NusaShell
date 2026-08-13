package ai

import (
	"strings"

	"nusashell/domain"
)

func textAttachmentContent(attachment domain.Attachment) string {
	return "Attached text file: " + attachment.Name + "\n\n" + attachment.Content
}

func documentAttachmentContent(attachment domain.Attachment) string {
	return "[Attached document: " + attachment.Name + " (" + attachment.MediaType + ")]"
}

func dataURLBase64(dataURL string) string {
	_, data, ok := strings.Cut(dataURL, ",")
	if !ok {
		return ""
	}
	return data
}
