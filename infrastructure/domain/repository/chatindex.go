package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// ChatMessage is one message a channel account saw or sent, kept so the
// owner can search their chats. It is a record of the chat, not of a room:
// keeping it neither routes it nor applies the allowlist.
type ChatMessage struct {
	Channel    string
	Chat       string // the chat's id on the channel
	ID         string // the message id within the chat
	Sender     string
	SenderName string
	ChatName   string
	At         time.Time
	FromMe     bool
	Group      bool
	Text       string
	// Raw is the channel's own payload of the message, opaque here.
	Raw json.RawMessage
	// Live marks a message seen as it happened; a copy from a history sync
	// never overwrites a live one.
	Live bool
	// Explicit marks a message the owner sent through a send request (the
	// CLI), not as a room's reply.
	Explicit bool
	// Receipt is how far an outgoing message got.
	Receipt valueobject.Receipt
}

// ChatSearch filters ChatIndex.Search. Query is matched as one phrase
// (case-insensitive); empty matches every message. Cursor continues a
// previous page.
type ChatSearch struct {
	Channel, Query, Chat, Sender, Cursor string
	Limit                                int
	// Raw includes each message's channel payload.
	Raw bool
}

// ChatSearchPage is one page of results, newest first.
type ChatSearchPage struct {
	Messages   []ChatMessage
	NextCursor string
}

// ChatIndex keeps the messages of the channels' accounts for search.
type ChatIndex interface {
	// Save upserts by channel, chat, and id. A history copy never replaces
	// a live one, and an older copy never replaces a newer one; the receipt
	// and the explicit mark are kept.
	Save(ctx context.Context, m ChatMessage) error
	// Get returns one message; 404 when it is not kept.
	Get(ctx context.Context, channel, chat, id string) (ChatMessage, error)
	// Revoke clears a message deleted for everyone, so it can no longer be
	// found or read. An unknown message is not an error.
	Revoke(ctx context.Context, channel, chat, id string) error
	// UpdateText replaces the text of an edited message.
	UpdateText(ctx context.Context, channel, chat, id, text string) error
	// MarkReceipt moves outgoing messages' receipts forward; a receipt
	// never goes back.
	MarkReceipt(ctx context.Context, channel, chat string, ids []string, receipt valueobject.Receipt) error
	// Purge drops a channel's messages older than before and returns how
	// many went.
	Purge(ctx context.Context, channel string, before time.Time) (int64, error)
	// Search pages through matching messages, newest first.
	Search(ctx context.Context, f ChatSearch) (ChatSearchPage, error)
}
