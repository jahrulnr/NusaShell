// Package attachments validates and converts wire-level attachment DTOs into
// domain Attachment values. Extracted from the application root so the agent
// runner and media generators depend on a small leaf package instead of the
// whole application package.
package attachments

import (
	"bytes"
	"encoding/base64"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"nusashell/contracts"
	"nusashell/domain"
)

const (
	MaxAttachmentsPerTurn = 4
	MaxAttachmentBytes    = 4 * 1024 * 1024
)

// AttachmentsFromDTO converts a slice of wire attachment DTOs into domain
// Attachments, enforcing the per-turn count limit. Returns a contracts-level
// validation error when the input is rejected.
func AttachmentsFromDTO(input []contracts.AttachmentDTO) ([]domain.Attachment, *contracts.RPCError) {
	if len(input) > MaxAttachmentsPerTurn {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "a turn can include up to 4 attachments"}
	}
	attachments := make([]domain.Attachment, 0, len(input))
	for _, item := range input {
		attachment, err := AttachmentFromDTO(item)
		if err != nil {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
		}
		attachments = append(attachments, attachment)
	}
	return attachments, nil
}

func AttachmentFromDTO(item contracts.AttachmentDTO) (domain.Attachment, error) {
	attachment := domain.Attachment{
		Type: strings.TrimSpace(item.Type), Name: strings.TrimSpace(item.Name),
		MediaType: strings.TrimSpace(item.MediaType), Content: item.Content, DataURL: item.DataURL,
		FilePath: strings.TrimSpace(item.FilePath),
	}
	if attachment.Name == "" {
		return domain.Attachment{}, errAttachment("attachment name is required")
	}
	switch attachment.Type {
	case "text":
		if attachment.MediaType != "text/plain" || attachment.DataURL != "" {
			return domain.Attachment{}, errAttachment("text attachment must contain UTF-8 text")
		}
		if !utf8.ValidString(attachment.Content) {
			return domain.Attachment{}, errAttachment("text attachment must contain UTF-8 text")
		}
		if len([]byte(attachment.Content)) > MaxAttachmentBytes {
			return domain.Attachment{}, errAttachment(attachment.Name + " is larger than 4 MiB")
		}
	case "image":
		switch attachment.MediaType {
		case "image/png", "image/jpeg", "image/gif", "image/webp":
		default:
			return domain.Attachment{}, errAttachment("unsupported image attachment type")
		}
		if err := validateDataURL(attachment.DataURL, attachment.MediaType, attachment.Name); err != nil {
			return domain.Attachment{}, err
		}
	case "file":
		if attachment.MediaType != "application/pdf" {
			return domain.Attachment{}, errAttachment("unsupported file attachment type")
		}
		if err := validateDataURL(attachment.DataURL, attachment.MediaType, attachment.Name); err != nil {
			return domain.Attachment{}, err
		}
	case "folder":
		// Folder attachments are path-only references (no bytes). The agent
		// can use file tools to explore the directory. FilePath must be a
		// non-empty absolute path.
		if attachment.FilePath == "" {
			return domain.Attachment{}, errAttachment("folder attachment requires a file_path")
		}
		if !filepath.IsAbs(attachment.FilePath) {
			return domain.Attachment{}, errAttachment("folder attachment file_path must be absolute")
		}
		attachment.MediaType = "inode/directory"
	default:
		return domain.Attachment{}, errAttachment("unsupported attachment type")
	}
	return attachment, nil
}

type errAttachment string

func (e errAttachment) Error() string { return string(e) }

func validateDataURL(dataURL, mediaType, name string) error {
	prefix := "data:" + mediaType + ";base64,"
	if !strings.HasPrefix(dataURL, prefix) {
		return errAttachment("invalid data for " + name)
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(dataURL, prefix))
	if err != nil || len(data) == 0 {
		return errAttachment("invalid data for " + name)
	}
	if len(data) > MaxAttachmentBytes {
		return errAttachment(name + " is larger than 4 MiB")
	}
	if sniffed := SniffMediaType(data); sniffed != mediaType {
		return errAttachment("invalid data for " + name)
	}
	return nil
}

// SniffMediaType identifies common image/document formats by their binary
// magic numbers. Returns the media type or "" when unknown.
func SniffMediaType(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}):
		return "image/png"
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return "image/jpeg"
	case bytes.HasPrefix(data, []byte("GIF8")):
		return "image/gif"
	case bytes.HasPrefix(data, []byte("%PDF-")):
		return "application/pdf"
	case len(data) >= 12 && bytes.HasPrefix(data, []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "image/webp"
	default:
		return ""
	}
}
