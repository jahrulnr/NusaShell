package aiutil

import (
	"encoding/json"
	"fmt"

	"nusashell/domain"
)

// MustJSON marshals v to a json.RawMessage. On error it returns an empty
// JSON array, matching the original behaviour where a failed marshal of
// content blocks should never panic the caller.
func MustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("[]")
	}
	return b
}

// ParseFloat parses a pricing string like "3.0" (USD per MTok) into a
// float64.
func ParseFloat(s string) (float64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	var v float64
	_, err := fmt.Sscanf(s, "%f", &v)
	return v, err
}

// Deref safely dereferences a *string, returning "" for nil.
func Deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// StrJSON encodes a plain string as a JSON string RawMessage (message
// content in the Responses API is a string, not a block array).
func StrJSON(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

// SanitizeToolName delegates to domain.SanitizeToolName.
func SanitizeToolName(name string) string {
	return domain.SanitizeToolName(name)
}

// TextAttachmentContent delegates to domain.TextAttachmentContent.
func TextAttachmentContent(att domain.Attachment) string {
	return domain.TextAttachmentContent(att)
}

// DocumentAttachmentContent delegates to domain.DocumentAttachmentContent.
func DocumentAttachmentContent(att domain.Attachment) string {
	return domain.DocumentAttachmentContent(att)
}

// DataURLBase64 extracts the base64 payload from a data: URL.
func DataURLBase64(dataURL string) string {
	return domain.DataURLBase64(dataURL)
}
