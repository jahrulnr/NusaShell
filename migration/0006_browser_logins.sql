-- Logins saved in a browser profile, one per profile, origin, and username.
-- Passwords live in the secret store under browser-login/<id>, never here.
CREATE TABLE browser_logins (
    id         TEXT    PRIMARY KEY,
    profile    TEXT    NOT NULL,
    origin     TEXT    NOT NULL,
    username   TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (profile, origin, username)
);
