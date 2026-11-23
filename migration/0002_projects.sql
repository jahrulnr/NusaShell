-- Projects group conversations that work on the same folders. folders is a
-- JSON array of absolute paths; index 0 is the primary folder. Membership
-- lives on each conversation's own record, so removing a project deletes
-- no conversation.
CREATE TABLE projects (
    id         TEXT    PRIMARY KEY,
    name       TEXT    NOT NULL,
    folders    TEXT    NOT NULL,
    pinned     INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
