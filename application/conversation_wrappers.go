package application

import (
	"fmt"
	"strings"

	"nusashell/application/conversation"
	"nusashell/contracts"
	"nusashell/domain"
)

const timeRFC3339 = "2006-01-02T15:04:05Z07:00"

// ConversationRepository is the sanctioned write path for one conversation.
// The implementation lives in application/conversation.
type ConversationRepository = conversation.Repository

// ErrConversationImmutable is returned by ConversationRepository.Save when
// message IDs would be inserted, reordered, or deleted.
var ErrConversationImmutable = conversation.ErrImmutable

// NewConversation creates a new in-memory conversation. Call Save to persist.
func NewConversation(store ConversationStore, title string) *ConversationRepository {
	return conversation.NewConversation(store, title)
}

// bindConversation wraps an already-loaded conversation so Add/Save go
// through the repository.
func bindConversation(store ConversationStore, c *domain.Conversation) *ConversationRepository {
	return conversation.Bind(store, c)
}

func cloneConversation(c *domain.Conversation) *domain.Conversation {
	return conversation.Clone(c)
}

func (a *App) loadRepo(id string) (*ConversationRepository, error) {
	if a == nil || a.Conversations == nil {
		return nil, fmt.Errorf("conversation store is required")
	}
	c, err := a.Conversations.Get(id)
	if err != nil {
		return nil, err
	}
	return bindConversation(a.Conversations, c), nil
}

func (a *App) loadRepoRPC(id string) (*ConversationRepository, *contracts.RPCError) {
	c, rpcErr := a.getConversation(id)
	if rpcErr != nil {
		return nil, rpcErr
	}
	return bindConversation(a.Conversations, c), nil
}

func convDTO(c *domain.Conversation) contracts.ConversationDTO {
	return conversation.ConvDTO(c)
}

func msgDTO(m domain.Message) contracts.MessageDTO {
	return conversation.MsgDTO(m)
}

func toolCallDTO(tc domain.ToolCall) contracts.ToolCallDTO {
	return conversation.ToolCallDTO(tc)
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

func (a *App) handleConversationsList() (any, *contracts.RPCError) {
	return a.conversationService().HandleList()
}

func (a *App) handleConversationsCreate(req contracts.ConversationCreateRequest) (any, *contracts.RPCError) {
	return a.conversationService().HandleCreate(req)
}

func (a *App) handleConversationsGet(req contracts.ConversationIDRequest) (any, *contracts.RPCError) {
	return a.conversationService().HandleGet(req)
}

func (a *App) handleConversationsChunk(req contracts.ConversationChunkRequest) (any, *contracts.RPCError) {
	return a.conversationService().HandleChunk(req)
}

func (a *App) handleConversationsRename(req contracts.ConversationRenameRequest) (any, *contracts.RPCError) {
	return a.conversationService().HandleRename(req)
}

func (a *App) handleConversationsDelete(req contracts.ConversationIDRequest) (any, *contracts.RPCError) {
	return a.conversationService().HandleDelete(req)
}

func (a *App) handleConversationsSetWorkspace(req contracts.ConversationSetWorkspaceRequest) (any, *contracts.RPCError) {
	return a.conversationService().HandleSetWorkspace(req)
}

func (a *App) handleTodosGet(req contracts.TodosGetRequest) (any, *contracts.RPCError) {
	return a.conversationService().HandleTodosGet(req)
}

func (a *App) handleTodosDelete(req contracts.TodosDeleteRequest) (any, *contracts.RPCError) {
	return a.conversationService().HandleTodosDelete(req)
}

func (a *App) handleWorkspaceListDirs(req contracts.WorkspaceListDirsRequest) (any, *contracts.RPCError) {
	return a.conversationService().HandleWorkspaceListDirs(req)
}

func (a *App) effectiveWorkspace(workspace string) string {
	return conversation.Effective(workspace, a.defaultWorkspace)
}

func listInstructionFiles(workspace string) []string {
	return conversation.ListInstructionFiles(workspace)
}

// RemoveOrphanJournalSidecars deletes leftover conversations/*.journal
// directories from the retired ChangeJournal. Best-effort and non-fatal.
func RemoveOrphanJournalSidecars(dataDir string) int {
	return conversation.RemoveOrphanJournalSidecars(dataDir)
}

func (a *App) ListConversations(currentConvID string, limit, offset int) (int, []ConversationSummaryDTO, error) {
	return a.conversationService().ListRooms(currentConvID, limit, offset)
}

func (a *App) SearchConversations(currentConvID, query string, limit, offset int) (int, []ConversationSummaryDTO, error) {
	return a.conversationService().SearchRooms(currentConvID, query, limit, offset)
}

func (a *App) SendConversationMessage(currentConvID, targetConvID, content string) error {
	return a.conversationService().SendPeer(currentConvID, targetConvID, content)
}

func (a *App) List(currentConvID string, limit, offset int) (int, []ConversationSummaryDTO, error) {
	return a.ListConversations(currentConvID, limit, offset)
}

func (a *App) Search(currentConvID, query string, limit, offset int) (int, []ConversationSummaryDTO, error) {
	return a.SearchConversations(currentConvID, query, limit, offset)
}

func (a *App) Send(currentConvID, targetConvID, content string) error {
	return a.SendConversationMessage(currentConvID, targetConvID, content)
}
