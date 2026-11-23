// Package repomemory keeps memories in the application database: a table,
// an FTS5 index the migration's triggers keep in step, and each memory's
// embedding as a little-endian float32 blob.
package repomemory

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// maxList bounds one listing.
const maxList = 1000

type Repository struct{ db *sql.DB }

func New(db *sql.DB) *Repository { return &Repository{db: db} }

var _ drepo.Memories = (*Repository)(nil)

const cols = `m.id, m.scope, m.type, m.name, m.description, m.body, m.source, m.origin,
	m.created_at, m.updated_at, m.verified_at, m.recalled_at, m.recalls, m.status, m.reason, m.archived_at`

func (r *Repository) Save(ctx context.Context, m *aggregate.Memory) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO memories
		(id, scope, type, name, description, body, source, origin, created_at, updated_at, verified_at,
		 recalled_at, recalls, status, reason, archived_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			scope = excluded.scope, type = excluded.type, name = excluded.name,
			embedding = CASE WHEN memories.description = excluded.description AND memories.body = excluded.body
				AND memories.name = excluded.name THEN memories.embedding ELSE NULL END,
			embedding_model = CASE WHEN memories.description = excluded.description AND memories.body = excluded.body
				AND memories.name = excluded.name THEN memories.embedding_model ELSE '' END,
			description = excluded.description, body = excluded.body,
			source = excluded.source, origin = excluded.origin,
			updated_at = excluded.updated_at, verified_at = excluded.verified_at,
			recalled_at = excluded.recalled_at, recalls = excluded.recalls,
			status = excluded.status, reason = excluded.reason, archived_at = excluded.archived_at`,
		m.ID(), m.Scope().String(), string(m.Type()), m.Name(), m.Description(), m.Body(), string(m.Source()), m.Origin(),
		ms(m.CreatedAt()), ms(m.UpdatedAt()), ms(m.VerifiedAt()), ms(m.RecalledAt()), m.Recalls(),
		string(m.Status()), m.Reason(), ms(m.ArchivedAt()))
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return apperror.New(409, "a memory named "+m.Name()+" already exists in "+m.Scope().String(), err)
	}
	return err
}

func (r *Repository) Get(ctx context.Context, id string) (*aggregate.Memory, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+cols+` FROM memories m WHERE m.id = ?`, id)
	m, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.New(404, "memory not found", nil)
	}
	return m, err
}

func (r *Repository) FindByName(ctx context.Context, scope valueobject.MemoryScope, name string) (*aggregate.Memory, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+cols+` FROM memories m WHERE m.scope = ? AND m.name = ? AND m.status <> 'archived'`,
		scope.String(), name)
	m, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.New(404, "no memory named "+name+" in "+scope.String(), nil)
	}
	return m, err
}

func (r *Repository) List(ctx context.Context, f drepo.MemoryFilter) ([]drepo.MemoryHit, error) {
	limit := f.Limit
	if limit <= 0 || limit > maxList {
		limit = maxList
	}
	var where []string
	var args []any
	if len(f.Scopes) > 0 {
		where = append(where, "m.scope IN ("+marks(len(f.Scopes))+")")
		for _, s := range f.Scopes {
			args = append(args, s.String())
		}
	}
	if len(f.Types) > 0 {
		where = append(where, "m.type IN ("+marks(len(f.Types))+")")
		for _, t := range f.Types {
			args = append(args, string(t))
		}
	}
	if len(f.Statuses) > 0 {
		where = append(where, "m.status IN ("+marks(len(f.Statuses))+")")
		for _, s := range f.Statuses {
			args = append(args, string(s))
		}
	} else {
		where = append(where, "m.status <> 'archived'")
	}
	match := ftsQuery(f.Query)
	query := `SELECT ` + cols + `, 0 FROM memories m`
	order := ` ORDER BY m.updated_at DESC, m.seq DESC`
	if match != "" {
		// bm25 is lower-is-better and negative; name and description
		// weigh more than the body, since they say what a memory is.
		query = `SELECT ` + cols + `, bm25(memories_fts, 4.0, 2.0, 1.0) FROM memories_fts JOIN memories m ON m.seq = memories_fts.rowid`
		where = append([]string{"memories_fts MATCH ?"}, where...)
		args = append([]any{match}, args...)
		order = ` ORDER BY 17`
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, query+order+` LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []drepo.MemoryHit
	for rows.Next() {
		var rank float64
		m, err := scanRow(rows, &rank)
		if err != nil {
			return nil, err
		}
		score := 0.0
		if match != "" {
			// Map bm25 (≤ 0, more negative is better) into (0, 1].
			score = -rank / (1 - rank)
			if score <= 0 {
				score = 1e-6
			}
		}
		hits = append(hits, drepo.MemoryHit{Memory: m, Score: score})
	}
	return hits, rows.Err()
}

func (r *Repository) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM memories WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperror.New(404, "memory not found", nil)
	}
	return nil
}

func (r *Repository) DeleteScope(ctx context.Context, scope valueobject.MemoryScope) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM memories WHERE scope = ?`, scope.String())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *Repository) PurgeArchived(ctx context.Context, before time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM memories WHERE status = 'archived' AND archived_at < ?`, ms(before))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *Repository) SetEmbedding(ctx context.Context, id, model string, vector []float32) error {
	blob := make([]byte, 4*len(vector))
	for i, v := range vector {
		binary.LittleEndian.PutUint32(blob[4*i:], math.Float32bits(v))
	}
	_, err := r.db.ExecContext(ctx, `UPDATE memories SET embedding = ?, embedding_model = ? WHERE id = ?`, blob, model, id)
	return err
}

func (r *Repository) Embeddings(ctx context.Context, model string, scopes []valueobject.MemoryScope) (map[string][]float32, error) {
	query := `SELECT id, embedding FROM memories WHERE embedding IS NOT NULL AND embedding_model = ? AND status <> 'archived'`
	args := []any{model}
	if len(scopes) > 0 {
		query += " AND scope IN (" + marks(len(scopes)) + ")"
		for _, s := range scopes {
			args = append(args, s.String())
		}
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]float32{}
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, err
		}
		if len(blob)%4 != 0 {
			continue
		}
		v := make([]float32, len(blob)/4)
		for i := range v {
			v[i] = math.Float32frombits(binary.LittleEndian.Uint32(blob[4*i:]))
		}
		out[id] = v
	}
	return out, rows.Err()
}

// ftsQuery turns free text into an FTS5 query: every word a quoted
// prefix term, any of them matching — bm25 ranks memories that hold more
// of them higher. Words are letters and digits only, so the query
// carries no FTS5 syntax of the caller's.
func ftsQuery(q string) string {
	words := strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	terms := make([]string, 0, len(words))
	seen := map[string]bool{}
	for _, w := range words {
		if seen[w] || len(terms) == 32 {
			continue
		}
		seen[w] = true
		terms = append(terms, `"`+w+`"*`)
	}
	return strings.Join(terms, " OR ")
}

func scan(row *sql.Row) (*aggregate.Memory, error) {
	return scanRow(row, nil)
}

func scanRow(s interface{ Scan(...any) error }, rank *float64) (*aggregate.Memory, error) {
	var id, scope, mtype, name, desc, body, source, origin, status, reason string
	var created, updated, verified, recalled, archived int64
	var recalls int
	dest := []any{&id, &scope, &mtype, &name, &desc, &body, &source, &origin,
		&created, &updated, &verified, &recalled, &recalls, &status, &reason, &archived}
	if rank != nil {
		dest = append(dest, rank)
	}
	if err := s.Scan(dest...); err != nil {
		return nil, err
	}
	sc, err := valueobject.ParseMemoryScope(scope)
	if err != nil {
		return nil, err
	}
	m, err := aggregate.NewMemory(id, sc, enum.MemoryType(mtype), name, desc, body, enum.MemorySource(source), origin, at(created))
	if err != nil {
		return nil, err
	}
	m.SetStored(at(created), at(updated), at(verified), at(recalled), recalls, enum.MemoryStatus(status), reason, at(archived))
	return m, nil
}

func marks(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
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
