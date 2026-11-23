-- What the owner left in each thread's view — the unsent draft and the
-- side pane — so a reload or another device picks it up where it was.
-- The document is the WebUI's own; the server only bounds its size. key is
-- a conversation id, or "new" for the composer of a thread not started.
CREATE TABLE thread_views (
    key        TEXT    PRIMARY KEY,
    document   TEXT    NOT NULL,
    updated_at INTEGER NOT NULL
);
