-- Long-lived credentials for programs that cannot hold a cookie: a browser extension
-- or a script sends one as `Authorization: Token <token>`. As with sessions, only the
-- SHA-256 of the token is stored, so a leaked database cannot be replayed. last_used_at
-- is NULL until the token is first used.
CREATE TABLE api_tokens (
    token_hash   TEXT    PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT    NOT NULL CHECK (name <> ''),
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER
) STRICT;

CREATE INDEX api_tokens_user_id ON api_tokens (user_id);
