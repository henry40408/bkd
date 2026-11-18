-- What the session list shows: where and with what a session started. Sessions that
-- exist now show blank details.
ALTER TABLE sessions ADD COLUMN ip TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN user_agent TEXT NOT NULL DEFAULT '';
-- When the owner last typed their password in this session. Admin changes ask for it
-- again once it is old. Existing sessions start unconfirmed.
ALTER TABLE sessions ADD COLUMN confirmed_at INTEGER NOT NULL DEFAULT 0;
