-- When the session was last used, so one that sits idle can expire before its
-- absolute lifetime ends. Sessions that exist now start their idle clock at migration time.
ALTER TABLE sessions ADD COLUMN last_used_at INTEGER NOT NULL DEFAULT 0;
UPDATE sessions SET last_used_at = CAST(strftime('%s', 'now') AS INTEGER);
