package application

import (
	"fmt"

	"nusashell/application/conversation"
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

func (a *App) loadRepo(id string) (*ConversationRepository, error) {
	if a == nil || a.Conversations == nil {
		return nil, fmt.Errorf("conversation store is required")
	}
	c, err := a.Conversations.Get(id)
	if err != nil {
		return nil, err
	}
	return conversation.Bind(a.Conversations, c), nil
}

func (a *App) effectiveWorkspace(workspace string) string {
	return conversation.Effective(workspace, a.defaultWorkspace)
}

// The six methods below satisfy ConversationMessenger (ports.go): the
// conversation tool consumes them as function values, so they cannot be
// inlined away.
func (a *App) List(currentConvID string, limit, offset int) (int, []ConversationSummaryDTO, error) {
	return a.conversationService().ListRooms(currentConvID, limit, offset)
}

func (a *App) Search(currentConvID, query string, limit, offset int) (int, []ConversationSummaryDTO, error) {
	return a.conversationService().SearchRooms(currentConvID, query, limit, offset)
}

func (a *App) Send(currentConvID, targetConvID, content string) error {
	return a.conversationService().SendPeer(currentConvID, targetConvID, content)
}

func (a *App) Info(id string, chunk *int) (ConversationInfoDTO, error) {
	return a.conversationService().RoomInfo(id, chunk)
}

func (a *App) Read(id string, chunk *int, start, end *int) (ConversationReadResult, error) {
	return a.conversationService().RoomRead(id, chunk, start, end)
}

func (a *App) SearchMessages(id, query string, limit, offset int) (int, []ConversationMessageHitDTO, error) {
	return a.conversationService().SearchMessages(id, query, limit, offset)
}
