package conversation

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/pkg/rpcdispatch"
)

func (s *Service) get(id string) (*domain.Conversation, *contracts.RPCError) {
	if strings.TrimSpace(id) == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "conversation id is required"}
	}
	c, err := s.store.Get(id)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	return c, nil
}

func (s *Service) loadRepo(id string) (*Repository, *contracts.RPCError) {
	c, rpcErr := s.get(id)
	if rpcErr != nil {
		return nil, rpcErr
	}
	return Bind(s.store, c), nil
}

// HandleList returns visible (non-pipeline) conversations.
func (s *Service) HandleList() (any, *contracts.RPCError) {
	list := s.store.List()
	out := make([]contracts.ConversationDTO, 0, len(list))
	for _, c := range list {
		if c.HiddenFromRoomList() {
			continue
		}
		out = append(out, ConvDTO(c))
	}
	return contracts.ConversationsListResult{Conversations: out}, nil
}

// HandleCreate persists a new untitled-or-titled room.
func (s *Service) HandleCreate(req contracts.ConversationCreateRequest) (any, *contracts.RPCError) {
	repo := NewConversation(s.store, strings.TrimSpace(req.Title))
	if err := repo.Save(); err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	c, err := s.store.Get(repo.ID())
	if err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	s.info("conversation created: %s", c.ID)
	return contracts.ConversationGetResult{Conversation: ConvDTO(c)}, nil
}

// HandleGet returns a room and its visible messages.
func (s *Service) HandleGet(req contracts.ConversationIDRequest) (any, *contracts.RPCError) {
	c, rpcErr := s.get(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	msgs := make([]contracts.MessageDTO, 0, len(c.Messages))
	for _, m := range c.Messages {
		dto := MsgDTO(m)
		if dto.ID == "" {
			continue // hidden hydration checkpoint
		}
		msgs = append(msgs, dto)
	}
	return contracts.ConversationGetResult{Conversation: ConvDTO(c), Messages: msgs}, nil
}

// HandleChunk returns one archived pre-compaction chunk.
func (s *Service) HandleChunk(req contracts.ConversationChunkRequest) (any, *contracts.RPCError) {
	msgs, err := s.store.GetChunk(req.ID, req.Index)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	out := make([]contracts.MessageDTO, 0, len(msgs))
	for _, m := range msgs {
		dto := MsgDTO(m)
		if dto.ID == "" {
			continue // hidden hydration checkpoint
		}
		out = append(out, dto)
	}
	return contracts.ConversationChunkResult{Messages: out}, nil
}

// HandleRename updates the room title.
func (s *Service) HandleRename(req contracts.ConversationRenameRequest) (any, *contracts.RPCError) {
	repo, rpcErr := s.loadRepo(req.ID)
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
		return nil, rpcdispatch.Internal(err)
	}
	return contracts.ConversationGetResult{Conversation: ConvDTO(c)}, nil
}

// HandleDelete stops live work, wipes sidecars, then deletes the JSON.
func (s *Service) HandleDelete(req contracts.ConversationIDRequest) (any, *contracts.RPCError) {
	if _, rpcErr := s.get(req.ID); rpcErr != nil {
		return nil, rpcErr
	}
	// Cascade: stop live processes and wipe sidecar data so a deleted
	// conversation leaves no orphan attachments, ACP snapshots, plan
	// files, or compaction chunks behind. The conversation JSON is kept
	// until last: any failure in the cascade steps leaves a retryable
	// state (the JSON is still on disk, sidecars can be retried).
	if s.acp != nil {
		for _, run := range s.acp.List(req.ID) {
			if run == nil {
				continue
			}
			if err := s.acp.Stop(run.ID); err != nil {
				s.warn("delete: failed to stop ACP run %s: %v", run.ID, err)
			}
		}
	}
	if s.cancelRun != nil {
		s.cancelRun(req.ID)
	}
	if s.attachments != nil {
		if err := s.attachments.Remove(req.ID); err != nil {
			s.warn("delete: failed to remove attachments for %s: %v", req.ID, err)
		}
	}
	if err := s.store.Delete(req.ID); err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	if s.todos != nil {
		s.todos.Clear(req.ID)
	}
	s.info("conversation deleted: %s", req.ID)
	return map[string]bool{"ok": true}, nil
}

// HandleSetProvider persists the provider route or Codex account for one room.
func (s *Service) HandleSetProvider(req contracts.ConversationSetProviderRequest) (any, *contracts.RPCError) {
	if _, rpcErr := s.get(req.ID); rpcErr != nil {
		return nil, rpcErr
	}
	if s.lockTurn != nil {
		unlock := s.lockTurn(req.ID)
		defer unlock()
	}
	c, rpcErr := s.get(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	c.ProviderRoute = strings.TrimSpace(req.ProviderRoute)
	c.Touch()
	if err := Bind(s.store, c).Save(); err != nil {
		return nil, rpcdispatch.Internal(err)
	}
	return contracts.ConversationGetResult{Conversation: ConvDTO(c)}, nil
}

func (s *Service) HandleSetWorkspace(req contracts.ConversationSetWorkspaceRequest) (any, *contracts.RPCError) {
	// Validate the conversation and the candidate path before doing any
	// filesystem work, and do not hold the turn lock during EnsureDir.
	// Once the path is confirmed, the latest conversation is read under the
	// same lock as turn persistence so a stale snapshot cannot overwrite a
	// completed message.
	if _, rpcErr := s.get(req.ID); rpcErr != nil {
		return nil, rpcErr
	}

	if s.browser == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "workspace folder browser is unavailable"}
	}
	workspace := strings.TrimSpace(req.Path)
	if workspace == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "workspace path is required"}
	}
	if !filepath.IsAbs(workspace) {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "workspace path must be absolute"}
	}
	if err := s.browser.EnsureDir(context.Background(), workspace); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "workspace directory does not exist"}
		}
		if errors.Is(err, fs.ErrInvalid) {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "workspace path is not a directory"}
		}
		return nil, rpcdispatch.Internal(err)
	}

	if s.lockTurn != nil {
		unlock := s.lockTurn(req.ID)
		defer unlock()
	}

	c, rpcErr := s.get(req.ID)
	if rpcErr != nil {
		return nil, rpcErr
	}
	repo := Bind(s.store, c)
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
		return nil, rpcdispatch.Internal(err)
	}
	s.info("workspace selected for conversation %s", c.ID)
	return contracts.ConversationGetResult{Conversation: ConvDTO(c)}, nil
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
