// Package repoapprovalrule stores rooms' "always allow" rules in the
// application database.
package repoapprovalrule

import (
	"context"
	"database/sql"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Repository is the rule store.
type Repository struct{ db *sql.DB }

// New wraps the application database.
func New(db *sql.DB) *Repository { return &Repository{db: db} }

var _ drepo.ApprovalRule = (*Repository)(nil)

func (r *Repository) Create(ctx context.Context, rule *aggregate.ApprovalRule) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO approval_rules (id, conversation_id, agent_id, action, created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(conversation_id, action) DO NOTHING`,
		rule.ID(), rule.ConversationID(), rule.AgentID(), string(rule.Action()), rule.CreatedAt().UnixNano())
	return err
}

func (r *Repository) Allows(ctx context.Context, conversationID string, action enum.ToolAction) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM approval_rules WHERE conversation_id = ? AND action = ?`, conversationID, string(action)).Scan(&n)
	return n > 0, err
}

func (r *Repository) List(ctx context.Context, conversationID string) ([]aggregate.ApprovalRule, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, conversation_id, agent_id, action, created_at FROM approval_rules WHERE conversation_id = ? ORDER BY created_at`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []aggregate.ApprovalRule
	for rows.Next() {
		var id, conv, agent, action string
		var created int64
		if err := rows.Scan(&id, &conv, &agent, &action, &created); err != nil {
			return nil, err
		}
		rule, err := aggregate.NewApprovalRule(id, conv, agent, enum.ParseToolAction(action), time.Unix(0, created))
		if err != nil {
			return nil, err
		}
		out = append(out, *rule)
	}
	return out, rows.Err()
}

func (r *Repository) Delete(ctx context.Context, conversationID, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM approval_rules WHERE id = ? AND conversation_id = ?`, id, conversationID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperror.NewNotFoundError("approval rule "+id, sql.ErrNoRows)
	}
	return nil
}

func (r *Repository) DeleteByConversation(ctx context.Context, conversationID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM approval_rules WHERE conversation_id = ?`, conversationID)
	return err
}
