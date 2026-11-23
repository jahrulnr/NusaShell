// Package repochatindex implements the chat message index on the
// application database: a table plus an FTS5 full-text index the
// migration's triggers keep in step.
package repochatindex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Repository is the chat index.
type Repository struct{ db *sql.DB }

// New wraps an opened application database.
func New(db *sql.DB) *Repository { return &Repository{db: db} }

var _ drepo.ChatIndex = (*Repository)(nil)

const cols = `id, channel, chat_id, message_id, sender_id, sender_name, chat_name, at_ms, from_me, is_group, text, raw, is_live, explicit, receipt`

func (r *Repository) Save(ctx context.Context, m drepo.ChatMessage) error {
	if m.Channel == "" || m.Chat == "" || m.ID == "" {
		return apperror.New(400, "a chat message needs a channel, a chat, and an id", nil)
	}
	raw := string(m.Raw)
	_, err := r.db.ExecContext(ctx, `INSERT INTO chat_messages
		(channel, chat_id, message_id, sender_id, sender_name, chat_name, at_ms, from_me, is_group, text, raw, is_live, explicit, receipt)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (channel, chat_id, message_id) DO UPDATE SET
			sender_id = excluded.sender_id, sender_name = excluded.sender_name,
			chat_name = CASE WHEN excluded.chat_name <> '' THEN excluded.chat_name ELSE chat_messages.chat_name END,
			at_ms = excluded.at_ms, from_me = excluded.from_me, is_group = excluded.is_group,
			text = excluded.text, raw = excluded.raw, is_live = excluded.is_live,
			explicit = MAX(chat_messages.explicit, excluded.explicit)
		WHERE excluded.is_live > chat_messages.is_live OR
			(excluded.is_live = chat_messages.is_live AND excluded.at_ms >= chat_messages.at_ms)`,
		m.Channel, m.Chat, m.ID, m.Sender, m.SenderName, m.ChatName, m.At.UnixMilli(), m.FromMe, m.Group,
		m.Text, raw, m.Live, m.Explicit, string(m.Receipt))
	return err
}

func scan(s interface{ Scan(...any) error }, withRaw bool) (int64, drepo.ChatMessage, error) {
	var m drepo.ChatMessage
	var id, at int64
	var raw, receipt string
	if err := s.Scan(&id, &m.Channel, &m.Chat, &m.ID, &m.Sender, &m.SenderName, &m.ChatName, &at,
		&m.FromMe, &m.Group, &m.Text, &raw, &m.Live, &m.Explicit, &receipt); err != nil {
		return 0, m, err
	}
	m.At = time.UnixMilli(at)
	m.Receipt, _ = valueobject.ParseReceipt(receipt)
	if withRaw && raw != "" {
		m.Raw = json.RawMessage(raw)
	}
	return id, m, nil
}

func (r *Repository) Get(ctx context.Context, channel, chat, id string) (drepo.ChatMessage, error) {
	_, m, err := scan(r.db.QueryRowContext(ctx, `SELECT `+cols+` FROM chat_messages WHERE channel = ? AND chat_id = ? AND message_id = ?`,
		channel, chat, id), true)
	if errors.Is(err, sql.ErrNoRows) {
		return m, apperror.NewNotFoundError("chat message "+id, err)
	}
	return m, err
}

func (r *Repository) Revoke(ctx context.Context, channel, chat, id string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE chat_messages SET text = '', raw = '', receipt = ''
		WHERE channel = ? AND chat_id = ? AND message_id = ? AND (text <> '' OR raw <> '')`, channel, chat, id)
	return err
}

func (r *Repository) UpdateText(ctx context.Context, channel, chat, id, text string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE chat_messages SET text = ? WHERE channel = ? AND chat_id = ? AND message_id = ?`,
		text, channel, chat, id)
	return err
}

func (r *Repository) MarkReceipt(ctx context.Context, channel, chat string, ids []string, receipt valueobject.Receipt) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		var cur string
		err := tx.QueryRowContext(ctx, `SELECT receipt FROM chat_messages WHERE channel = ? AND chat_id = ? AND message_id = ? AND from_me = 1`,
			channel, chat, id).Scan(&cur)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		prev, _ := valueobject.ParseReceipt(cur)
		if !receipt.After(prev) {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE chat_messages SET receipt = ? WHERE channel = ? AND chat_id = ? AND message_id = ?`,
			string(receipt), channel, chat, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *Repository) Purge(ctx context.Context, channel string, before time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM chat_messages WHERE channel = ? AND at_ms < ?`, channel, before.UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Search matches the query as one FTS5 phrase. A query FTS5 cannot take
// as a phrase (it holds quotes) falls back to a substring scan.
func (r *Repository) Search(ctx context.Context, f drepo.ChatSearch) (drepo.ChatSearchPage, error) {
	page := drepo.ChatSearchPage{Messages: []drepo.ChatMessage{}}
	if f.Limit < 1 || f.Limit > 500 {
		return page, apperror.New(400, "limit must be between 1 and 500", nil)
	}
	where := []string{"channel = ?"}
	args := []any{f.Channel}
	q := strings.TrimSpace(f.Query)
	if len(q) >= 2 && strings.HasPrefix(q, `"`) && strings.HasSuffix(q, `"`) {
		q = strings.TrimSpace(q[1 : len(q)-1])
	}
	switch {
	case q == "":
	case !strings.ContainsAny(q, `"'`):
		where = append(where, "id IN (SELECT rowid FROM chat_messages_fts WHERE chat_messages_fts MATCH ?)")
		args = append(args, `"`+q+`"`)
	default:
		where = append(where, "instr(lower(text), lower(?)) > 0")
		args = append(args, q)
	}
	if f.Chat != "" {
		where = append(where, "chat_id = ?")
		args = append(args, f.Chat)
	}
	if f.Sender != "" {
		where = append(where, "sender_id = ?")
		args = append(args, f.Sender)
	}
	if f.Cursor != "" {
		at, row, ok := strings.Cut(f.Cursor, ":")
		atMs, e1 := strconv.ParseInt(at, 10, 64)
		rowID, e2 := strconv.ParseInt(row, 10, 64)
		if !ok || e1 != nil || e2 != nil || rowID < 1 {
			return page, apperror.New(400, "invalid cursor", nil)
		}
		where = append(where, "(at_ms < ? OR (at_ms = ? AND id < ?))")
		args = append(args, atMs, atMs, rowID)
	}
	args = append(args, f.Limit+1)
	rows, err := r.db.QueryContext(ctx, `SELECT `+cols+` FROM chat_messages WHERE `+strings.Join(where, " AND ")+
		` ORDER BY at_ms DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	var lastID int64
	for rows.Next() {
		id, m, err := scan(rows, f.Raw)
		if err != nil {
			return page, err
		}
		if len(page.Messages) == f.Limit {
			last := page.Messages[len(page.Messages)-1]
			page.NextCursor = fmt.Sprintf("%d:%d", last.At.UnixMilli(), lastID)
			break
		}
		page.Messages = append(page.Messages, m)
		lastID = id
	}
	return page, rows.Err()
}
