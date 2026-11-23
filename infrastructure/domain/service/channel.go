package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// InboundMessage is one message an external chat sent that the channel let
// through its own gates (enabled, allowlist, group policy).
type InboundMessage struct {
	Chat valueobject.ChatRef
	// MessageID is unique per channel; it deduplicates redeliveries.
	MessageID  string
	SenderID   string
	SenderName string
	// ChatName is the group's name, or the sender's for a direct chat.
	ChatName string
	// Text carries a bracketed note for media that could not be fetched,
	// so the room knows something was lost.
	Text string
	// Quote is what the message replies to: the quoted message's text, or a
	// label for what it was when it had none ("an image"). Empty when the
	// message replies to nothing.
	Quote string
	Group bool
	// Files are the media the message carried, downloaded.
	Files []ChannelFile
	// Vote is a vote on a poll this channel sent; such a message carries
	// nothing else.
	Vote *PollVote
	// Inline marks a message written through the bot's inline mode: its
	// @tokens were picked from ChannelMentions.
	Inline bool
	At     time.Time
}

// ChannelFile is a file a chat sent or a reply sends to a chat.
type ChannelFile struct {
	Name      string
	MediaType string
	Data      []byte
}

// MaxChannelFile bounds one file a channel takes in or sends out.
const MaxChannelFile = 25 << 20

// PollVote is a vote on a poll the channel sent: the poll's id as SendPoll
// returned it, and the options now picked (empty when the voter took their
// vote back, nil when the vote could not be read).
type PollVote struct {
	PollID   string
	Selected []string
}

// ChannelMention is a thread or project a chat can name while it writes,
// for surfaces that suggest them (Telegram inline mode). Token is what
// the message gets: "@label".
type ChannelMention struct {
	Kind  string // conversation or project
	ID    string
	Token string
	Title string
}

// InboundHandler takes messages from channels. HandleInbound must return
// quickly; the work it starts runs on its own.
type InboundHandler interface {
	HandleInbound(ctx context.Context, in InboundMessage)
	// Mentions are the threads and projects matching query, best first.
	Mentions(ctx context.Context, query string) []ChannelMention
	// HandleReceipt reports how far messages this app sent got: delivered
	// or read, by the ids Channel.Send returned.
	HandleReceipt(ctx context.Context, channel string, messageIDs []string, receipt valueobject.Receipt)
}

// FileSender is a channel that delivers files. Files are sent one by one
// after the reply's text.
type FileSender interface {
	SendFile(ctx context.Context, chat valueobject.ChatRef, f ChannelFile) error
}

// PollSender is a channel with native polls. maxSelectable 1 is a
// single-choice poll. The id it returns comes back on PollVote.
type PollSender interface {
	SendPoll(ctx context.Context, chat valueobject.ChatRef, question string, options []string, maxSelectable int) (string, error)
}

// CommandMenu is a channel with a per-chat "/" menu. SyncCommands puts the
// room's agent commands after the channel's own; none puts the chat back
// on the default menu. Failures are the adapter's to log: a menu is never
// worth failing a turn.
type CommandMenu interface {
	SyncCommands(ctx context.Context, chat valueobject.ChatRef, cmds []AgentCommand)
}

// BlockedSender is a recent sender the channel's allowlist turned away, so
// the owner can allow them with one action.
type BlockedSender struct {
	SenderID string    `json:"senderId"`
	ChatID   string    `json:"chatId"`
	Name     string    `json:"name,omitempty"`
	Group    bool      `json:"group,omitempty"`
	At       time.Time `json:"at"`
}

// ChannelStatus is what the owner sees about a channel.
type ChannelStatus struct {
	Name      string `json:"name"`
	Label     string `json:"label"`
	Enabled   bool   `json:"enabled"`
	Connected bool   `json:"connected"`
	// State is the channel's own word: unpaired, pairing, connecting,
	// connected, disconnected, error, …
	State     string          `json:"state"`
	Detail    string          `json:"detail,omitempty"`
	Account   string          `json:"account,omitempty"`
	HasQR     bool            `json:"hasQr,omitempty"`
	Blocked   []BlockedSender `json:"blocked"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

// Channel is an external chat surface the owner can reach their rooms from.
// Adapters own their wire, identity, allowlist, and text format: Send takes
// Markdown and renders it for the surface.
type Channel interface {
	Name() string
	// Start begins work in the background and returns at once; messages
	// that pass the channel's gates go to handler.
	Start(ctx context.Context, handler InboundHandler) error
	Stop() error
	Status() ChannelStatus
	// Send delivers one message to a chat, split when the chat caps its
	// length, and returns the chat's ids of what it sent.
	Send(ctx context.Context, chat valueobject.ChatRef, markdown string) ([]string, error)
	// Typing shows that a reply is being prepared until stop is called.
	Typing(ctx context.Context, chat valueobject.ChatRef) (stop func())

	// Connect, Disconnect, and Logout control the channel's session; QR
	// returns a pairing image when the channel pairs by QR code.
	Connect(ctx context.Context) error
	Disconnect() error
	Logout(ctx context.Context) error
	QR(ctx context.Context) ([]byte, error)
	// Settings and UpdateSettings read and write the channel's own
	// settings document; secret fields are write-only.
	Settings(ctx context.Context) (json.RawMessage, error)
	UpdateSettings(ctx context.Context, doc json.RawMessage) error
}
