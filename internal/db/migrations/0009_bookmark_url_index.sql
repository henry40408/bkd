-- Finding a user's bookmark by its address: the linkding-compatible API asks
-- "is this page bookmarked?" every time a browser extension opens, and a create
-- there updates the bookmark that already has the address. Several bookmarks may
-- share an address, so the index is not unique.
CREATE INDEX bookmarks_user_url ON bookmarks (user_id, url);
