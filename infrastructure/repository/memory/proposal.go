package repomemory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Proposals keeps memory suggestions beside the memories they point at.
type Proposals struct{ db *sql.DB }

func NewProposals(db *sql.DB) *Proposals { return &Proposals{db: db} }

var _ drepo.MemoryProposals = (*Proposals)(nil)

const proposalCols = `id, conversation_id, message_id, op, scope, type, name, description, body, reason, evidence,
	COALESCE(target_id, ''), target_updated_at, similar, status, accepted_id, created_at, decided_at`

func (r *Proposals) Add(ctx context.Context, p *aggregate.MemoryProposal) error {
	similar, err := json.Marshal(p.Similar())
	if err != nil {
		return err
	}
	if p.Similar() == nil {
		similar = []byte("[]")
	}
	var target any
	if p.TargetID() != "" {
		target = p.TargetID()
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO memory_proposals (`+
		`id, conversation_id, message_id, op, scope, type, name, description, body, reason, evidence,
		 target_id, target_updated_at, similar, status, accepted_id, created_at, decided_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID(), p.ConversationID(), p.MessageID(), string(p.Op()), p.Scope().String(), string(p.Type()), p.Name(),
		p.Description(), p.Body(), p.Reason(), p.Evidence(), target, ms(p.TargetUpdatedAt()), string(similar),
		string(p.Status()), p.AcceptedID(), ms(p.CreatedAt()), ms(p.DecidedAt()))
	if err != nil && strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		return apperror.New(409, "the memory this suggestion changes is gone", err)
	}
	return err
}

func (r *Proposals) Get(ctx context.Context, id string) (*aggregate.MemoryProposal, error) {
	p, err := scanProposal(r.db.QueryRowContext(ctx, `SELECT `+proposalCols+` FROM memory_proposals WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.New(404, "no such suggestion", err)
	}
	return p, err
}

func (r *Proposals) List(ctx context.Context, statuses []enum.MemoryProposalStatus, limit int) ([]*aggregate.MemoryProposal, error) {
	if len(statuses) == 0 {
		statuses = []enum.MemoryProposalStatus{enum.MemoryProposalPending}
	}
	if limit <= 0 || limit > maxList {
		limit = maxList
	}
	args := make([]any, 0, len(statuses)+1)
	for _, s := range statuses {
		args = append(args, string(s))
	}
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, `SELECT `+proposalCols+` FROM memory_proposals
		WHERE status IN (`+marks(len(statuses))+`) ORDER BY created_at DESC, id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*aggregate.MemoryProposal
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Proposals) CountPending(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_proposals WHERE status = 'pending'`).Scan(&n)
	return n, err
}

func (r *Proposals) Decide(ctx context.Context, p *aggregate.MemoryProposal) error {
	res, err := r.db.ExecContext(ctx, `UPDATE memory_proposals SET status = ?, accepted_id = ?, decided_at = ?
		WHERE id = ? AND status = 'pending'`, string(p.Status()), p.AcceptedID(), ms(p.DecidedAt()), p.ID())
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return apperror.New(409, "this suggestion was already decided", nil)
	}
	return nil
}

func (r *Proposals) Seen(ctx context.Context, key drepo.MemoryProposalKey, convID string, since time.Time) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_proposals
		WHERE scope = ? AND name = ? AND op = ?
		  AND (status = 'pending' OR (conversation_id = ? AND decided_at >= ?))`,
		key.Scope.String(), key.Name, string(key.Op), convID, ms(since)).Scan(&n)
	return n > 0, err
}

func (r *Proposals) DismissFrom(ctx context.Context, convID string, now time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE memory_proposals SET status = 'dismissed', decided_at = ?
		WHERE conversation_id = ? AND status = 'pending'`, ms(now), convID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *Proposals) DismissIn(ctx context.Context, scope valueobject.MemoryScope, now time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE memory_proposals SET status = 'dismissed', decided_at = ?
		WHERE scope = ? AND status = 'pending'`, ms(now), scope.String())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *Proposals) PurgeDecided(ctx context.Context, before time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM memory_proposals WHERE status <> 'pending' AND decided_at < ?`, ms(before))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func scanProposal(s interface{ Scan(...any) error }) (*aggregate.MemoryProposal, error) {
	var id, conv, msg, op, scope, mtype, name, desc, body, reason, evidence, target, similar, status, accepted string
	var targetAt, created, decided int64
	if err := s.Scan(&id, &conv, &msg, &op, &scope, &mtype, &name, &desc, &body, &reason, &evidence,
		&target, &targetAt, &similar, &status, &accepted, &created, &decided); err != nil {
		return nil, err
	}
	sc, err := valueobject.ParseMemoryScope(scope)
	if err != nil {
		return nil, err
	}
	p, err := aggregate.NewMemoryProposal(id, aggregate.MemoryProposalInput{
		ConversationID: conv, MessageID: msg, Op: enum.MemoryProposalOp(op), Scope: sc, Type: enum.MemoryType(mtype),
		Name: name, Description: desc, Body: body, Reason: reason, Evidence: evidence, TargetID: target, TargetUpdatedAt: at(targetAt),
	}, at(created))
	if err != nil {
		return nil, err
	}
	var ids []string
	if err := json.Unmarshal([]byte(similar), &ids); err == nil {
		p.SetSimilar(ids)
	}
	p.SetStored(enum.MemoryProposalStatus(status), accepted, at(created), at(decided))
	return p, nil
}
