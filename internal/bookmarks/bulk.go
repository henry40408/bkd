package bookmarks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/henry40408/bkd/internal/db"
)

// MaxBulkIDs is the most bookmarks one bulk action may touch.
const MaxBulkIDs = 200

// BulkAction names what a bulk request does.
type BulkAction string

const (
	BulkDelete     BulkAction = "delete"
	BulkAddTags    BulkAction = "add_tags"
	BulkRemoveTags BulkAction = "remove_tags"
)

var (
	// ErrBulkNotFound means an id is missing or belongs to someone else. Nothing changed.
	ErrBulkNotFound = errors.New("a bulk id is missing or not owned by the user")
	// ErrBulkTooManyTags means adding the tags would give a bookmark more tags
	// than it may have. Nothing changed.
	ErrBulkTooManyTags = errors.New("a bookmark would get too many tags")
)

// BulkInput is what a client sends. It becomes a ValidBulk only through Validate.
type BulkInput struct {
	IDs    []int64
	Action BulkAction
	Tags   []string
}

// UnmarshalJSON reads the same shape as serde's internally tagged enum: `ids`
// is required, `action` picks a variant, and the add and remove variants need
// `tags`. Every error it returns is a well-formed document that does not fit
// the shape (HTTP 422); syntax errors never reach it.
func (b *BulkInput) UnmarshalJSON(data []byte) error {
	var raw struct {
		IDs    *[]int64 `json:"ids"`
		Action *string  `json:"action"`
		// Left raw: the delete variant ignores `tags`, whatever it holds.
		Tags json.RawMessage `json:"tags"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.IDs == nil {
		return errors.New("missing field `ids`")
	}
	if raw.Action == nil {
		return errors.New("missing field `action`")
	}
	action := BulkAction(*raw.Action)
	switch action {
	case BulkDelete:
	case BulkAddTags, BulkRemoveTags:
		if err := json.Unmarshal(raw.Tags, &b.Tags); err != nil || b.Tags == nil {
			return errors.New("missing or invalid field `tags`")
		}
	default:
		return fmt.Errorf("unknown variant `%s`", *raw.Action)
	}
	b.IDs = *raw.IDs
	b.Action = action
	return nil
}

// ValidBulk is a request that passed every rule.
type ValidBulk struct {
	IDs    []int64
	Action BulkAction
	Tags   []string
}

// Validate drops repeated ids and checks the limits.
func (b BulkInput) Validate() (ValidBulk, error) {
	ids := uniqueIDs(b.IDs)
	if len(ids) == 0 {
		return ValidBulk{}, ValidationError("ids must not be empty")
	}
	if len(ids) > MaxBulkIDs {
		return ValidBulk{}, ValidationError("too many ids")
	}
	valid := ValidBulk{IDs: ids, Action: b.Action}
	if b.Action == BulkDelete {
		return valid, nil
	}
	tags, err := NormalizeTags(b.Tags)
	if err != nil {
		return ValidBulk{}, err
	}
	if len(tags) == 0 {
		return ValidBulk{}, ValidationError("tags must not be empty")
	}
	valid.Tags = tags
	return valid, nil
}

func uniqueIDs(ids []int64) []int64 {
	seen := make(map[int64]bool, len(ids))
	unique := make([]int64, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	return unique
}

// ApplyBulk runs one action on several of the user's bookmarks in one
// transaction: either every bookmark changes or none does. changed counts the
// bookmarks the action changed; one that already had the tag (or never had it)
// is not counted. It returns ErrBulkNotFound or ErrBulkTooManyTags without
// changing anything.
func (s *Store) ApplyBulk(ctx context.Context, userID int64, bulk ValidBulk, now int64) (changed int, err error) {
	// IMMEDIATE takes the write lock up front. A deferred transaction that reads
	// first and writes later fails with SQLITE_BUSY_SNAPSHOT when another
	// connection commits in between.
	err = db.WithImmediateTx(ctx, s.db, func(conn *sql.Conn) error {
		owned, err := countOwned(ctx, conn, userID, bulk.IDs)
		if err != nil {
			return err
		}
		if owned != len(bulk.IDs) {
			return ErrBulkNotFound
		}
		changed, err = applyAction(ctx, conn, bulk, now)
		if err != nil {
			return err
		}
		tooMany, err := hasTooManyTags(ctx, conn, bulk.IDs)
		if err != nil {
			return err
		}
		if tooMany {
			return ErrBulkTooManyTags
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return changed, nil
}

func applyAction(ctx context.Context, conn *sql.Conn, bulk ValidBulk, now int64) (int, error) {
	switch bulk.Action {
	case BulkDelete:
		return deleteMany(ctx, conn, bulk.IDs)
	case BulkAddTags:
		return changeTags(ctx, conn, bulk, now, addTagQuery)
	default:
		return changeTags(ctx, conn, bulk, now, removeTagQuery)
	}
}

// idList is `(?, ?, ...)` with one placeholder per id, and the ids as arguments.
func idList(ids []int64) (placeholders string, args []any) {
	args = make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return "(" + strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ") + ")", args
}

func countOwned(ctx context.Context, conn *sql.Conn, userID int64, ids []int64) (int, error) {
	placeholders, args := idList(ids)
	var count int
	err := conn.QueryRowContext(ctx,
		"SELECT count(*) FROM bookmarks WHERE user_id = ? AND id IN "+placeholders,
		append([]any{userID}, args...)...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count owned bookmarks: %w", err)
	}
	return count, nil
}

func deleteMany(ctx context.Context, conn *sql.Conn, ids []int64) (int, error) {
	placeholders, args := idList(ids)
	result, err := conn.ExecContext(ctx, "DELETE FROM bookmarks WHERE id IN "+placeholders, args...)
	if err != nil {
		return 0, fmt.Errorf("delete bookmarks: %w", err)
	}
	deleted, err := result.RowsAffected()
	return int(deleted), err
}

// tagQuery builds the statement for one tag; its RETURNING clause lists the
// bookmarks that really changed.
type tagQuery func(placeholders string) string

func addTagQuery(placeholders string) string {
	return "INSERT OR IGNORE INTO bookmark_tags (bookmark_id, tag) SELECT id, ? FROM bookmarks WHERE id IN " +
		placeholders + " RETURNING bookmark_id"
}

func removeTagQuery(placeholders string) string {
	return "DELETE FROM bookmark_tags WHERE tag = ? AND bookmark_id IN " + placeholders + " RETURNING bookmark_id"
}

func changeTags(ctx context.Context, conn *sql.Conn, bulk ValidBulk, now int64, build tagQuery) (int, error) {
	placeholders, idArgs := idList(bulk.IDs)
	changed := map[int64]bool{}
	for _, tag := range bulk.Tags {
		ids, err := returnedIDs(ctx, conn, build(placeholders), append([]any{tag}, idArgs...))
		if err != nil {
			return 0, err
		}
		for _, id := range ids {
			changed[id] = true
		}
	}
	if err := touch(ctx, conn, changed, now); err != nil {
		return 0, err
	}
	return len(changed), nil
}

func returnedIDs(ctx context.Context, conn *sql.Conn, query string, args []any) ([]int64, error) {
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("change tags: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan changed id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func touch(ctx context.Context, conn *sql.Conn, changed map[int64]bool, now int64) error {
	if len(changed) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(changed))
	for id := range changed {
		ids = append(ids, id)
	}
	placeholders, args := idList(ids)
	_, err := conn.ExecContext(ctx, "UPDATE bookmarks SET updated_at = ? WHERE id IN "+placeholders, append([]any{now}, args...)...)
	if err != nil {
		return fmt.Errorf("touch bookmarks: %w", err)
	}
	return nil
}

func hasTooManyTags(ctx context.Context, conn *sql.Conn, ids []int64) (bool, error) {
	placeholders, args := idList(ids)
	var exists bool
	err := conn.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT 1 FROM bookmark_tags WHERE bookmark_id IN "+placeholders+
			" GROUP BY bookmark_id HAVING count(*) > ?)",
		append(args, int64(MaxTagsPerBookmark))...).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check tag counts: %w", err)
	}
	return exists, nil
}
