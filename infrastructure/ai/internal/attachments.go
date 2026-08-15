package aiutil

import (
	"strings"

	"nusashell/domain"
)

// TextAttachmentContent renders a text attachment as inline content.
func TextAttachmentContent(attachment domain.Attachment) string {
	return "Attached text file: " + attachment.Name + "\n\n" + attachment.Content
}

// DocumentAttachmentContent renders a document attachment as a descriptive
// text marker (used by Chat Completions, which has no portable file part).
func DocumentAttachmentContent(attachment domain.Attachment) string {
	return "[Attached document: " + attachment.Name + " (" + attachment.MediaType + ")]"
}

// DataURLBase64 extracts the base64 payload from a data: URL.
func DataURLBase64(dataURL string) string {
	_, data, ok := strings.Cut(dataURL, ",")
	if !ok {
		return ""
	}
	return data
}
