// Package repoautomation implements the Automation repository port on the
// application database (see infrastructure/repository/store).
package repoautomation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Repository is the SQLite Automation store.
type Repository struct{ db *sql.DB }

// New wraps an opened application database.
func New(db *sql.DB) *Repository { return &Repository{db: db} }

var _ drepo.Automation = (*Repository)(nil)

const columns = `id, name, prompt, agent_id, provider_id, model_id, schedule_kind, cron, run_at, timezone, thread,
	conversation_id, project_id, notify_channel, notify_chat, notify_thread, expires_at, enabled, next_run_at, created_at, updated_at, version`

// nanos stores a time as Unix nanoseconds; zero stays 0.
func nanos(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

func fromNanos(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}

func values(a *aggregate.Automation) []any {
	s := a.Schedule()
	var ch, chat, thread string
	if n := a.Notify(); n != nil {
		ch, chat, thread = n.Channel(), n.ChatID(), n.Thread()
	}
	return []any{a.ID(), a.Name(), a.Prompt(), a.AgentID(), a.ProviderID(), a.ModelID(), string(s.Kind()), s.Cron(), nanos(s.At()), s.Zone(),
		a.Thread().String(), a.ConversationID(), a.ProjectID(), ch, chat, thread, nanos(a.ExpiresAt()), a.Enabled(), nanos(a.NextRunAt()),
		nanos(a.CreatedAt()), nanos(a.UpdatedAt()), a.Version()}
}

func (r *Repository) Create(ctx context.Context, a *aggregate.Automation) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO automations (`+columns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, values(a)...)
	if err != nil {
		if _, getErr := r.Get(ctx, a.ID()); getErr == nil {
			return apperror.New(409, "automation already exists", err)
		}
	}
	return err
}

func (r *Repository) Update(ctx context.Context, a *aggregate.Automation, readAt time.Time) error {
	// The owner-facing token is the updated_at the edit was based on; the
	// version read with it keeps any write in between from being lost.
	v := values(a)
	args := append(v[1:len(v)-1], a.ID(), nanos(readAt), a.Version())
	res, err := r.db.ExecContext(ctx, `UPDATE automations SET name = ?, prompt = ?, agent_id = ?, provider_id = ?, model_id = ?, schedule_kind = ?, cron = ?,
		run_at = ?, timezone = ?, thread = ?, conversation_id = ?, project_id = ?, notify_channel = ?, notify_chat = ?, notify_thread = ?,
		expires_at = ?, enabled = ?, next_run_at = ?, created_at = ?, updated_at = ?, version = version + 1
		WHERE id = ? AND updated_at = ? AND version = ?`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		a.BumpVersion()
		return nil
	}
	if _, err := r.Get(ctx, a.ID()); err != nil {
		return err
	}
	return apperror.New(409, "the automation was changed elsewhere — reload it and try again", aggregate.ErrAutomationChanged)
}

func (r *Repository) Get(ctx context.Context, id string) (*aggregate.Automation, error) {
	a, err := scan(r.db.QueryRowContext(ctx, `SELECT `+columns+` FROM automations WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.NewNotFoundError("automation "+id, err)
	}
	return a, err
}

func (r *Repository) List(ctx context.Context) ([]aggregate.Automation, error) {
	return r.query(ctx, `SELECT `+columns+` FROM automations ORDER BY created_at DESC, id`)
}

// Due leaves out automations past their expiry and ones with a run still
// going — that firing is claimed once the run ends, the same collapse a
// missed firing gets after downtime.
func (r *Repository) Due(ctx context.Context, now time.Time) ([]aggregate.Automation, error) {
	return r.query(ctx, `SELECT `+columns+` FROM automations WHERE enabled = 1 AND next_run_at > 0 AND next_run_at <= ?
		AND (expires_at = 0 OR expires_at > ?)
		AND NOT EXISTS (SELECT 1 FROM automation_runs r WHERE r.automation_id = automations.id AND r.status = ?)
		ORDER BY next_run_at`, now.UnixNano(), now.UnixNano(), enum.RunRunning.String())
}

func (r *Repository) query(ctx context.Context, q string, args ...any) ([]aggregate.Automation, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []aggregate.Automation
	for rows.Next() {
		a, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func (r *Repository) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM automations WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return apperror.NewNotFoundError("automation "+id, sql.ErrNoRows)
	}
	return nil
}

// Claim moves the claimed firing to the next one and records its run in
// one transaction. The version guard refuses a row changed since it was
// read; the running-run check makes "one run at a time" atomic — a firing
// it refuses stays due and runs once after the current run ends.
func (r *Repository) Claim(ctx context.Context, a *aggregate.Automation, run *entity.AutomationRun) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE automations SET next_run_at = ?, version = version + 1
		WHERE id = ? AND version = ?
		AND NOT EXISTS (SELECT 1 FROM automation_runs WHERE automation_id = ? AND status = ?)`,
		nanos(a.NextRunAt()), a.ID(), a.Version(), a.ID(), enum.RunRunning.String())
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return false, nil
	}
	if err := saveRun(ctx, tx, run); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	a.BumpVersion()
	return true, nil
}

func (r *Repository) KeepConversation(ctx context.Context, id, conversationID string, version int64) (bool, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE automations SET conversation_id = ?, version = version + 1 WHERE id = ? AND version = ?`,
		conversationID, id, version)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// StartRun inserts run only when the automation has no running run —
// "one run at a time" enforced in the insert itself.
func (r *Repository) StartRun(ctx context.Context, run *entity.AutomationRun) (bool, error) {
	res, err := r.db.ExecContext(ctx, `INSERT INTO automation_runs (id, automation_id, scheduled_at, started_at, ended_at, conversation_id, status, note)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?
		WHERE NOT EXISTS (SELECT 1 FROM automation_runs WHERE automation_id = ? AND status = ?)`,
		run.ID(), run.AutomationID(), nanos(run.ScheduledAt()), nanos(run.StartedAt()), nanos(run.EndedAt()), run.ConversationID(),
		run.Status().String(), run.Note(), run.AutomationID(), enum.RunRunning.String())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func saveRun(ctx context.Context, ex interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, run *entity.AutomationRun) error {
	_, err := ex.ExecContext(ctx, `INSERT INTO automation_runs (id, automation_id, scheduled_at, started_at, ended_at, conversation_id, status, note)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET ended_at = excluded.ended_at, conversation_id = excluded.conversation_id, status = excluded.status, note = excluded.note`,
		run.ID(), run.AutomationID(), nanos(run.ScheduledAt()), nanos(run.StartedAt()), nanos(run.EndedAt()), run.ConversationID(), run.Status().String(), run.Note())
	return err
}

func (r *Repository) SaveRun(ctx context.Context, run *entity.AutomationRun) error {
	return saveRun(ctx, r.db, run)
}

func (r *Repository) Runs(ctx context.Context, automationID string, limit int) ([]entity.AutomationRun, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, automation_id, scheduled_at, started_at, ended_at, conversation_id, status, note
		FROM automation_runs WHERE automation_id = ? ORDER BY started_at DESC, id LIMIT ?`, automationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []entity.AutomationRun
	for rows.Next() {
		var id, aid, conv, status, note string
		var sched, start, end int64
		if err := rows.Scan(&id, &aid, &sched, &start, &end, &conv, &status, &note); err != nil {
			return nil, err
		}
		st, ok := enum.ParseRunStatus(status)
		if !ok {
			return nil, fmt.Errorf("run %q has unknown status %q", id, status)
		}
		out = append(out, *entity.RestoreAutomationRun(id, aid, fromNanos(sched), fromNanos(start), fromNanos(end), conv, st, note))
	}
	return out, rows.Err()
}

func (r *Repository) PruneRuns(ctx context.Context, automationID string, keep int) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM automation_runs WHERE automation_id = ? AND id NOT IN (
		SELECT id FROM automation_runs WHERE automation_id = ? ORDER BY started_at DESC, id LIMIT ?)`, automationID, automationID, keep)
	return err
}

func (r *Repository) InterruptRuns(ctx context.Context, at time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE automation_runs SET status = ?, ended_at = ?, note = 'The server stopped while this run was going; check its thread.' WHERE status = ?`,
		enum.RunInterrupted.String(), at.UnixNano(), enum.RunRunning.String())
	return err
}

type scanner interface{ Scan(dest ...any) error }

func scan(s scanner) (*aggregate.Automation, error) {
	var id, name, prompt, agent, provider, model, kind, cronExpr, zone, thread, conv, project, nch, nchat, nthread string
	var runAt, expires, next, created, updated, version int64
	var enabled bool
	if err := s.Scan(&id, &name, &prompt, &agent, &provider, &model, &kind, &cronExpr, &runAt, &zone, &thread,
		&conv, &project, &nch, &nchat, &nthread, &expires, &enabled, &next, &created, &updated, &version); err != nil {
		return nil, err
	}
	corrupt := func(err error) error { return fmt.Errorf("automation %q corrupt: %w", id, err) }
	a, err := aggregate.NewAutomation(id, name, prompt, fromNanos(created))
	if err != nil {
		return nil, corrupt(err)
	}
	if err := a.SetRuntime(agent, provider, model); err != nil {
		return nil, corrupt(err)
	}
	var sched valueobject.Schedule
	switch valueobject.ScheduleKind(kind) {
	case valueobject.ScheduleOnce:
		sched, err = valueobject.NewOnceSchedule(fromNanos(runAt), zone)
	case valueobject.ScheduleCron:
		sched, err = valueobject.NewCronSchedule(cronExpr, zone)
	default:
		err = fmt.Errorf("unknown schedule kind %q", kind)
	}
	if err != nil {
		return nil, corrupt(err)
	}
	t, ok := enum.ParseAutomationThread(thread)
	if !ok {
		return nil, corrupt(fmt.Errorf("unknown thread mode %q", thread))
	}
	a.SetThread(t)
	a.SetProject(project)
	if nch != "" {
		chat, err := valueobject.NewChatRef(nch, nchat, nthread)
		if err != nil {
			return nil, corrupt(err)
		}
		a.SetNotify(&chat)
	}
	// Stored state is trusted as-is — an expiry already passed must
	// hydrate, not re-validate.
	a.Restore(sched, enabled, fromNanos(next), conv, fromNanos(expires), fromNanos(created), fromNanos(updated), version)
	return a, nil
}
