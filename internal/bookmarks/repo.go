package bookmarks

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/henry40408/bkd/internal/db"
)

const (
	// DefaultPageSize is the page size when the client names none.
	DefaultPageSize uint32 = 50
	// MaxPageSize caps the page size, so a client cannot ask for everything.
	MaxPageSize uint32 = 200
)

// tagSeparator joins tags inside SQL. Tags are rejected if they contain control
// characters, so this byte can never appear in one.
const tagSeparator = "\x1f"

// Filter narrows a bookmark query. SQL returns ` AND ...` conditions for a
// query that selects from bookmarks, with their bind arguments. A nil Filter
// lets everything through.
type Filter interface {
	SQL() (conditions string, args []any)
}

// Page is a window onto a list.
type Page struct {
	Limit  uint32
	Offset uint32
}

// NewPage applies the defaults and caps the size.
func NewPage(limit, offset *uint32) Page {
	page := Page{Limit: DefaultPageSize}
	if limit != nil {
		page.Limit = min(max(*limit, 1), MaxPageSize)
	}
	if offset != nil {
		page.Offset = *offset
	}
	return page
}

// ImportEntry is a bookmark from an import file, with the time it was first saved.
type ImportEntry struct {
	Bookmark  Valid
	CreatedAt int64
}

// UpdateRequest names the bookmark to replace and what replaces it.
type UpdateRequest struct {
	UserID   int64
	ID       int64
	Bookmark Valid
	Now      int64
}

// Store reads and writes bookmarks.
type Store struct {
	db *sql.DB
}

func NewStore(pool *sql.DB) *Store { return &Store{db: pool} }

// The tags come from a sub-select so one query returns everything the list needs.
const selectBookmark = `SELECT bookmarks.id, bookmarks.url, bookmarks.title, bookmarks.note,
    bookmarks.created_at, bookmarks.updated_at,
    bookmarks.link_status, bookmarks.link_checked_at, bookmarks.link_detail,
    bookmarks.site_title, bookmarks.site_description,
    (SELECT group_concat(tag, char(31))
     FROM (SELECT tag FROM bookmark_tags WHERE bookmark_id = bookmarks.id ORDER BY tag)) AS tags
    FROM bookmarks`

// An empty page title or description is stored as NULL: the page said nothing.
const insertBookmark = `INSERT INTO bookmarks (user_id, url, title, note, site_title, site_description, created_at, updated_at)
    VALUES (?1, ?2, ?3, ?4, NULLIF(?5, ''), NULLIF(?6, ''), ?7, ?7) RETURNING id`

func insertArgs(userID int64, bookmark Valid, createdAt int64) []any {
	return []any{userID, bookmark.URL, bookmark.Title, bookmark.Note,
		bookmark.Site.Title, bookmark.Site.Description, createdAt}
}

// execer is what insertTags needs from a transaction or a connection.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Create stores a bookmark and returns it as read back.
func (s *Store) Create(ctx context.Context, userID int64, bookmark Valid, now int64) (Bookmark, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Bookmark{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var id int64
	err = tx.QueryRowContext(ctx, insertBookmark, insertArgs(userID, bookmark, now)...).Scan(&id)
	if err != nil {
		return Bookmark{}, fmt.Errorf("insert bookmark: %w", err)
	}
	if err := insertTags(ctx, tx, id, bookmark.Tags); err != nil {
		return Bookmark{}, err
	}
	if err := tx.Commit(); err != nil {
		return Bookmark{}, fmt.Errorf("commit bookmark: %w", err)
	}
	// The owner could delete it in this gap, so a missing row is an error, not a bug.
	created, found, err := s.Get(ctx, userID, id)
	if err != nil {
		return Bookmark{}, err
	}
	if !found {
		return Bookmark{}, fmt.Errorf("bookmark %d was deleted right after it was created", id)
	}
	return created, nil
}

// Update replaces every field and the whole tag set. found is false when the
// bookmark does not exist or belongs to someone else.
func (s *Store) Update(ctx context.Context, req UpdateRequest) (updated Bookmark, found bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Bookmark{}, false, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	changed, err := updateRow(ctx, tx, req)
	if err != nil || !changed {
		return Bookmark{}, false, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM bookmark_tags WHERE bookmark_id = ?1", req.ID); err != nil {
		return Bookmark{}, false, fmt.Errorf("delete tags: %w", err)
	}
	if err := insertTags(ctx, tx, req.ID, req.Bookmark.Tags); err != nil {
		return Bookmark{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Bookmark{}, false, fmt.Errorf("commit bookmark: %w", err)
	}
	return s.Get(ctx, req.UserID, req.ID)
}

func updateRow(ctx context.Context, tx *sql.Tx, req UpdateRequest) (bool, error) {
	// A check and a page's details describe one address, so a check is dropped
	// when the URL changes and the details are replaced by those of the new
	// page. With the same URL the details are replaced only when the form sent
	// some (it read the page again); both columns go together, because a title
	// and a description from two readings do not describe one page. In SET,
	// `url` is still the old value.
	siteSent := req.Bookmark.Site != (Site{})
	result, err := tx.ExecContext(ctx, `UPDATE bookmarks SET
             link_status      = CASE WHEN url = ?1 THEN link_status      END,
             link_checked_at  = CASE WHEN url = ?1 THEN link_checked_at  END,
             link_detail      = CASE WHEN url = ?1 THEN link_detail      END,
             site_title       = CASE WHEN url = ?1 AND NOT ?9 THEN site_title       ELSE NULLIF(?7, '') END,
             site_description = CASE WHEN url = ?1 AND NOT ?9 THEN site_description ELSE NULLIF(?8, '') END,
             url = ?1, title = ?2, note = ?3, updated_at = ?4
         WHERE id = ?5 AND user_id = ?6`,
		req.Bookmark.URL, req.Bookmark.Title, req.Bookmark.Note, req.Now, req.ID, req.UserID,
		req.Bookmark.Site.Title, req.Bookmark.Site.Description, siteSent)
	if err != nil {
		return false, fmt.Errorf("update bookmark: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count updated bookmarks: %w", err)
	}
	return changed > 0, nil
}

func insertTags(ctx context.Context, conn execer, bookmarkID int64, tags []string) error {
	for _, tag := range tags {
		_, err := conn.ExecContext(ctx, "INSERT INTO bookmark_tags (bookmark_id, tag) VALUES (?1, ?2)", bookmarkID, tag)
		if err != nil {
			return fmt.Errorf("insert tag: %w", err)
		}
	}
	return nil
}

// Delete reports false when the bookmark does not exist or belongs to someone else.
func (s *Store) Delete(ctx context.Context, userID, id int64) (bool, error) {
	result, err := s.db.ExecContext(ctx, "DELETE FROM bookmarks WHERE id = ?1 AND user_id = ?2", id, userID)
	if err != nil {
		return false, fmt.Errorf("delete bookmark: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count deleted bookmarks: %w", err)
	}
	return deleted > 0, nil
}

// Get returns one of the user's bookmarks.
func (s *Store) Get(ctx context.Context, userID, id int64) (bookmark Bookmark, found bool, err error) {
	rows, err := s.query(ctx, selectBookmark+" WHERE bookmarks.user_id = ?1 AND bookmarks.id = ?2", userID, id)
	if err != nil || len(rows) == 0 {
		return Bookmark{}, false, err
	}
	return rows[0], true, nil
}

// List returns the user's bookmarks, newest first, narrowed by filter.
func (s *Store) List(ctx context.Context, userID int64, filter Filter, page Page) ([]Bookmark, error) {
	conditions, args := filterSQL(filter)
	query := selectBookmark + " WHERE bookmarks.user_id = ?" + conditions +
		" ORDER BY bookmarks.created_at DESC, bookmarks.id DESC LIMIT ? OFFSET ?"
	args = append([]any{userID}, args...)
	return s.query(ctx, query, append(args, int64(page.Limit), int64(page.Offset))...)
}

// Count is how many of the user's bookmarks filter lets through.
func (s *Store) Count(ctx context.Context, userID int64, filter Filter) (int64, error) {
	conditions, args := filterSQL(filter)
	query := "SELECT COUNT(*) FROM bookmarks WHERE bookmarks.user_id = ?" + conditions
	var count int64
	if err := s.db.QueryRowContext(ctx, query, append([]any{userID}, args...)...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count bookmarks: %w", err)
	}
	return count, nil
}

// Random returns one bookmark chosen at random from those filter lets through.
func (s *Store) Random(ctx context.Context, userID int64, filter Filter) (bookmark Bookmark, found bool, err error) {
	conditions, args := filterSQL(filter)
	query := selectBookmark + " WHERE bookmarks.user_id = ?" + conditions + " ORDER BY RANDOM() LIMIT 1"
	rows, err := s.query(ctx, query, append([]any{userID}, args...)...)
	if err != nil || len(rows) == 0 {
		return Bookmark{}, false, err
	}
	return rows[0], true, nil
}

// All returns every bookmark the user has, oldest first, so a file read back in
// keeps the order.
func (s *Store) All(ctx context.Context, userID int64) ([]Bookmark, error) {
	return s.query(ctx, selectBookmark+" WHERE bookmarks.user_id = ?1 ORDER BY bookmarks.created_at, bookmarks.id", userID)
}

func filterSQL(filter Filter) (string, []any) {
	if filter == nil {
		return "", nil
	}
	return filter.SQL()
}

func (s *Store) query(ctx context.Context, query string, args ...any) ([]Bookmark, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query bookmarks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	bookmarks := []Bookmark{}
	for rows.Next() {
		bookmark, err := scanBookmark(rows)
		if err != nil {
			return nil, err
		}
		bookmarks = append(bookmarks, bookmark)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read bookmarks: %w", err)
	}
	return bookmarks, nil
}

func scanBookmark(rows *sql.Rows) (Bookmark, error) {
	var (
		bookmark  Bookmark
		status    sql.NullString
		checkedAt sql.NullInt64
		detail    sql.NullString
		site      siteColumns
		tags      sql.NullString
	)
	err := rows.Scan(&bookmark.ID, &bookmark.URL, &bookmark.Title, &bookmark.Note,
		&bookmark.CreatedAt, &bookmark.UpdatedAt, &status, &checkedAt, &detail,
		&site.title, &site.description, &tags)
	if err != nil {
		return Bookmark{}, fmt.Errorf("scan bookmark: %w", err)
	}
	bookmark.Tags = []string{}
	if tags.Valid {
		bookmark.Tags = strings.Split(tags.String, tagSeparator)
	}
	if status.Valid {
		bookmark.Link = &LinkCheck{Status: status.String, CheckedAt: checkedAt.Int64, Detail: detail.String}
	}
	bookmark.Site = site.value()
	return bookmark, nil
}

// siteColumns is the site_* columns, which are NULL when the page said nothing.
type siteColumns struct {
	title       sql.NullString
	description sql.NullString
}

func (c siteColumns) value() *Site {
	if !c.title.Valid && !c.description.Valid {
		return nil
	}
	return &Site{Title: c.title.String, Description: c.description.String}
}

// TagCounts lists every tag the user has used, with how many bookmarks carry it.
func (s *Store) TagCounts(ctx context.Context, userID int64) ([]TagCount, error) {
	return s.TagCountsWithStatus(ctx, userID, "")
}

// TagCountsWithStatus counts only the bookmarks with this link check result
// (unchecked for none); a blank status counts them all. A tag none of those
// bookmarks carry is left out.
func (s *Store) TagCountsWithStatus(ctx context.Context, userID int64, status string) ([]TagCount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT bookmark_tags.tag AS tag, count(*) AS count
         FROM bookmark_tags JOIN bookmarks ON bookmarks.id = bookmark_tags.bookmark_id
         WHERE bookmarks.user_id = ?1
           AND (?2 = '' OR COALESCE(bookmarks.link_status, 'unchecked') = ?2)
         GROUP BY bookmark_tags.tag ORDER BY bookmark_tags.tag`, userID, normalizeStatus(status))
	if err != nil {
		return nil, fmt.Errorf("count tags: %w", err)
	}
	defer func() { _ = rows.Close() }()
	counts := []TagCount{}
	for rows.Next() {
		var count TagCount
		if err := rows.Scan(&count.Tag, &count.Count); err != nil {
			return nil, fmt.Errorf("scan tag count: %w", err)
		}
		counts = append(counts, count)
	}
	return counts, rows.Err()
}

// normalizeStatus spells a status the way the search filter does.
func normalizeStatus(status string) string {
	return strings.ToLower(strings.TrimSpace(status))
}

// LinkCounts says how many of the user's bookmarks have each link check
// result. A result with no bookmarks is left out; unchecked counts the
// bookmarks never checked.
func (s *Store) LinkCounts(ctx context.Context, userID int64) ([]LinkCount, error) {
	return s.LinkCountsWithTag(ctx, userID, "")
}

// LinkCountsWithTag counts only the bookmarks carrying this exact tag; a blank
// tag counts them all.
func (s *Store) LinkCountsWithTag(ctx context.Context, userID int64, tag string) ([]LinkCount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT COALESCE(link_status, 'unchecked') AS status, count(*) AS count
         FROM bookmarks WHERE user_id = ?1
           AND (?2 = '' OR EXISTS (SELECT 1 FROM bookmark_tags
                WHERE bookmark_tags.bookmark_id = bookmarks.id AND bookmark_tags.tag = ?2))
         GROUP BY status ORDER BY status`, userID, tag)
	if err != nil {
		return nil, fmt.Errorf("count link results: %w", err)
	}
	defer func() { _ = rows.Close() }()
	counts := []LinkCount{}
	for rows.Next() {
		var count LinkCount
		if err := rows.Scan(&count.Status, &count.Count); err != nil {
			return nil, fmt.Errorf("scan link count: %w", err)
		}
		counts = append(counts, count)
	}
	return counts, rows.Err()
}

// Import adds the entries in one transaction, so a failure leaves nothing
// behind. A URL the user already has, or that appears twice in the file, is
// skipped. It returns how many were added.
func (s *Store) Import(ctx context.Context, userID int64, entries []ImportEntry) (added int, err error) {
	// Immediate, so the read below cannot go stale and fail to upgrade to a writer.
	err = db.WithImmediateTx(ctx, s.db, func(conn *sql.Conn) error {
		seen, err := existingURLs(ctx, conn, userID)
		if err != nil {
			return err
		}
		added = 0
		for _, entry := range entries {
			if seen[entry.Bookmark.URL] {
				continue
			}
			seen[entry.Bookmark.URL] = true
			if err := importEntry(ctx, conn, userID, entry); err != nil {
				return err
			}
			added++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return added, nil
}

func existingURLs(ctx context.Context, conn *sql.Conn, userID int64) (map[string]bool, error) {
	rows, err := conn.QueryContext(ctx, "SELECT url FROM bookmarks WHERE user_id = ?1", userID)
	if err != nil {
		return nil, fmt.Errorf("read existing urls: %w", err)
	}
	defer func() { _ = rows.Close() }()
	seen := map[string]bool{}
	for rows.Next() {
		var url string
		if err := rows.Scan(&url); err != nil {
			return nil, fmt.Errorf("scan url: %w", err)
		}
		seen[url] = true
	}
	return seen, rows.Err()
}

func importEntry(ctx context.Context, conn *sql.Conn, userID int64, entry ImportEntry) error {
	var id int64
	bookmark := entry.Bookmark
	err := conn.QueryRowContext(ctx, insertBookmark, insertArgs(userID, bookmark, entry.CreatedAt)...).Scan(&id)
	if err != nil {
		return fmt.Errorf("insert imported bookmark: %w", err)
	}
	return insertTags(ctx, conn, id, bookmark.Tags)
}
