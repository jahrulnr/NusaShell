-- Which room answers each external chat. A chat links to one room; a room
-- may be linked from several chats.
CREATE TABLE channel_links (
    channel         TEXT    NOT NULL,
    chat_id         TEXT    NOT NULL,
    thread_id       TEXT    NOT NULL DEFAULT '',
    conversation_id TEXT    NOT NULL,
    display_name    TEXT    NOT NULL,
    is_group        INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    PRIMARY KEY (channel, chat_id, thread_id)
);
CREATE INDEX channel_links_conversation ON channel_links (conversation_id);

-- Channel messages already taken, so a redelivery after a reconnect does
-- not run twice. Pruned by age.
CREATE TABLE channel_inbound (
    channel     TEXT    NOT NULL,
    message_id  TEXT    NOT NULL,
    received_at INTEGER NOT NULL,
    PRIMARY KEY (channel, message_id)
);
CREATE INDEX channel_inbound_received ON channel_inbound (received_at);

-- Each channel's own settings document, opaque to the store. Credentials
-- live in the secret store, never here.
CREATE TABLE channel_settings (
    channel    TEXT    PRIMARY KEY,
    document   TEXT    NOT NULL,
    updated_at INTEGER NOT NULL
);
