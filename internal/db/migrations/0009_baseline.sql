-- The whole schema at version 9, in place of the nine files that built it. A new
-- database gets it from here. A database that already reached version 9 through
-- those files has nothing left to create, so every statement must stay safe to run
-- against it. Columns keep the order the old files left them in.
--
-- Timestamps are Unix seconds. STRICT makes SQLite reject a value of the wrong type.
-- SQL stays plain so a Postgres port only changes the id columns and STRICT.

CREATE TABLE IF NOT EXISTS users (
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
    disabled_at   INTEGER,
    -- Whether the server may check this user's bookmarks on its own, in the background.
    -- Off until the user turns it on: the server makes requests to the addresses they saved.
    link_check_enabled INTEGER NOT NULL DEFAULT 0 CHECK (link_check_enabled IN (0, 1)),
    -- The User-Agent the link check sends for this user's bookmarks. Empty means the
    -- built-in one, so nobody's requests change until they choose a value.
    link_check_user_agent TEXT NOT NULL DEFAULT ''
) STRICT;

-- Only a hash of the cookie token is stored, so a leaked database cannot be replayed.
CREATE TABLE IF NOT EXISTS sessions (
    token_hash   TEXT    PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at   INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL,
    -- When the session was last used, so one that sits idle can expire before its
    -- absolute lifetime ends.
    last_used_at INTEGER NOT NULL DEFAULT 0,
    -- What the session list shows: where and with what a session started.
    ip           TEXT    NOT NULL DEFAULT '',
    user_agent   TEXT    NOT NULL DEFAULT '',
    -- When the owner last typed their password in this session. Admin changes ask for
    -- it again once it is old.
    confirmed_at INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE INDEX IF NOT EXISTS sessions_user_id ON sessions (user_id);

CREATE TABLE IF NOT EXISTS bookmarks (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    url        TEXT    NOT NULL CHECK (url <> ''),
    title      TEXT    NOT NULL CHECK (title <> ''),
    note       TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    -- The result of the last link check. All three are NULL until a bookmark is checked,
    -- and again after its URL changes, because the result then describes another address.
    link_status     TEXT CHECK (link_status IN ('ok', 'redirected', 'broken', 'unknown')),
    link_checked_at INTEGER,
    -- A short reason such as "HTTP 404", or the address a redirect ended at.
    link_detail     TEXT,
    -- What the site itself says about a bookmark's page: its Open Graph (or <title> and
    -- description) tags, read when the add or edit form is given the address. They stay
    -- apart from title and note, which the user owns, and stand in for either one the
    -- user leaves blank. NULL when the page said nothing, and replaced when the URL
    -- changes, because they then describe another page.
    site_title       TEXT,
    site_description TEXT
) STRICT;

CREATE INDEX IF NOT EXISTS bookmarks_user_created ON bookmarks (user_id, created_at DESC);

-- Finding a user's bookmark by its address: the linkding-compatible API asks
-- "is this page bookmarked?" every time a browser extension opens a page. Several
-- bookmarks may share an address, so the index is not unique.
CREATE INDEX IF NOT EXISTS bookmarks_user_url ON bookmarks (user_id, url);

CREATE TABLE IF NOT EXISTS bookmark_tags (
    bookmark_id INTEGER NOT NULL REFERENCES bookmarks (id) ON DELETE CASCADE,
    -- The application trims and lowercases tags; SQLite cannot fold non-ASCII case.
    tag         TEXT    NOT NULL CHECK (tag <> ''),
    PRIMARY KEY (bookmark_id, tag)
) STRICT;

-- Long-lived credentials for programs that cannot hold a cookie: a browser extension
-- or a script sends one as `Authorization: Token <token>`. As with sessions, only the
-- SHA-256 of the token is stored, so a leaked database cannot be replayed. last_used_at
-- is NULL until the token is first used.
CREATE TABLE IF NOT EXISTS api_tokens (
    token_hash   TEXT    PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT    NOT NULL CHECK (name <> ''),
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER
) STRICT;

CREATE INDEX IF NOT EXISTS api_tokens_user_id ON api_tokens (user_id);

-- Releases up to 0.5, and the Rust server before them, recorded migrations in this
-- table and refuse to start when it names one they do not have. This row makes them
-- stop instead of running against a schema they do not know. success is TRUE because
-- a failed row makes sqlx tell its user to delete it.
CREATE TABLE IF NOT EXISTS _sqlx_migrations (
    version BIGINT PRIMARY KEY,
    description TEXT NOT NULL,
    installed_on TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    success BOOLEAN NOT NULL,
    checksum BLOB NOT NULL,
    execution_time BIGINT NOT NULL
);

INSERT INTO _sqlx_migrations (version, description, success, checksum, execution_time)
VALUES (99999999999999, 'the schema is versioned by PRAGMA user_version now', TRUE, X'', 0)
ON CONFLICT (version) DO NOTHING;
