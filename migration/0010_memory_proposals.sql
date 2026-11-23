-- Memory changes read from a thread's sealed history after a compaction,
-- waiting for the owner. See docs/memory.md.
CREATE TABLE memory_proposals (
    id                TEXT    PRIMARY KEY,
    conversation_id   TEXT    NOT NULL,
    message_id        TEXT    NOT NULL DEFAULT '',
    op                TEXT    NOT NULL,
    scope             TEXT    NOT NULL,
    type              TEXT    NOT NULL DEFAULT '',
    name              TEXT    NOT NULL,
    description       TEXT    NOT NULL DEFAULT '',
    body              TEXT    NOT NULL DEFAULT '',
    reason            TEXT    NOT NULL DEFAULT '',
    evidence          TEXT    NOT NULL,
    -- An update or stale points at its memory; deleting the memory
    -- deletes what was suggested about it.
    target_id         TEXT    REFERENCES memories (id) ON DELETE CASCADE,
    target_updated_at INTEGER NOT NULL DEFAULT 0,
    -- JSON array of the live memory ids an add overlaps.
    similar           TEXT    NOT NULL DEFAULT '[]',
    status            TEXT    NOT NULL,
    accepted_id       TEXT    NOT NULL DEFAULT '',
    created_at        INTEGER NOT NULL,
    decided_at        INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX memory_proposals_status ON memory_proposals (status, created_at);
CREATE INDEX memory_proposals_target ON memory_proposals (target_id);
CREATE INDEX memory_proposals_conversation ON memory_proposals (conversation_id, status);
CREATE INDEX memory_proposals_key ON memory_proposals (scope, name, op);
