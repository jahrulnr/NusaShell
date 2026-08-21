package aiutil

import (
	"strings"

	"nusashell/domain"
)

// TextAttachmentContent renders a text attachment as inline content.
// The leading newline keeps the header visually separated from the user's
// message text even when a provider concatenates adjacent text blocks
// without a separator. The header explicitly states that the full content
// follows, so models do not mistake the attachment for a dangling reference
// to a file they still need to obtain.
func TextAttachmentContent(attachment domain.Attachment) string {
	return "\n[Attached text file: " + attachment.Name + " - full content included below]\n\n" + attachment.Content
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
