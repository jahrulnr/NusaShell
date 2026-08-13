package application

import (
	"encoding/base64"
	"strings"

	"nusashell/contracts"
	"nusashell/domain"
)

const (
	maxAttachmentsPerTurn = 4
	maxAttachmentBytes    = 4 * 1024 * 1024
)

func attachmentsFromDTO(input []contracts.AttachmentDTO) ([]domain.Attachment, *contracts.RPCError) {
	if len(input) > maxAttachmentsPerTurn {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "a turn can include up to 4 attachments"}
	}
	attachments := make([]domain.Attachment, 0, len(input))
	for _, item := range input {
		attachment, err := attachmentFromDTO(item)
		if err != nil {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
		}
		attachments = append(attachments, attachment)
	}
	return attachments, nil
}

func attachmentFromDTO(item contracts.AttachmentDTO) (domain.Attachment, error) {
	attachment := domain.Attachment{
		Type: strings.TrimSpace(item.Type), Name: strings.TrimSpace(item.Name),
		MediaType: strings.TrimSpace(item.MediaType), Content: item.Content, DataURL: item.DataURL,
	}
	if attachment.Name == "" {
		return domain.Attachment{}, errAttachment("attachment name is required")
	}
	switch attachment.Type {
	case "text":
		if attachment.MediaType != "text/plain" || attachment.DataURL != "" {
			return domain.Attachment{}, errAttachment("text attachment must contain UTF-8 text")
		}
		if len([]byte(attachment.Content)) > maxAttachmentBytes {
			return domain.Attachment{}, errAttachment(attachment.Name + " is larger than 4 MiB")
		}
	case "image":
		if !allowedImageMediaType(attachment.MediaType) {
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
	default:
		return domain.Attachment{}, errAttachment("unsupported attachment type")
	}
	return attachment, nil
}

type errAttachment string

func (e errAttachment) Error() string { return string(e) }

func allowedImageMediaType(mediaType string) bool {
	switch mediaType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func validateDataURL(dataURL, mediaType, name string) error {
	prefix := "data:" + mediaType + ";base64,"
	if !strings.HasPrefix(dataURL, prefix) {
		return errAttachment("invalid data for " + name)
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(dataURL, prefix))
	if err != nil || len(data) == 0 {
		return errAttachment("invalid data for " + name)
	}
	if len(data) > maxAttachmentBytes {
		return errAttachment(name + " is larger than 4 MiB")
	}
	return nil
}
