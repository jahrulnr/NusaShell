package valueobject

import (
	"errors"
	"strings"
)

// ChatRef names one chat on an external channel: the channel ("whatsapp",
// "telegram"), the chat's id on that channel, and an optional thread inside
// it (a Telegram forum topic). Ids are opaque to the domain.
type ChatRef struct {
	channel string
	chatID  string
	thread  string
}

// NewChatRef validates the reference.
func NewChatRef(channel, chatID, thread string) (ChatRef, error) {
	channel, chatID, thread = strings.TrimSpace(channel), strings.TrimSpace(chatID), strings.TrimSpace(thread)
	if channel == "" || chatID == "" {
		return ChatRef{}, errors.New("a chat needs a channel and a chat id")
	}
	return ChatRef{channel: channel, chatID: chatID, thread: thread}, nil
}

func (c ChatRef) Channel() string { return c.channel }
func (c ChatRef) ChatID() string  { return c.chatID }
func (c ChatRef) Thread() string  { return c.thread }

// Key is a stable string form, unique per chat.
func (c ChatRef) Key() string { return c.channel + ":" + c.chatID + ":" + c.thread }

// Routing decides where a room's replies go besides the web, which always
// sees everything.
type Routing struct {
	// ReplyToOrigin sends the final reply back to the chat the turn came from.
	ReplyToOrigin bool `json:"replyToOrigin"`
	// MirrorFinal also sends the final reply to every other linked chat.
	MirrorFinal bool `json:"mirrorFinal"`
	// NotifyApprovals pings the origin chat when the agent asks permission.
	NotifyApprovals bool `json:"notifyApprovals"`
}

// DefaultRouting answers in the chat a message came from and asks there for
// approvals; it copies nothing to other chats.
func DefaultRouting() Routing {
	return Routing{ReplyToOrigin: true, NotifyApprovals: true}
}
