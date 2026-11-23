-- A room's "always allow" answers: its agent is not asked again for that
-- kind of action.
CREATE TABLE approval_rules (
    id              TEXT    PRIMARY KEY,
    conversation_id TEXT    NOT NULL,
    agent_id        TEXT    NOT NULL DEFAULT '',
    action          TEXT    NOT NULL,
    created_at      INTEGER NOT NULL,
    UNIQUE (conversation_id, action)
);
