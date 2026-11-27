package reposkill

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Proposals keeps learned-skill proposals in the application database. It owns
// the row encoding only; what a proposal may say is the aggregate's decision.
type Proposals struct{ db *sql.DB }

func NewProposals(db *sql.DB) *Proposals { return &Proposals{db: db} }

var _ drepo.SkillProposals = (*Proposals)(nil)

const proposalCols = `id, op, kind, name, description, body, reason, evidence, evidence_kind, tool,
	target_name, target_digest, target_level, status, accepted_path, accepted_level, dismiss_reason,
	created_at, decided_at, decided_open, evidence_at`

func (r *Proposals) Add(ctx context.Context, p *aggregate.SkillProposal) error {
	if p.Status() != enum.SkillProposalPending {
		return apperror.New(400, "only a pending proposal is added; a decided one is a commit", nil)
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO skill_proposals (`+proposalCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID(), string(p.Op()), string(p.Kind()), p.Name(), p.Description(), p.Body(), p.Reason(), p.Evidence(),
		string(p.EvidenceKind()), p.Tool(), p.TargetName(), p.TargetDigest(), string(p.TargetLevel()),
		string(p.Status()), p.AcceptedPath(), string(p.AcceptedLevel()), p.DismissReason(), ms(p.CreatedAt()), ms(p.DecidedAt()),
		boolInt(p.DecidedOpen()), ms(p.EvidenceAt()))
	if err != nil && isUniqueViolation(err) {
		// The one-pending index and the primary key both surface as unique
		// violations; the column in the message says which one fired.
		if strings.Contains(err.Error(), "skill_proposals.name") {
			return apperror.New(409, "a pending proposal for this skill already exists", err)
		}
		return apperror.New(409, "a proposal with this id already exists", err)
	}
	return err
}

func (r *Proposals) Get(ctx context.Context, id string) (*aggregate.SkillProposal, error) {
	p, err := scanProposal(r.db.QueryRowContext(ctx, `SELECT `+proposalCols+` FROM skill_proposals WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.New(404, "no such skill proposal", err)
	}
	return p, err
}

func (r *Proposals) Pending(ctx context.Context) ([]*aggregate.SkillProposal, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+proposalCols+` FROM skill_proposals
		WHERE status = 'pending' ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*aggregate.SkillProposal
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Seen is the candidate's cooldown, so the pass does not ask the owner twice. A
// pending proposal holds its name whatever its op, as the one-pending index does.
// An acceptance holds the name for its op inside the window. A dismissal does too,
// unless the proposal had already gone stale when the owner closed it, or the pass
// retired it: neither is a refusal of the procedure, so neither holds anything.
func (r *Proposals) Seen(ctx context.Context, name string, op enum.SkillProposalOp, since time.Time) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM skill_proposals
		WHERE name = ? AND (status = 'pending'
			OR (op = ? AND decided_at >= ? AND dismiss_reason NOT GLOB ?
				AND (status = 'accepted' OR decided_open = 1)))`,
		name, string(op), ms(since), aggregate.SupersededReasonPrefix+"*").Scan(&n)
	return n > 0, err
}

// Declined answers the candidate prompt with the owner's refusals: dismissals of
// proposals that were still open, decided since the cutoff, newest first. A
// retirement and a stale close are not refusals, as in Seen.
func (r *Proposals) Declined(ctx context.Context, since time.Time, limit int) ([]*aggregate.SkillProposal, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+proposalCols+` FROM skill_proposals
		WHERE status = 'dismissed' AND decided_open = 1 AND dismiss_reason NOT GLOB ? AND decided_at >= ?
		ORDER BY decided_at DESC, id DESC LIMIT ?`,
		aggregate.SupersededReasonPrefix+"*", ms(since), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*aggregate.SkillProposal
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func scanProposal(s interface{ Scan(...any) error }) (*aggregate.SkillProposal, error) {
	var id, op, kind, name, desc, body, reason, evidence, evidenceKind, tool string
	var targetName, targetDigest, targetLevel, status, acceptedPath, acceptedLevel, dismissReason string
	var created, decided, evidenceAt int64
	var decidedOpen int
	if err := s.Scan(&id, &op, &kind, &name, &desc, &body, &reason, &evidence, &evidenceKind, &tool,
		&targetName, &targetDigest, &targetLevel, &status, &acceptedPath, &acceptedLevel, &dismissReason,
		&created, &decided, &decidedOpen, &evidenceAt); err != nil {
		return nil, err
	}
	p, err := aggregate.NewSkillProposal(id, aggregate.SkillProposalInput{
		Op: enum.SkillProposalOp(op), Kind: enum.SkillKind(kind), Tool: tool,
		Name: name, Description: desc, Body: body, Reason: reason,
		Evidence: evidence, EvidenceKind: enum.SkillEvidenceKind(evidenceKind), EvidenceAt: at(evidenceAt),
		TargetName: targetName, TargetDigest: targetDigest, TargetLevel: enum.SkillLevel(targetLevel),
	}, at(created))
	if err != nil {
		return nil, err
	}
	if err := p.SetStored(enum.SkillProposalStatus(status), acceptedPath, enum.SkillLevel(acceptedLevel), dismissReason, at(created), at(decided), decidedOpen != 0); err != nil {
		return nil, err
	}
	return p, nil
}
