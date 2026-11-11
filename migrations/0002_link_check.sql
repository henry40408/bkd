-- The result of the last link check. All three are NULL until a bookmark is checked,
-- and again after its URL changes, because the result then describes another address.
ALTER TABLE bookmarks ADD COLUMN link_status TEXT
    CHECK (link_status IN ('ok', 'redirected', 'broken', 'unknown'));
ALTER TABLE bookmarks ADD COLUMN link_checked_at INTEGER;
-- A short reason such as "HTTP 404", or the address a redirect ended at.
ALTER TABLE bookmarks ADD COLUMN link_detail TEXT;
