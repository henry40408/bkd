-- The User-Agent the link check sends for this user's bookmarks. Empty means the
-- built-in one, so nobody's requests change until they choose a value.
ALTER TABLE users ADD COLUMN link_check_user_agent TEXT NOT NULL DEFAULT '';
