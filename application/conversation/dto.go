package conversation

import (
	"nusashell/application/service/toolpresentation"
	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

// ConvDTO maps a conversation aggregate to the wire list/get shape.
func ConvDTO(c *domain.Conversation) contracts.ConversationDTO {
	return contracts.ConversationDTO{
		ID:              c.ID,
		Title:           c.Title,
		CreatedAt:       clock.NewTime(c.CreatedAt).Format(timeRFC3339),
		UpdatedAt:       clock.NewTime(c.UpdatedAt).Format(timeRFC3339),
		MessageCount:    len(c.Messages),
		Model:           c.Model,
		Effort:          c.Effort,
		ProviderRoute:   c.ProviderRoute,
		Status:          c.Status,
		Workspace:       c.Workspace,
		ChunkCount:      c.ChunkCount,
		EstimatedTokens: c.EstimatedTokens,
		ContextTokens:   c.ContextTokens,
	}
}

const timeRFC3339 = "2006-01-02T15:04:05Z07:00"

// MsgDTO maps a persisted message to the UI DTO. Hydration-only messages
// return a zero DTO (empty ID) so callers can skip them.
func MsgDTO(m domain.Message) contracts.MessageDTO {
	// Filter hydration tool calls from the DTO sent to the UI.
	// If ALL tool calls in a message are hydration calls and there is
	// no content or reasoning, the entire message is hidden from the UI.
	if domain.IsHydrationMessage(m) {
		return contracts.MessageDTO{}
	}
	m = domain.FilterHydrationToolCalls(m)
	dto := contracts.MessageDTO{
		ID:             m.ID,
		Role:           string(m.Role),
		Content:        m.Content,
		Reasoning:      m.Reasoning,
		Model:          m.Model,
		ProviderID:     m.ProviderID,
		CreatedAt:      clock.NewTime(m.CreatedAt).Format(timeRFC3339),
		Status:         string(m.Status),
		Error:          m.Error,
		Steer:          m.Steer,
		ContextUpdated: m.ContextUpdated,
	}
	if m.Usage != nil {
		dto.Usage = &contracts.UsageDTO{
			InputTokens:  m.Usage.InputTokens,
			OutputTokens: m.Usage.OutputTokens,
			CacheRead:    m.Usage.CacheRead,
			CacheWrite:   m.Usage.CacheWrite,
		}
	}
	for _, tc := range m.ToolCalls {
		dto.ToolCalls = append(dto.ToolCalls, ToolCallDTO(tc))
	}
	for _, attachment := range m.Attachments {
		dto.Attachments = append(dto.Attachments, contracts.AttachmentDTO{
			Type: attachment.Type, Name: attachment.Name, MediaType: attachment.MediaType,
			Content: attachment.Content, DataURL: attachment.DataURL, FilePath: attachment.FilePath,
		})
	}
	for _, stepSrc := range m.Steps {
		step := contracts.MessageStepDTO{
			Type:    string(stepSrc.Type),
			Content: stepSrc.Content,
		}
		for _, tc := range stepSrc.ToolCalls {
			step.ToolCalls = append(step.ToolCalls, ToolCallDTO(tc))
		}
		dto.Steps = append(dto.Steps, step)
	}
	return dto
}

// ToolCallDTO maps a tool call, including presentation and output attachments.
func ToolCallDTO(tc domain.ToolCall) contracts.ToolCallDTO {
	dto := contracts.ToolCallDTO{
		ID:           tc.ID,
		Name:         tc.Name,
		Args:         []byte(tc.Args),
		Status:       string(tc.Status),
		Output:       tc.Output,
		Opaque:       tc.Opaque,
		Presentation: toolpresentation.ToolPresentationDTO(tc),
	}
	dto.OutputAttachments = toolpresentation.ToolAttachmentDTOs(tc.OutputAttachments)
	return dto
}
