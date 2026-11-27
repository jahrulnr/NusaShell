package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// ChannelLink stores which room answers each external chat. A missing
// chat is not found, reported with a stable error identity.
type ChannelLink interface {
	// Find returns the link of one chat.
	Find(ctx context.Context, chat valueobject.ChatRef) (*aggregate.ChannelLink, error)
	// Save upserts by chat: saving a chat that is linked elsewhere moves it.
	Save(ctx context.Context, l aggregate.ChannelLink) error
	// List returns every linked chat.
	List(ctx context.Context) ([]aggregate.ChannelLink, error)
	// ListByConversation returns the chats linked to one room.
	ListByConversation(ctx context.Context, conversationID string) ([]aggregate.ChannelLink, error)
	// Delete unlinks one chat; an unlinked chat is not an error.
	Delete(ctx context.Context, chat valueobject.ChatRef) error
	// DeleteByConversation unlinks every chat of a room.
	DeleteByConversation(ctx context.Context, conversationID string) error
}

// InboundLog remembers which channel messages were already taken, so a
// channel that redelivers after a reconnect does not run a message twice.
type InboundLog interface {
	// Claim records the message and reports whether it was new.
	Claim(ctx context.Context, channel, messageID string) (fresh bool, err error)
	// Release forgets a claim whose message could not be taken, so a
	// redelivery is processed.
	Release(ctx context.Context, channel, messageID string) error
	// Prune drops claims older than before.
	Prune(ctx context.Context, before time.Time) error
}

// ChannelSettings stores each channel's own settings document. The shape
// belongs to the channel adapter; the store keeps it opaque.
type ChannelSettings interface {
	// Get returns the stored document; not found, with a stable error identity, when the channel has none yet.
	Get(ctx context.Context, channel string) (json.RawMessage, error)
	// Put replaces the stored document.
	Put(ctx context.Context, channel string, doc json.RawMessage) error
}

// ChannelSecretKey names the vault entry for a channel credential, such as
// a Telegram bot token.
func ChannelSecretKey(channel, name string) string {
	return "channel/" + channel + "/" + name
}
