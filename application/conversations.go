package application

import (
	"context"
	"errors"
	"strings"

	"nusashell/contracts"
	"nusashell/domain"
)

func convDTO(c *domain.Conversation) contracts.ConversationDTO {
	return contracts.ConversationDTO{
		ID:           c.ID,
		Title:        c.Title,
		CreatedAt:    c.CreatedAt.Format(timeRFC3339),
		UpdatedAt:    c.UpdatedAt.Format(timeRFC3339),
		MessageCount: len(c.Messages),
		Model:        c.Model,
		Status:       c.Status,
		Workspace:    c.Workspace,
	}
}

const timeRFC3339 = "2006-01-02T15:04:05Z07:00"

func msgDTO(m domain.Message) contracts.MessageDTO {
	dto := contracts.MessageDTO{
		ID:        m.ID,
		Role:      string(m.Role),
		Content:   m.Content,
		Reasoning: m.Reasoning,
		Model:     m.Model,
		CreatedAt: m.CreatedAt.Format(timeRFC3339),
		Status:    string(m.Status),
		Error:     m.Error,
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
		dto.ToolCalls = append(dto.ToolCalls, contracts.ToolCallDTO{
			ID:     tc.ID,
			Name:   tc.Name,
			Args:   []byte(tc.Args),
			Status: string(tc.Status),
			Output: tc.Output,
		})
	}
	for _, attachment := range m.Attachments {
		dto.Attachments = append(dto.Attachments, contracts.AttachmentDTO{
			Type: attachment.Type, Name: attachment.Name, MediaType: attachment.MediaType,
			Content: attachment.Content, DataURL: attachment.DataURL,
		})
	}
	for _, s := range m.Steps {
		step := contracts.MessageStepDTO{
			Type:    string(s.Type),
			Content: s.Content,
		}
		for _, tc := range s.ToolCalls {
			step.ToolCalls = append(step.ToolCalls, contracts.ToolCallDTO{
				ID:     tc.ID,
				Name:   tc.Name,
				Args:   []byte(tc.Args),
				Status: string(tc.Status),
				Output: tc.Output,
			})
		}
		dto.Steps = append(dto.Steps, step)
	}
	return dto
}

func (a *App) handleConversationsList() (any, *contracts.RPCError) {
	list := a.Conversations.List()
	out := make([]contracts.ConversationDTO, 0, len(list))
	for _, c := range list {
		out = append(out, convDTO(c))
	}
	return contracts.ConversationsListResult{Conversations: out}, nil
}

func (a *App) handleConversationsCreate(req contracts.ConversationCreateRequest) (any, *contracts.RPCError) {
	c := domain.NewConversation(domain.NewID("conv"), strings.TrimSpace(req.Title))
	if c.Title == "" {
		c.Title = "Untitled"
	}
	if err := a.Conversations.Save(c); err != nil {
		return nil, rpcInternal(err)
	}
	a.log("info", "agent", "conversation created: %s", c.ID)
	return contracts.ConversationGetResult{Conversation: convDTO(c)}, nil
}

func (a *App) handleConversationsGet(req contracts.ConversationIDRequest) (any, *contracts.RPCError) {
	c, rpcErr := a.getConversation(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	msgs := make([]contracts.MessageDTO, 0, len(c.Messages))
	for _, m := range c.Messages {
		msgs = append(msgs, msgDTO(m))
	}
	return contracts.ConversationGetResult{Conversation: convDTO(c), Messages: msgs}, nil
}

func (a *App) handleConversationsRename(req contracts.ConversationRenameRequest) (any, *contracts.RPCError) {
	c, rpcErr := a.getConversation(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "title is required"}
	}
	c.Title = title
	c.Touch()
	if err := a.Conversations.Save(c); err != nil {
		return nil, rpcInternal(err)
	}
	return contracts.ConversationGetResult{Conversation: convDTO(c)}, nil
}

func (a *App) handleConversationsDelete(req contracts.ConversationIDRequest) (any, *contracts.RPCError) {
	if _, rpcErr := a.getConversation(req.ID); rpcErr != nil {
		return nil, rpcErr
	}
	if err := a.Conversations.Delete(req.ID); err != nil {
		return nil, rpcInternal(err)
	}
	a.log("info", "agent", "conversation deleted: %s", req.ID)
	return map[string]bool{"ok": true}, nil
}

func (a *App) handleConversationsPickWorkspace(req contracts.ConversationIDRequest) (any, *contracts.RPCError) {
	c, rpcErr := a.getConversation(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if a.WorkspacePicker == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "workspace folder picker is unavailable"}
	}
	workspace, err := a.WorkspacePicker.Choose(context.Background())
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return contracts.ConversationGetResult{Conversation: convDTO(c)}, nil
		}
		return nil, rpcInternal(err)
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return contracts.ConversationGetResult{Conversation: convDTO(c)}, nil
	}
	c.Workspace = workspace
	c.Touch()
	if err := a.Conversations.Save(c); err != nil {
		return nil, rpcInternal(err)
	}
	a.log("info", "agent", "workspace selected for conversation %s", c.ID)
	return contracts.ConversationGetResult{Conversation: convDTO(c)}, nil
}

func (a *App) getConversation(id string) (*domain.Conversation, *contracts.RPCError) {
	if strings.TrimSpace(id) == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "conversation id is required"}
	}
	c, err := a.Conversations.Get(id)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	return c, nil
}
