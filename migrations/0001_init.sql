-- Timestamps are Unix seconds. STRICT makes SQLite reject a value of the wrong type.
-- SQL stays plain so a Postgres port only changes the id columns and STRICT.

CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    -- ASCII only and lowercase: SQLite folds case for ASCII alone, so this keeps the
    -- UNIQUE constraint case-insensitive on every database.
    username      TEXT    NOT NULL UNIQUE CHECK (
        length(username) BETWEEN 3 AND 32
        AND username GLOB '[a-z0-9]*[a-z0-9]'
        AND username NOT GLOB '*[^a-z0-9._-]*'
    ),
    password_hash TEXT    NOT NULL,
    is_admin      INTEGER NOT NULL DEFAULT 0 CHECK (is_admin IN (0, 1)),
    created_at    INTEGER NOT NULL,
    -- NULL means active.
    disabled_at   INTEGER
) STRICT;

-- Only a hash of the cookie token is stored, so a leaked database cannot be replayed.
CREATE TABLE sessions (
    token_hash TEXT    PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
) STRICT;

CREATE INDEX sessions_user_id ON sessions (user_id);

CREATE TABLE bookmarks (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    url        TEXT    NOT NULL CHECK (url <> ''),
    title      TEXT    NOT NULL CHECK (title <> ''),
    note       TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE INDEX bookmarks_user_created ON bookmarks (user_id, created_at DESC);

CREATE TABLE bookmark_tags (
    bookmark_id INTEGER NOT NULL REFERENCES bookmarks (id) ON DELETE CASCADE,
    -- The application trims and lowercases tags; SQLite cannot fold non-ASCII case.
    tag         TEXT    NOT NULL CHECK (tag <> ''),
    PRIMARY KEY (bookmark_id, tag)
) STRICT;
