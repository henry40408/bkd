-- What the site itself says about a bookmark's page: its Open Graph (or <title> and
-- description) tags, read when the add or edit form is given the address. They stay
-- apart from title and note, which the user owns, and stand in for either one the user
-- leaves blank. NULL when the page said nothing, and replaced when the URL changes,
-- because they then describe another page.
ALTER TABLE bookmarks ADD COLUMN site_title TEXT;
ALTER TABLE bookmarks ADD COLUMN site_description TEXT;
