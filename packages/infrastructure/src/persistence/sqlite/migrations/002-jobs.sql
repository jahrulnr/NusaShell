CREATE TABLE IF NOT EXISTS jobs (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  schedule_json TEXT NOT NULL,
  mode_json TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  repeat_times INTEGER,           -- NULL = repeat forever
  repeat_completed INTEGER NOT NULL DEFAULT 0,
  next_run_at TEXT,
  last_run_at TEXT,
  last_status TEXT,               -- 'ok' | 'error' | NULL
  last_error TEXT,
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_jobs_due ON jobs (enabled, next_run_at);

CREATE TABLE IF NOT EXISTS job_claims (
  job_id TEXT NOT NULL,
  claim_id TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  PRIMARY KEY (job_id, claim_id)
);

CREATE TABLE IF NOT EXISTS job_outputs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  job_id TEXT NOT NULL,
  run_at TEXT NOT NULL,
  status TEXT NOT NULL,
  summary TEXT NOT NULL,
  path TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_job_outputs_job ON job_outputs (job_id, id DESC);
