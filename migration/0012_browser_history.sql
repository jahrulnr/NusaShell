-- Pages a browser profile visited, one row per profile and url, for the
-- address bar's suggestions.
CREATE TABLE browser_history (
    profile    TEXT    NOT NULL,
    url        TEXT    NOT NULL,
    title      TEXT    NOT NULL,
    visits     INTEGER NOT NULL,
    visited_at INTEGER NOT NULL,
    PRIMARY KEY (profile, url)
);
CREATE INDEX browser_history_recent ON browser_history (profile, visited_at DESC);
