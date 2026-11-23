// Package repochannel implements the channel repository ports — links,
// the inbound dedupe log, and channel settings — on the application
// database (see infrastructure/repository/store).
package repochannel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Repository serves all three channel ports from one handle.
type Repository struct{ db *sql.DB }

// New wraps an opened application database.
func New(db *sql.DB) *Repository { return &Repository{db: db} }

var (
	_ drepo.ChannelLink     = (*Repository)(nil)
	_ drepo.InboundLog      = (*Repository)(nil)
	_ drepo.ChannelSettings = (*Repository)(nil)
)

const linkCols = `channel, chat_id, thread_id, conversation_id, display_name, is_group, created_at`

func scanLink(s interface{ Scan(...any) error }) (*aggregate.ChannelLink, error) {
	var channel, chat, thread, conv, display string
	var group bool
	var created int64
	if err := s.Scan(&channel, &chat, &thread, &conv, &display, &group, &created); err != nil {
		return nil, err
	}
	ref, err := valueobject.NewChatRef(channel, chat, thread)
	if err != nil {
		return nil, err
	}
	l, err := aggregate.NewChannelLink(ref, conv, display, group, time.Unix(created, 0))
	return &l, err
}

func (r *Repository) Find(ctx context.Context, chat valueobject.ChatRef) (*aggregate.ChannelLink, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+linkCols+` FROM channel_links WHERE channel = ? AND chat_id = ? AND thread_id = ?`,
		chat.Channel(), chat.ChatID(), chat.Thread())
	l, err := scanLink(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NewNotFoundError("channel link "+chat.Key(), err)
	}
	return l, err
}

func (r *Repository) Save(ctx context.Context, l aggregate.ChannelLink) error {
	c := l.Chat()
	_, err := r.db.ExecContext(ctx, `INSERT INTO channel_links (`+linkCols+`) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(channel, chat_id, thread_id) DO UPDATE SET
			conversation_id = excluded.conversation_id, display_name = excluded.display_name, is_group = excluded.is_group`,
		c.Channel(), c.ChatID(), c.Thread(), l.ConversationID(), l.Display(), l.IsGroup(), l.CreatedAt().Unix())
	return err
}

func (r *Repository) List(ctx context.Context) ([]aggregate.ChannelLink, error) {
	return r.links(ctx, `SELECT `+linkCols+` FROM channel_links ORDER BY channel, display_name, chat_id`)
}

func (r *Repository) ListByConversation(ctx context.Context, conversationID string) ([]aggregate.ChannelLink, error) {
	return r.links(ctx, `SELECT `+linkCols+` FROM channel_links WHERE conversation_id = ? ORDER BY created_at, channel, chat_id`, conversationID)
}

func (r *Repository) links(ctx context.Context, q string, args ...any) ([]aggregate.ChannelLink, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []aggregate.ChannelLink
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

func (r *Repository) Delete(ctx context.Context, chat valueobject.ChatRef) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM channel_links WHERE channel = ? AND chat_id = ? AND thread_id = ?`,
		chat.Channel(), chat.ChatID(), chat.Thread())
	return err
}

func (r *Repository) DeleteByConversation(ctx context.Context, conversationID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM channel_links WHERE conversation_id = ?`, conversationID)
	return err
}

func (r *Repository) Claim(ctx context.Context, channel, messageID string) (bool, error) {
	res, err := r.db.ExecContext(ctx, `INSERT OR IGNORE INTO channel_inbound (channel, message_id, received_at) VALUES (?, ?, ?)`,
		channel, messageID, time.Now().Unix())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (r *Repository) Release(ctx context.Context, channel, messageID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM channel_inbound WHERE channel = ? AND message_id = ?`, channel, messageID)
	return err
}

func (r *Repository) Prune(ctx context.Context, before time.Time) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM channel_inbound WHERE received_at < ?`, before.Unix())
	return err
}

func (r *Repository) Get(ctx context.Context, channel string) (json.RawMessage, error) {
	var doc string
	err := r.db.QueryRowContext(ctx, `SELECT document FROM channel_settings WHERE channel = ?`, channel).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NewNotFoundError("settings for "+channel, err)
	}
	return json.RawMessage(doc), err
}

func (r *Repository) Put(ctx context.Context, channel string, doc json.RawMessage) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO channel_settings (channel, document, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(channel) DO UPDATE SET document = excluded.document, updated_at = excluded.updated_at`,
		channel, string(doc), time.Now().Unix())
	return err
}
