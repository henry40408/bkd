-- What the site itself says about a bookmark's page: its Open Graph (or <title> and
-- description) tags, read when the user asks to refresh. They stay apart from title and
-- note, which the user owns. All three are NULL until a refresh succeeds, and again
-- after the URL changes, because they then describe another page.
ALTER TABLE bookmarks ADD COLUMN site_title TEXT;
ALTER TABLE bookmarks ADD COLUMN site_description TEXT;
ALTER TABLE bookmarks ADD COLUMN site_fetched_at INTEGER;
