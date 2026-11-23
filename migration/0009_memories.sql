-- What threads remember across threads: one fact per row, typed, scoped
-- (global, project:<id>, room:<conversation id>), and dated so trust can
-- decay. See docs/memory.md.
CREATE TABLE memories (
    -- An explicit rowid keeps the full-text index's row references stable
    -- across VACUUM.
    seq             INTEGER PRIMARY KEY,
    id              TEXT    NOT NULL UNIQUE,
    scope           TEXT    NOT NULL,
    type            TEXT    NOT NULL,
    name            TEXT    NOT NULL,
    description     TEXT    NOT NULL,
    body            TEXT    NOT NULL,
    source          TEXT    NOT NULL,
    origin          TEXT    NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    verified_at     INTEGER NOT NULL,
    recalled_at     INTEGER NOT NULL DEFAULT 0,
    recalls         INTEGER NOT NULL DEFAULT 0,
    status          TEXT    NOT NULL,
    reason          TEXT    NOT NULL DEFAULT '',
    archived_at     INTEGER NOT NULL DEFAULT 0,
    -- The vector the embedding model made from the current text; NULL
    -- until made, cleared when the text changes.
    embedding       BLOB,
    embedding_model TEXT    NOT NULL DEFAULT ''
);
-- A name is unique among a scope's live memories; forgotten ones keep
-- theirs without blocking a new memory of the same name.
CREATE UNIQUE INDEX memories_live_name ON memories (scope, name) WHERE status <> 'archived';
CREATE INDEX memories_scope ON memories (scope, status);
CREATE INDEX memories_archived ON memories (archived_at) WHERE status = 'archived';

CREATE VIRTUAL TABLE memories_fts USING fts5(
    name,
    description,
    body,
    content = 'memories',
    content_rowid = 'seq',
    tokenize = 'unicode61 remove_diacritics 2'
);

CREATE TRIGGER memories_fts_ai AFTER INSERT ON memories BEGIN
    INSERT INTO memories_fts (rowid, name, description, body) VALUES (new.seq, new.name, new.description, new.body);
END;

CREATE TRIGGER memories_fts_ad AFTER DELETE ON memories BEGIN
    INSERT INTO memories_fts (memories_fts, rowid, name, description, body) VALUES ('delete', old.seq, old.name, old.description, old.body);
END;

CREATE TRIGGER memories_fts_au AFTER UPDATE OF name, description, body ON memories BEGIN
    INSERT INTO memories_fts (memories_fts, rowid, name, description, body) VALUES ('delete', old.seq, old.name, old.description, old.body);
    INSERT INTO memories_fts (rowid, name, description, body) VALUES (new.seq, new.name, new.description, new.body);
END;
