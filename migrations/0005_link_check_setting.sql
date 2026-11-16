-- Whether the server may check this user's bookmarks on its own, in the background.
-- Off until the user turns it on: the server makes requests to the addresses they saved.
ALTER TABLE users ADD COLUMN link_check_enabled INTEGER NOT NULL DEFAULT 0
    CHECK (link_check_enabled IN (0, 1));
