package application

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"nusashell/contracts"
	"nusashell/domain"
)

// isHydrationMessage returns true when a message is a pure hydration
// checkpoint — all tool calls have the "hydrate-" prefix and there is no
// visible content or reasoning. These messages are hidden from the UI.
func isHydrationMessage(m domain.Message) bool {
	return domain.IsHydrationMessage(m)
}

// filterHydrationToolCalls strips hydration tool calls from a message that
// has both real and hydration tool calls (mixed). Returns the message
// unchanged if it has no hydration tool calls.
func filterHydrationToolCalls(m domain.Message) domain.Message {
	return domain.FilterHydrationToolCalls(m)
}

func convDTO(c *domain.Conversation) contracts.ConversationDTO {
	return contracts.ConversationDTO{
		ID:              c.ID,
		Title:           c.Title,
		CreatedAt:       c.CreatedAt.Format(timeRFC3339),
		UpdatedAt:       c.UpdatedAt.Format(timeRFC3339),
		MessageCount:    len(c.Messages),
		Model:           c.Model,
		Effort:          c.Effort,
		Status:          c.Status,
		Workspace:       c.Workspace,
		ChunkCount:      c.ChunkCount,
		EstimatedTokens: c.EstimatedTokens,
		ContextTokens:   c.ContextTokens,
	}
}

const timeRFC3339 = "2006-01-02T15:04:05Z07:00"

func msgDTO(m domain.Message) contracts.MessageDTO {
	// Filter hydration tool calls from the DTO sent to the UI.
	// If ALL tool calls in a message are hydration calls and there is
	// no content or reasoning, the entire message is hidden from the UI.
	if isHydrationMessage(m) {
		return contracts.MessageDTO{}
	}
	m = filterHydrationToolCalls(m)
	dto := contracts.MessageDTO{
		ID:             m.ID,
		Role:           string(m.Role),
		Content:        m.Content,
		Reasoning:      m.Reasoning,
		Model:          m.Model,
		ProviderID:     m.ProviderID,
		CreatedAt:      m.CreatedAt.Format(timeRFC3339),
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
		dto.ToolCalls = append(dto.ToolCalls, toolCallDTO(tc))
	}
	for _, attachment := range m.Attachments {
		dto.Attachments = append(dto.Attachments, contracts.AttachmentDTO{
			Type: attachment.Type, Name: attachment.Name, MediaType: attachment.MediaType,
			Content: attachment.Content, DataURL: attachment.DataURL, FilePath: attachment.FilePath,
		})
	}
	for _, s := range m.Steps {
		step := contracts.MessageStepDTO{
			Type:    string(s.Type),
			Content: s.Content,
		}
		for _, tc := range s.ToolCalls {
			step.ToolCalls = append(step.ToolCalls, toolCallDTO(tc))
		}
		dto.Steps = append(dto.Steps, step)
	}
	return dto
}

func toolCallDTO(tc domain.ToolCall) contracts.ToolCallDTO {
	dto := contracts.ToolCallDTO{
		ID:           tc.ID,
		Name:         tc.Name,
		Args:         []byte(tc.Args),
		Status:       string(tc.Status),
		Output:       tc.Output,
		Opaque:       tc.Opaque,
		Presentation: toolPresentationDTO(tc),
	}
	for _, att := range tc.OutputAttachments {
		dto.OutputAttachments = append(dto.OutputAttachments, contracts.AttachmentDTO{
			Type: att.Type, Name: att.Name, MediaType: att.MediaType, FilePath: att.FilePath,
		})
	}
	return dto
}

func (a *App) handleConversationsList() (any, *contracts.RPCError) {
	list := a.Conversations.List()
	out := make([]contracts.ConversationDTO, 0, len(list))
	for _, c := range list {
		if c.HiddenFromRoomList() {
			continue
		}
		out = append(out, convDTO(c))
	}
	return contracts.ConversationsListResult{Conversations: out}, nil
}

func (a *App) handleConversationsCreate(req contracts.ConversationCreateRequest) (any, *contracts.RPCError) {
	repo := NewConversation(a.Conversations, strings.TrimSpace(req.Title))
	if err := repo.Save(); err != nil {
		return nil, rpcInternal(err)
	}
	c, err := a.Conversations.Get(repo.ID())
	if err != nil {
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
		dto := msgDTO(m)
		if dto.ID == "" {
			continue // hidden hydration checkpoint
		}
		msgs = append(msgs, dto)
	}
	return contracts.ConversationGetResult{Conversation: convDTO(c), Messages: msgs}, nil
}

func (a *App) handleConversationsChunk(req contracts.ConversationChunkRequest) (any, *contracts.RPCError) {
	msgs, err := a.Conversations.GetChunk(req.ID, req.Index)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	out := make([]contracts.MessageDTO, 0, len(msgs))
	for _, m := range msgs {
		dto := msgDTO(m)
		if dto.ID == "" {
			continue // hidden hydration checkpoint
		}
		out = append(out, dto)
	}
	return contracts.ConversationChunkResult{Messages: out}, nil
}

func (a *App) handleConversationsRename(req contracts.ConversationRenameRequest) (any, *contracts.RPCError) {
	repo, rpcErr := a.loadRepoRPC(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	c := repo.Conversation()
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "title is required"}
	}
	c.Title = title
	c.Touch()
	if err := repo.Save(); err != nil {
		return nil, rpcInternal(err)
	}
	return contracts.ConversationGetResult{Conversation: convDTO(c)}, nil
}

func (a *App) handleConversationsDelete(req contracts.ConversationIDRequest) (any, *contracts.RPCError) {
	if _, rpcErr := a.getConversation(req.ID); rpcErr != nil {
		return nil, rpcErr
	}
	if run := a.activeRunForConversation(req.ID); run != nil {
		run.Cancel()
	}
	if err := a.Conversations.Delete(req.ID); err != nil {
		return nil, rpcInternal(err)
	}
	a.removeJournal(req.ID)
	if a.Todos != nil {
		a.Todos.Clear(req.ID)
	}
	a.log("info", "agent", "conversation deleted: %s", req.ID)
	return map[string]bool{"ok": true}, nil
}

func (a *App) handleConversationsPickWorkspace(req contracts.ConversationIDRequest) (any, *contracts.RPCError) {
	// Validate the conversation before opening the native picker, but do not
	// hold the turn lock while the user is choosing a folder. Once the picker
	// returns, the latest conversation is read under the same lock as turn
	// persistence so an older pre-picker snapshot cannot overwrite a completed
	// message.
	if _, rpcErr := a.getConversation(req.ID); rpcErr != nil {
		return nil, rpcErr
	}

	if a.WorkspacePicker == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "workspace folder picker is unavailable"}
	}
	workspace, err := a.WorkspacePicker.Choose(context.Background())
	if err != nil && !errors.Is(err, context.Canceled) {
		return nil, rpcInternal(err)
	}

	turnLock := a.conversationTurnLock(req.ID)
	turnLock.Lock()
	defer turnLock.Unlock()

	c, rpcErr := a.getConversation(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if err != nil {
		return contracts.ConversationGetResult{Conversation: convDTO(c)}, nil
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return contracts.ConversationGetResult{Conversation: convDTO(c)}, nil
	}
	if !filepath.IsAbs(workspace) {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "workspace path must be absolute"}
	}
	repo := bindConversation(a.Conversations, c)
	oldWorkspace := strings.TrimSpace(c.Workspace)
	changed := filepath.Clean(oldWorkspace) != filepath.Clean(workspace)
	c.Workspace = workspace
	c.Touch()
	// Visible notice (announcement + AGENTS.md file_read) waits for the
	// next user message — same injection point as restart announcements.
	// Empty rooms skip it: there is no "after my chat" slot yet.
	// Stale hidden hydration stays in formed history (append-only); the
	// visible notice carries the new AGENTS.md on the next user turn.
	if changed && conversationHasUser(c) {
		c.PendingWorkspaceAnnouncement = true
		c.WorkspaceSwitchFrom = oldWorkspace
	}
	if err := repo.Save(); err != nil {
		return nil, rpcInternal(err)
	}
	a.log("info", "agent", "workspace selected for conversation %s", c.ID)
	return contracts.ConversationGetResult{Conversation: convDTO(c)}, nil
}

func conversationHasUser(c *domain.Conversation) bool {
	if c == nil {
		return false
	}
	for _, m := range c.Messages {
		if m.Role == domain.RoleUser {
			return true
		}
	}
	return false
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
