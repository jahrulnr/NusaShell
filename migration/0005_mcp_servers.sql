-- MCP servers the owner adds; every agent session and the model runtime
-- get the enabled ones. args is a JSON array. Environment and header
-- values can hold credentials, so they live in the secret store under
-- mcp/<id>, never here.
CREATE TABLE mcp_servers (
    id         TEXT    PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE,
    transport  TEXT    NOT NULL,
    command    TEXT    NOT NULL DEFAULT '',
    args       TEXT    NOT NULL DEFAULT '[]',
    url        TEXT    NOT NULL DEFAULT '',
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
