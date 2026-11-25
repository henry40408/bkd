package bookmarks

import (
	"context"
	"fmt"
	"strings"
)

// Since narrows another filter to bookmarks added or modified after a time, in
// Unix seconds. A nil bound is no bound, and a nil Inner lets everything else
// through.
type Since struct {
	Inner         Filter
	AddedAfter    *int64
	ModifiedAfter *int64
}

// SQL implements Filter.
func (since Since) SQL() (conditions string, args []any) {
	conditions, args = filterSQL(since.Inner)
	if since.AddedAfter != nil {
		conditions += " AND bookmarks.created_at > ?"
		args = append(args, *since.AddedAfter)
	}
	if since.ModifiedAfter != nil {
		conditions += " AND bookmarks.updated_at > ?"
		args = append(args, *since.ModifiedAfter)
	}
	return conditions, args
}

// FindByURL returns the user's bookmark with exactly this address (after
// trimming, as a stored address is). When several share it, the oldest wins, so
// the answer does not change while nothing is added.
func (s *Store) FindByURL(ctx context.Context, userID int64, url string) (bookmark Bookmark, found bool, err error) {
	rows, err := s.query(ctx,
		selectBookmark+" WHERE bookmarks.user_id = ?1 AND bookmarks.url = ?2 ORDER BY bookmarks.id LIMIT 1",
		userID, strings.TrimSpace(url))
	if err != nil || len(rows) == 0 {
		return Bookmark{}, false, err
	}
	return rows[0], true, nil
}

// TagEntry is a tag as a program that expects tags to be records sees it. A tag
// here is only a word on bookmarks, so ID and AddedAt are made up from them: ID is
// the smallest row number the tag has among the user's bookmarks, and AddedAt is
// when the oldest bookmark that carries it was saved. ID is stable while that
// bookmark keeps the tag and unique among one user's tags.
type TagEntry struct {
	ID      int64
	Name    string
	AddedAt int64
}

// TagEntries lists the user's tags by name, one page of them, with how many tags
// there are in all.
func (s *Store) TagEntries(ctx context.Context, userID int64, page Page) (entries []TagEntry, total int64, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT count(DISTINCT bookmark_tags.tag)
         FROM bookmark_tags JOIN bookmarks ON bookmarks.id = bookmark_tags.bookmark_id
         WHERE bookmarks.user_id = ?1`, userID).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count tags: %w", err)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT bookmark_tags.tag, min(bookmark_tags.rowid), min(bookmarks.created_at)
         FROM bookmark_tags JOIN bookmarks ON bookmarks.id = bookmark_tags.bookmark_id
         WHERE bookmarks.user_id = ?1
         GROUP BY bookmark_tags.tag ORDER BY bookmark_tags.tag LIMIT ?2 OFFSET ?3`,
		userID, int64(page.Limit), int64(page.Offset))
	if err != nil {
		return nil, 0, fmt.Errorf("list tags: %w", err)
	}
	defer func() { _ = rows.Close() }()
	entries = []TagEntry{}
	for rows.Next() {
		var entry TagEntry
		if err := rows.Scan(&entry.Name, &entry.ID, &entry.AddedAt); err != nil {
			return nil, 0, fmt.Errorf("scan tag: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("read tags: %w", err)
	}
	return entries, total, nil
}
