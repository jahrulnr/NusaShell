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

// Ownership keeps the ownership rows of learned skills in the application
// database. The row is the loop's write authority; the aggregate decides what
// may change it, this store only persists that decision exactly.
type Ownership struct{ db *sql.DB }

func NewOwnership(db *sql.DB) *Ownership { return &Ownership{db: db} }

var _ drepo.SkillOwnership = (*Ownership)(nil)

func (r *Ownership) Get(ctx context.Context, root enum.SkillRoot, name string) (*aggregate.SkillOwnership, error) {
	var level, digest string
	err := r.db.QueryRowContext(ctx, `SELECT level, digest FROM skill_ownership WHERE root = ? AND name = ?`,
		string(root), name).Scan(&level, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.New(404, "no ownership row for this skill", err)
	}
	if err != nil {
		return nil, err
	}
	return aggregate.NewSkillOwnership(root, name, enum.SkillLevel(level), digest)
}

func (r *Ownership) List(ctx context.Context) ([]*aggregate.SkillOwnership, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT root, name, level, digest FROM skill_ownership`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*aggregate.SkillOwnership
	for rows.Next() {
		var root, name, level, digest string
		if err := rows.Scan(&root, &name, &level, &digest); err != nil {
			return nil, err
		}
		o, err := aggregate.NewSkillOwnership(enum.SkillRoot(root), name, enum.SkillLevel(level), digest)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Save is the compare-and-swap the port contracts: an empty expectDigest
// inserts only where no row exists, a non-empty one updates only while the
// stored digest still equals it. The observed flip-to-user goes through here;
// an accept's row never does — that write belongs to the decision transaction.
func (r *Ownership) Save(ctx context.Context, o *aggregate.SkillOwnership, expectDigest string) error {
	if expectDigest == "" {
		_, err := r.db.ExecContext(ctx, `INSERT INTO skill_ownership (root, name, level, digest) VALUES (?, ?, ?, ?)`,
			string(o.Root()), o.Name(), string(o.Level()), o.Digest())
		if isUniqueViolation(err) {
			return apperror.New(409, "an ownership row for this skill already exists", err)
		}
		return err
	}
	res, err := r.db.ExecContext(ctx, `UPDATE skill_ownership SET level = ?, digest = ?
		WHERE root = ? AND name = ? AND digest = ?`,
		string(o.Level()), o.Digest(), string(o.Root()), o.Name(), expectDigest)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	var count int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM skill_ownership WHERE root = ? AND name = ?`,
		string(o.Root()), o.Name()).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return apperror.New(404, "no ownership row for this skill", nil)
	}
	return apperror.New(409, "the ownership row moved since it was read", nil)
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func at(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v)
}
