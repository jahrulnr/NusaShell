-- Automations: a prompt sent to a runtime on a schedule. Times are Unix
-- nanoseconds; 0 means unset. version guards every write — claims, kept
-- threads, edits — while updated_at moves only on owner edits, so the
-- stale-form check compares it exactly.
CREATE TABLE automations (
    id              TEXT    PRIMARY KEY,
    name            TEXT    NOT NULL,
    prompt          TEXT    NOT NULL,
    agent_id        TEXT    NOT NULL DEFAULT '',
    provider_id     TEXT    NOT NULL DEFAULT '',
    model_id        TEXT    NOT NULL DEFAULT '',
    schedule_kind   TEXT    NOT NULL,
    cron            TEXT    NOT NULL DEFAULT '',
    run_at          INTEGER NOT NULL DEFAULT 0,
    timezone        TEXT    NOT NULL,
    thread          TEXT    NOT NULL,
    conversation_id TEXT    NOT NULL DEFAULT '',
    project_id      TEXT    NOT NULL DEFAULT '',
    notify_channel  TEXT    NOT NULL DEFAULT '',
    notify_chat     TEXT    NOT NULL DEFAULT '',
    notify_thread   TEXT    NOT NULL DEFAULT '',
    expires_at      INTEGER NOT NULL DEFAULT 0,
    enabled         INTEGER NOT NULL,
    next_run_at     INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    version         INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX automations_due ON automations (enabled, next_run_at);

-- One row per firing.
CREATE TABLE automation_runs (
    id              TEXT    PRIMARY KEY,
    automation_id   TEXT    NOT NULL REFERENCES automations (id) ON DELETE CASCADE,
    scheduled_at    INTEGER NOT NULL,
    started_at      INTEGER NOT NULL,
    ended_at        INTEGER NOT NULL DEFAULT 0,
    conversation_id TEXT    NOT NULL DEFAULT '',
    status          TEXT    NOT NULL,
    note            TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX automation_runs_by_automation ON automation_runs (automation_id, started_at);
CREATE INDEX automation_runs_status ON automation_runs (status);
