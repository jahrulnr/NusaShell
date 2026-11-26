package reposkill

import (
	"context"
	"database/sql"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Decisions is the one write that decides a skill proposal: the decision and
// the ownership row it leaves behind commit in one database transaction, or
// neither does. The preconditions are re-verified inside the transaction —
// the application checked them against its own read, and anything may have
// moved since; a promise only the transaction can keep is kept here.
type Decisions struct{ db *sql.DB }

func NewDecisions(db *sql.DB) *Decisions { return &Decisions{db: db} }

var _ drepo.SkillDecisions = (*Decisions)(nil)

func (r *Decisions) Commit(ctx context.Context, p *aggregate.SkillProposal, row *aggregate.SkillOwnership) error {
	// Input validation before anything writes: the decision must be one, and
	// the row must be exactly the one the decision leaves behind.
	switch p.Status() {
	case enum.SkillProposalAccepted:
		if row == nil {
			return apperror.New(400, "an accepted proposal commits with its ownership row", nil)
		}
	case enum.SkillProposalDismissed:
		if row != nil {
			return apperror.New(400, "only an accepted proposal commits an ownership row", nil)
		}
	default:
		return apperror.New(400, "a pending proposal has no decision to commit", nil)
	}
	if row != nil {
		name := p.Name()
		if p.Op() == enum.SkillProposalPatch {
			name = p.TargetName()
		}
		switch {
		case row.Root() != enum.SkillRootNusashell:
			return apperror.New(400, "a learned skill's row lives in the nusashell root, not "+string(row.Root()), nil)
		case row.Name() != name:
			return apperror.New(400, "the ownership row is not for the skill this proposal names", nil)
		case row.Level() != p.AcceptedLevel():
			return apperror.New(400, "the ownership row's level is not the level the owner accepted", nil)
		}
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// A missing proposal and a decided one both write nothing, but they are
	// not the same answer; the transaction reads the status first.
	var status string
	err = tx.QueryRowContext(ctx, `SELECT status FROM skill_proposals WHERE id = ?`, p.ID()).Scan(&status)
	if err == sql.ErrNoRows {
		return apperror.New(404, "no such skill proposal", err)
	}
	if err != nil {
		return err
	}
	if status != string(enum.SkillProposalPending) {
		return apperror.New(409, "this skill proposal was already decided", nil)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE skill_proposals
		SET status = ?, accepted_path = ?, accepted_level = ?, dismiss_reason = ?, decided_at = ?
		WHERE id = ?`,
		string(p.Status()), p.AcceptedPath(), string(p.AcceptedLevel()), p.DismissReason(), ms(p.DecidedAt()), p.ID()); err != nil {
		return err
	}

	if row != nil {
		if err := commitRow(ctx, tx, p, row); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// commitRow persists the accept's ownership row with its precondition inside
// the transaction: a create's name must still be free, a patch's row must
// still carry the digest and level the decision was read against.
func commitRow(ctx context.Context, tx *sql.Tx, p *aggregate.SkillProposal, row *aggregate.SkillOwnership) error {
	if p.Op() == enum.SkillProposalCreate {
		_, err := tx.ExecContext(ctx, `INSERT INTO skill_ownership (root, name, level, digest) VALUES (?, ?, ?, ?)`,
			string(row.Root()), row.Name(), string(row.Level()), row.Digest())
		if isUniqueViolation(err) {
			return apperror.New(409, "the skill's name was taken while the decision was written", err)
		}
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE skill_ownership SET level = ?, digest = ?
		WHERE root = ? AND name = ? AND digest = ? AND level = ?`,
		string(row.Level()), row.Digest(), string(row.Root()), row.Name(), p.TargetDigest(), string(p.TargetLevel()))
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return apperror.New(409, "the ownership row moved while the decision was written", nil)
	}
	return nil
}
