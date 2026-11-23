package aggregate

import (
	"errors"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// ChannelLink ties one external chat to the room that answers it. A chat
// links to exactly one room; a room may be linked from several chats (a
// WhatsApp DM and a Telegram group, say).
type ChannelLink struct {
	chat           valueobject.ChatRef
	conversationID string
	display        string
	group          bool
	createdAt      time.Time
}

// NewChannelLink validates the link.
func NewChannelLink(chat valueobject.ChatRef, conversationID, display string, group bool, now time.Time) (ChannelLink, error) {
	if err := valueobject.ValidID(conversationID); err != nil {
		return ChannelLink{}, errors.New("a channel link needs a conversation id")
	}
	if display == "" {
		display = chat.ChatID()
	}
	return ChannelLink{chat: chat, conversationID: conversationID, display: display, group: group, createdAt: now}, nil
}

func (l ChannelLink) Chat() valueobject.ChatRef { return l.chat }
func (l ChannelLink) ConversationID() string    { return l.conversationID }
func (l ChannelLink) Display() string           { return l.display }
func (l ChannelLink) IsGroup() bool             { return l.group }
func (l ChannelLink) CreatedAt() time.Time      { return l.createdAt }
