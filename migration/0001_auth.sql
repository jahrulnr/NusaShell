-- The owner account: one row, the password hash only.
CREATE TABLE owner (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    password_hash TEXT    NOT NULL,
    updated_at    INTEGER NOT NULL
);

-- Login sessions. The cookie carries the token; only its SHA-256 is stored.
CREATE TABLE sessions (
    token_hash   TEXT    PRIMARY KEY,
    csrf_token   TEXT    NOT NULL,
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL
);

CREATE INDEX sessions_expires_at ON sessions (expires_at);
