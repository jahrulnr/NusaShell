CREATE TABLE IF NOT EXISTS plugins (
  id TEXT PRIMARY KEY,
  version TEXT NOT NULL,
  manifest_json TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  install_path TEXT NOT NULL,
  installed_at TEXT NOT NULL
);
