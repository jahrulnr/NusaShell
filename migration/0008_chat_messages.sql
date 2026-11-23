-- Messages the channels' accounts saw or sent, kept for search. Keeping a
-- message neither routes it to a room nor applies the allowlist.
CREATE TABLE chat_messages (
    -- An explicit rowid keeps the full-text index's row references stable
    -- across VACUUM.
    id          INTEGER PRIMARY KEY,
    channel     TEXT    NOT NULL,
    chat_id     TEXT    NOT NULL,
    message_id  TEXT    NOT NULL,
    sender_id   TEXT    NOT NULL DEFAULT '',
    sender_name TEXT    NOT NULL DEFAULT '',
    chat_name   TEXT    NOT NULL DEFAULT '',
    at_ms       INTEGER NOT NULL,
    from_me     INTEGER NOT NULL DEFAULT 0,
    is_group    INTEGER NOT NULL DEFAULT 0,
    text        TEXT    NOT NULL DEFAULT '',
    raw         TEXT    NOT NULL DEFAULT '',
    is_live     INTEGER NOT NULL DEFAULT 0,
    explicit    INTEGER NOT NULL DEFAULT 0,
    receipt     TEXT    NOT NULL DEFAULT '',
    UNIQUE (channel, chat_id, message_id)
);
CREATE INDEX chat_messages_time ON chat_messages (channel, at_ms DESC);
CREATE INDEX chat_messages_chat_time ON chat_messages (channel, chat_id, at_ms);

-- Full-text index over the text, kept in step by triggers.
CREATE VIRTUAL TABLE chat_messages_fts USING fts5(
    text,
    content = 'chat_messages',
    content_rowid = 'id',
    tokenize = 'unicode61'
);

CREATE TRIGGER chat_messages_fts_ai AFTER INSERT ON chat_messages BEGIN
    INSERT INTO chat_messages_fts (rowid, text) VALUES (new.id, new.text);
END;

CREATE TRIGGER chat_messages_fts_ad AFTER DELETE ON chat_messages BEGIN
    INSERT INTO chat_messages_fts (chat_messages_fts, rowid, text) VALUES ('delete', old.id, old.text);
END;

CREATE TRIGGER chat_messages_fts_au AFTER UPDATE OF text ON chat_messages BEGIN
    INSERT INTO chat_messages_fts (chat_messages_fts, rowid, text) VALUES ('delete', old.id, old.text);
    INSERT INTO chat_messages_fts (rowid, text) VALUES (new.id, new.text);
END;
