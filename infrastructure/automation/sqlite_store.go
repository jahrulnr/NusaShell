package automation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nusashell/application"
	"nusashell/domain"
	clock "nusashell/pkg/time"

	_ "modernc.org/sqlite"
)

// SQLite is the durable automation/Automation store.
type SQLite struct {
	db *sql.DB
}

func OpenSQLite(path string) (*SQLite, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// One connection: PRAGMA settings (journal_mode, busy_timeout) are
	// per-connection, and a multi-connection pool would hit SQLITE_BUSY
	// because only the first connection carries the busy timeout.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS workflows (id TEXT PRIMARY KEY, json TEXT NOT NULL, updated_at TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS runs (id TEXT PRIMARY KEY, workflow_id TEXT, workspace TEXT, status TEXT, json TEXT NOT NULL, created_at TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS schedules (id TEXT PRIMARY KEY, workflow_id TEXT, status TEXT, next_run_at TEXT, json TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS events (id TEXT PRIMARY KEY, json TEXT NOT NULL, created_at TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS deliveries (event_id TEXT, trigger_id TEXT, workflow_id TEXT, run_id TEXT, matched_at TEXT, PRIMARY KEY(event_id, trigger_id, workflow_id));
		CREATE TABLE IF NOT EXISTS waits (id TEXT PRIMARY KEY, status TEXT, wake_at TEXT, event_type TEXT, json TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS logs (job_id TEXT, seq INTEGER, json TEXT NOT NULL, PRIMARY KEY(job_id, seq));
		CREATE TABLE IF NOT EXISTS locks (key TEXT PRIMARY KEY, run_id TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS debounce (id TEXT PRIMARY KEY, at TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS provider_state (provider_id TEXT PRIMARY KEY, disabled INTEGER NOT NULL);
		CREATE INDEX IF NOT EXISTS idx_runs_created ON runs(created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_sched_due ON schedules(status, next_run_at);
		CREATE INDEX IF NOT EXISTS idx_waits_due ON waits(status, wake_at);
	`); err != nil {
		db.Close()
		return nil, err
	}
	// One-time TaskState migration: JobRun's "FailureReason" key was
	// renamed to TaskState's "Error" in the 2026-08-31 domain
	// normalization (docs/decisions/003-agent-engine.md). Old rows still
	// load — encoding/json ignores unknown keys — but would silently lose
	// the failure message. Idempotent: only rows still carrying the old
	// key are rewritten.
	if err := migrateTaskStateJSON(db); err != nil {
		db.Close()
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	return &SQLite{db: db}, nil
}

func (s *SQLite) Close() error { return s.db.Close() }

func (s *SQLite) Put(ctx context.Context, w *domain.WorkflowDefinition) error {
	b, _ := json.Marshal(w)
	_, err := s.db.ExecContext(ctx, `INSERT INTO workflows(id,json,updated_at) VALUES(?,?,?)
		ON CONFLICT(id) DO UPDATE SET json=excluded.json, updated_at=excluded.updated_at`, w.ID, string(b), clock.NewTime().RFC3339())
	return err
}

func (s *SQLite) Get(ctx context.Context, id string) (*domain.WorkflowDefinition, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT json FROM workflows WHERE id=?`, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("workflow %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	var w domain.WorkflowDefinition
	return &w, json.Unmarshal([]byte(raw), &w)
}

func (s *SQLite) List(ctx context.Context) ([]*domain.WorkflowDefinition, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT json FROM workflows ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.WorkflowDefinition
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var w domain.WorkflowDefinition
		if err := json.Unmarshal([]byte(raw), &w); err != nil {
			return nil, err
		}
		cp := w
		out = append(out, &cp)
	}
	return out, rows.Err()
}

func (s *SQLite) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM workflows WHERE id=?`, id)
	return err
}

func (s *SQLite) Create(ctx context.Context, run *domain.WorkflowRun) error {
	b, _ := json.Marshal(run)
	_, err := s.db.ExecContext(ctx, `INSERT INTO runs(id,workflow_id,workspace,status,json,created_at) VALUES(?,?,?,?,?,?)`,
		run.ID, run.WorkflowID, run.Workspace, string(run.Status), string(b), clock.NewTime(run.CreatedAt).RFC3339())
	return err
}

func (s *SQLite) GetRun(ctx context.Context, id string) (*domain.WorkflowRun, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT json FROM runs WHERE id=?`, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("run %s not found", id)
	}
	if err != nil {
		return nil, err
	}
	var r domain.WorkflowRun
	return &r, json.Unmarshal([]byte(raw), &r)
}

func (s *SQLite) ListRuns(ctx context.Context, filter application.RunFilter) ([]*domain.WorkflowRun, error) {
	q := `SELECT json FROM runs WHERE 1=1`
	var args []any
	if filter.WorkflowID != "" {
		q += ` AND workflow_id=?`
		args = append(args, filter.WorkflowID)
	}
	if filter.Workspace != "" {
		q += ` AND workspace=?`
		args = append(args, filter.Workspace)
	}
	if filter.Status != "" {
		q += ` AND status=?`
		args = append(args, string(filter.Status))
	}
	q += ` ORDER BY created_at DESC`
	if filter.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, filter.Limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.WorkflowRun
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var r domain.WorkflowRun
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			return nil, err
		}
		cp := r
		out = append(out, &cp)
	}
	return out, rows.Err()
}

func (s *SQLite) Update(ctx context.Context, run *domain.WorkflowRun) error {
	b, _ := json.Marshal(run)
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET workflow_id=?, workspace=?, status=?, json=? WHERE id=?`,
		run.WorkflowID, run.Workspace, string(run.Status), string(b), run.ID)
	return err
}

func (s *SQLite) PutSchedule(ctx context.Context, rec *domain.ScheduleRecord) error {
	b, _ := json.Marshal(rec)
	_, err := s.db.ExecContext(ctx, `INSERT INTO schedules(id,workflow_id,status,next_run_at,json) VALUES(?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET workflow_id=excluded.workflow_id, status=excluded.status, next_run_at=excluded.next_run_at, json=excluded.json`,
		rec.ID, rec.WorkflowID, string(rec.Status), clock.NewTime(rec.NextRunAt).Format(time.RFC3339Nano), string(b))
	return err
}

func (s *SQLite) DueSchedules(ctx context.Context, now time.Time, limit int) ([]*domain.ScheduleRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT json FROM schedules WHERE status=? AND next_run_at<=? ORDER BY next_run_at LIMIT ?`,
		string(domain.SchedulePending), clock.NewTime(now).Format(time.RFC3339Nano), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.ScheduleRecord
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var rec domain.ScheduleRecord
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			return nil, err
		}
		cp := rec
		out = append(out, &cp)
	}
	return out, rows.Err()
}

func (s *SQLite) ClaimSchedule(ctx context.Context, id string, now time.Time) (*domain.ScheduleRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var raw, status string
	err = tx.QueryRowContext(ctx, `SELECT json, status FROM schedules WHERE id=?`, id).Scan(&raw, &status)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if status != string(domain.SchedulePending) {
		return nil, nil
	}
	var rec domain.ScheduleRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, err
	}
	rec.Status = domain.ScheduleFired
	t := clock.NewTime(now).Time()
	rec.FiredAt = &t
	b, _ := json.Marshal(rec)
	// Compare-and-set: only the writer that flips pending->fired wins;
	// a concurrent claimer sees 0 rows affected and loses cleanly.
	res, err := tx.ExecContext(ctx, `UPDATE schedules SET status=?, json=? WHERE id=? AND status=?`,
		string(rec.Status), string(b), id, string(domain.SchedulePending))
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *SQLite) ListSchedules(ctx context.Context) ([]*domain.ScheduleRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT json FROM schedules`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.ScheduleRecord
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var rec domain.ScheduleRecord
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			return nil, err
		}
		cp := rec
		out = append(out, &cp)
	}
	return out, rows.Err()
}

func (s *SQLite) PutEvent(ctx context.Context, ev *domain.Event) error {
	b, _ := json.Marshal(ev)
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO events(id,json,created_at) VALUES(?,?,?)`, ev.ID, string(b), clock.NewTime(ev.Time).RFC3339())
	return err
}

func (s *SQLite) RecordDelivery(ctx context.Context, eventID, triggerID, workflowID, runID string, matchedAt time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO deliveries(event_id,trigger_id,workflow_id,run_id,matched_at) VALUES(?,?,?,?,?)`,
		eventID, triggerID, workflowID, runID, clock.NewTime(matchedAt).RFC3339())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (s *SQLite) ListEvents(ctx context.Context, limit int) ([]*domain.Event, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT json FROM events ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Event
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var ev domain.Event
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			return nil, err
		}
		cp := ev
		out = append(out, &cp)
	}
	return out, rows.Err()
}

func (s *SQLite) PutWait(ctx context.Context, rec *domain.WaitRecord) error {
	b, _ := json.Marshal(rec)
	wake := ""
	if rec.WakeAt != nil {
		wake = clock.NewTime(*rec.WakeAt).Format(time.RFC3339Nano)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO waits(id,status,wake_at,event_type,json) VALUES(?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET status=excluded.status, wake_at=excluded.wake_at, event_type=excluded.event_type, json=excluded.json`,
		rec.ID, string(rec.Status), wake, rec.EventType, string(b))
	return err
}

func (s *SQLite) DueWaits(ctx context.Context, now time.Time, limit int) ([]*domain.WaitRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT json FROM waits WHERE status=? AND wake_at!='' AND wake_at<=? LIMIT ?`,
		string(domain.SchedulePending), clock.NewTime(now).Format(time.RFC3339Nano), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.WaitRecord
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var rec domain.WaitRecord
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			return nil, err
		}
		cp := rec
		out = append(out, &cp)
	}
	return out, rows.Err()
}

func (s *SQLite) ClaimWait(ctx context.Context, id string) (*domain.WaitRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var raw, status string
	err = tx.QueryRowContext(ctx, `SELECT json, status FROM waits WHERE id=?`, id).Scan(&raw, &status)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if status != string(domain.SchedulePending) {
		return nil, nil
	}
	var rec domain.WaitRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, err
	}
	rec.Status = domain.ScheduleFired
	b, _ := json.Marshal(rec)
	// Compare-and-set guard: exactly one concurrent claimer wins.
	res, err := tx.ExecContext(ctx, `UPDATE waits SET status=?, json=? WHERE id=? AND status=?`,
		string(rec.Status), string(b), id, string(domain.SchedulePending))
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *SQLite) WaitingForEvent(ctx context.Context, eventType string) ([]*domain.WaitRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT json FROM waits WHERE status=? AND event_type=?`, string(domain.SchedulePending), eventType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.WaitRecord
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var rec domain.WaitRecord
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			return nil, err
		}
		cp := rec
		out = append(out, &cp)
	}
	return out, rows.Err()
}

func (s *SQLite) Append(ctx context.Context, chunk domain.LogChunk) error {
	for attempt := 0; attempt < 5; attempt++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		var seq uint64
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM logs WHERE job_id=?`, chunk.JobID).Scan(&seq)
		if err == nil {
			chunk.Sequence = seq + 1 // surface the assigned sequence on the stored chunk
			var b []byte
			b, err = json.Marshal(chunk)
			if err == nil {
				_, err = tx.ExecContext(ctx, `INSERT INTO logs(job_id,seq,json) VALUES(?,?,?)`, chunk.JobID, chunk.Sequence, string(b))
			}
		}
		if err != nil {
			_ = tx.Rollback()
			if !isBusy(err) {
				return err
			}
			if busyBackoff(ctx, attempt) {
				continue
			}
			return ctx.Err()
		}
		if err := tx.Commit(); err != nil {
			if !isBusy(err) {
				return err
			}
			if busyBackoff(ctx, attempt) {
				continue
			}
			return ctx.Err()
		}
		return nil
	}
	return fmt.Errorf("append log chunk: still SQLITE_BUSY after retries")
}

// busyBackoff sleeps before a SQLITE_BUSY retry; false means ctx cancelled.
func busyBackoff(ctx context.Context, attempt int) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(time.Duration(attempt+1) * 10 * time.Millisecond):
		return true
	}
}

// isBusy reports whether err is SQLite SQLITE_BUSY / SQLITE_LOCKED.
func isBusy(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "SQLITE_BUSY") || strings.Contains(s, "database is locked") || strings.Contains(s, "SQLITE_LOCKED")
}

func (s *SQLite) Read(ctx context.Context, jobID string, after uint64, limit int) ([]domain.LogChunk, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT json FROM logs WHERE job_id=? AND seq>? ORDER BY seq LIMIT ?`, jobID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.LogChunk
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var c domain.LogChunk
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *SQLite) Active(ctx context.Context, key string) (string, bool, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT run_id FROM locks WHERE key=?`, key).Scan(&id)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return id, err == nil, err
}

func (s *SQLite) Acquire(ctx context.Context, key, runID string) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO locks(key,run_id) VALUES(?,?)`, key, runID)
	return err
}

func (s *SQLite) Release(ctx context.Context, key, runID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM locks WHERE key=? AND run_id=?`, key, runID)
	return err
}

func (s *SQLite) Last(ctx context.Context, workflowID, triggerID string) (time.Time, bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT at FROM debounce WHERE id=?`, workflowID+"/"+triggerID).Scan(&raw)
	if err == sql.ErrNoRows {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	return t, err == nil, err
}

func (s *SQLite) Touch(ctx context.Context, workflowID, triggerID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO debounce(id,at) VALUES(?,?)`, workflowID+"/"+triggerID, clock.NewTime(at).Format(time.RFC3339Nano))
	return err
}

func (s *SQLite) GetDisabled(ctx context.Context, providerID string) (bool, bool, error) {
	var d int
	err := s.db.QueryRowContext(ctx, `SELECT disabled FROM provider_state WHERE provider_id=?`, providerID).Scan(&d)
	if err == sql.ErrNoRows {
		return false, false, nil
	}
	return d == 1, err == nil, err
}

func (s *SQLite) SetDisabled(ctx context.Context, providerID string, disabled bool) error {
	v := 0
	if disabled {
		v = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO provider_state(provider_id,disabled) VALUES(?,?)`, providerID, v)
	return err
}

// Adapters for colliding method names.

type WorkflowSQL struct{ *SQLite }
type RunSQL struct{ *SQLite }
type ScheduleSQL struct{ *SQLite }
type EventSQL struct{ *SQLite }
type WaitSQL struct{ *SQLite }
type LogSQL struct{ *SQLite }
type LockSQL struct{ *SQLite }
type DebounceSQL struct{ *SQLite }
type ProviderStateSQL struct{ *SQLite }

func (a RunSQL) Get(ctx context.Context, id string) (*domain.WorkflowRun, error) {
	return a.GetRun(ctx, id)
}
func (a RunSQL) List(ctx context.Context, f application.RunFilter) ([]*domain.WorkflowRun, error) {
	return a.ListRuns(ctx, f)
}
func (a ScheduleSQL) Put(ctx context.Context, rec *domain.ScheduleRecord) error {
	return a.PutSchedule(ctx, rec)
}
func (a ScheduleSQL) Due(ctx context.Context, now time.Time, limit int) ([]*domain.ScheduleRecord, error) {
	return a.DueSchedules(ctx, now, limit)
}
func (a ScheduleSQL) Claim(ctx context.Context, id string, now time.Time) (*domain.ScheduleRecord, error) {
	return a.ClaimSchedule(ctx, id, now)
}
func (a ScheduleSQL) List(ctx context.Context) ([]*domain.ScheduleRecord, error) {
	return a.ListSchedules(ctx)
}
func (a WaitSQL) Put(ctx context.Context, rec *domain.WaitRecord) error { return a.PutWait(ctx, rec) }
func (a WaitSQL) Due(ctx context.Context, now time.Time, limit int) ([]*domain.WaitRecord, error) {
	return a.DueWaits(ctx, now, limit)
}
func (a WaitSQL) Claim(ctx context.Context, id string) (*domain.WaitRecord, error) {
	return a.ClaimWait(ctx, id)
}
func (a ProviderStateSQL) Get(ctx context.Context, providerID string) (bool, bool, error) {
	return a.GetDisabled(ctx, providerID)
}

// migrateTaskStateJSON rewrites persisted workflow runs that still carry
// the pre-TaskState JobRun key "FailureReason" into the unified "Error"
// key, at any nesting depth. Runs without the old key are left untouched,
// so the migration is idempotent across restarts and new stores. Rows
// whose JSON cannot be parsed are skipped (they would not unmarshal as a
// WorkflowRun anyway); query/update failures abort the open.
func migrateTaskStateJSON(db *sql.DB) error {
	rows, err := db.Query(`SELECT id, json FROM runs`)
	if err != nil {
		return err
	}
	type staleRow struct{ id, body string }
	var stale []staleRow
	for rows.Next() {
		var r staleRow
		if err := rows.Scan(&r.id, &r.body); err != nil {
			rows.Close()
			return err
		}
		if strings.Contains(r.body, `"FailureReason"`) {
			stale = append(stale, r)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range stale {
		fixed, err := renameJSONKey(r.body, "FailureReason", "Error")
		if err != nil {
			continue // unparsable row: leave as-is, it cannot be read either way
		}
		if _, err := db.Exec(`UPDATE runs SET json = ? WHERE id = ?`, fixed, r.id); err != nil {
			return err
		}
	}
	return nil
}

// renameJSONKey renames every occurrence of oldKey to newKey at any
// nesting depth of a JSON document.
func renameJSONKey(body, oldKey, newKey string) (string, error) {
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		return "", err
	}
	renameKeyInValue(v, oldKey, newKey)
	out, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func renameKeyInValue(v any, oldKey, newKey string) {
	switch t := v.(type) {
	case map[string]any:
		if val, ok := t[oldKey]; ok {
			delete(t, oldKey)
			t[newKey] = val
		}
		for _, child := range t {
			renameKeyInValue(child, oldKey, newKey)
		}
	case []any:
		for _, child := range t {
			renameKeyInValue(child, oldKey, newKey)
		}
	}
}
