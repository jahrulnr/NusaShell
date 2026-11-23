// Package repomcpserver stores the owner's MCP servers: the record in the
// application database, environment and header values in the secret store.
package repomcpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Repository is the MCP server store.
type Repository struct {
	db      *sql.DB
	secrets drepo.SecretStore
}

// New wraps the application database and the secret store.
func New(db *sql.DB, secrets drepo.SecretStore) *Repository {
	return &Repository{db: db, secrets: secrets}
}

var _ drepo.MCPServer = (*Repository)(nil)

const cols = `id, name, transport, command, args, url, enabled, created_at, updated_at`

// values are the secret part of a server.
type values struct {
	Env     map[string]string `json:"env,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

func secretKey(id string) string { return "mcp/" + id }

func (r *Repository) saveValues(ctx context.Context, s *aggregate.MCPServer) error {
	raw, err := json.Marshal(values{Env: s.Env(), Headers: s.Headers()})
	if err != nil {
		return err
	}
	return r.secrets.Set(ctx, secretKey(s.ID()), string(raw))
}

func uniqueName(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: mcp_servers.name")
}

func (r *Repository) Create(ctx context.Context, s *aggregate.MCPServer) error {
	args, err := json.Marshal(s.Args())
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO mcp_servers (`+cols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID(), s.Name(), s.Transport().String(), s.Command(), string(args), s.URL(), s.Enabled(),
		s.CreatedAt().UnixNano(), s.UpdatedAt().UnixNano())
	if uniqueName(err) {
		return apperror.New(409, "an MCP server named "+s.Name()+" already exists", err)
	}
	if err != nil {
		return err
	}
	return r.saveValues(ctx, s)
}

func (r *Repository) Update(ctx context.Context, s *aggregate.MCPServer, readAt time.Time) error {
	args, err := json.Marshal(s.Args())
	if err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx, `UPDATE mcp_servers SET name = ?, transport = ?, command = ?, args = ?, url = ?, enabled = ?, updated_at = ?
		WHERE id = ? AND updated_at = ?`,
		s.Name(), s.Transport().String(), s.Command(), string(args), s.URL(), s.Enabled(), s.UpdatedAt().UnixNano(), s.ID(), readAt.UnixNano())
	if uniqueName(err) {
		return apperror.New(409, "an MCP server named "+s.Name()+" already exists", err)
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := r.Get(ctx, s.ID()); err != nil {
			return err
		}
		return apperror.New(409, "the MCP server was changed elsewhere — reload it and try again", aggregate.ErrMCPServerChanged)
	}
	return r.saveValues(ctx, s)
}

func (r *Repository) Get(ctx context.Context, id string) (*aggregate.MCPServer, error) {
	s, err := r.scan(ctx, r.db.QueryRowContext(ctx, `SELECT `+cols+` FROM mcp_servers WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NewNotFoundError("MCP server "+id, err)
	}
	return s, err
}

func (r *Repository) List(ctx context.Context) ([]aggregate.MCPServer, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+cols+` FROM mcp_servers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []aggregate.MCPServer
	for rows.Next() {
		s, err := r.scan(ctx, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

func (r *Repository) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM mcp_servers WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperror.NewNotFoundError("MCP server "+id, sql.ErrNoRows)
	}
	if err := r.secrets.Delete(ctx, secretKey(id)); err != nil && apperror.As(err).StatusCode() != 404 {
		return err
	}
	return nil
}

func (r *Repository) scan(ctx context.Context, row interface{ Scan(...any) error }) (*aggregate.MCPServer, error) {
	var id, name, transport, command, argsJSON, url string
	var enabled bool
	var created, updated int64
	if err := row.Scan(&id, &name, &transport, &command, &argsJSON, &url, &enabled, &created, &updated); err != nil {
		return nil, err
	}
	corrupt := func(err error) error { return fmt.Errorf("MCP server %q corrupt: %w", id, err) }
	var args []string
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return nil, corrupt(err)
	}
	var v values
	if raw, err := r.secrets.Get(ctx, secretKey(id)); err == nil {
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, corrupt(err)
		}
	} else if apperror.As(err).StatusCode() != 404 {
		return nil, err
	}
	s, err := aggregate.NewMCPServer(id, name, time.Unix(0, created))
	if err != nil {
		return nil, corrupt(err)
	}
	t, ok := enum.ParseMCPTransport(transport)
	if !ok {
		return nil, corrupt(fmt.Errorf("transport %q", transport))
	}
	if t.Remote() {
		err = s.SetRemote(t, url, v.Headers)
	} else {
		err = s.SetStdio(command, args, v.Env)
	}
	if err != nil {
		return nil, corrupt(err)
	}
	s.SetEnabled(enabled)
	s.Restore(time.Unix(0, created), time.Unix(0, updated))
	return s, nil
}
