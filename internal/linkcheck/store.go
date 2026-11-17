package linkcheck

import (
	"context"
	"database/sql"
	"strings"

	"github.com/henry40408/bkd/internal/bookmarks"
	"github.com/henry40408/bkd/internal/db"
)

// MaxCheckIDs is how many bookmarks one request may ask about.
const MaxCheckIDs = 200

// CheckInput is what a client sends. It becomes a ValidCheck only through Validate.
type CheckInput struct {
	IDs []int64 `json:"ids"`
}

// ValidCheck is a request that passed validation: distinct ids, at least one,
// at most MaxCheckIDs.
type ValidCheck struct {
	IDs []int64
}

// Validate drops repeated ids and checks the count.
func (input CheckInput) Validate() (ValidCheck, error) {
	seen := make(map[int64]bool, len(input.IDs))
	ids := make([]int64, 0, len(input.IDs))
	for _, id := range input.IDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return ValidCheck{}, bookmarks.ValidationError("ids must not be empty")
	}
	if len(ids) > MaxCheckIDs {
		return ValidCheck{}, bookmarks.ValidationError("too many ids")
	}
	return ValidCheck{IDs: ids}, nil
}

// Store reads the targets of a check and writes its verdicts.
type Store struct {
	db *sql.DB
}

// NewStore creates a Store.
func NewStore(pool *sql.DB) *Store {
	return &Store{db: pool}
}

// Targets returns the bookmarks to check. found is false when an id is
// missing or belongs to someone else.
func (s *Store) Targets(ctx context.Context, userID int64, request ValidCheck) (targets []Target, found bool, err error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(request.IDs)), ",")
	args := make([]any, 0, len(request.IDs)+1)
	args = append(args, userID)
	for _, id := range request.IDs {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, url FROM bookmarks WHERE user_id = ? AND id IN ("+placeholders+")", args...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var target Target
		if err := rows.Scan(&target.ID, &target.URL); err != nil {
			return nil, false, err
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(targets) != len(request.IDs) {
		return nil, false, nil
	}
	return targets, true, nil
}

// StoreRequest is a batch of verdicts to write for one user.
type StoreRequest struct {
	UserID  int64
	Checked []Checked
	Now     int64
}

// Stored is a verdict that was written.
type Stored struct {
	ID    int64
	Check bookmarks.LinkCheck
}

// Store writes the verdicts and returns what was written. A bookmark whose
// URL was edited during the check is skipped, because the verdict describes
// the old address.
func (s *Store) Store(ctx context.Context, request StoreRequest) ([]Stored, error) {
	stored := make([]Stored, 0, len(request.Checked))
	err := db.WithImmediateTx(ctx, s.db, func(conn *sql.Conn) error {
		stored = stored[:0]
		for _, checked := range request.Checked {
			written, err := writeVerdict(ctx, conn, request, checked)
			if err != nil {
				return err
			}
			if written {
				stored = append(stored, storedFrom(checked, request.Now))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return stored, nil
}

func writeVerdict(ctx context.Context, conn *sql.Conn, request StoreRequest, checked Checked) (bool, error) {
	result, err := conn.ExecContext(ctx,
		`UPDATE bookmarks SET link_status = ?1, link_checked_at = ?2, link_detail = ?3
		 WHERE id = ?4 AND user_id = ?5 AND url = ?6`,
		string(checked.Verdict.Status), request.Now, checked.Verdict.Detail,
		checked.Target.ID, request.UserID, checked.Target.URL)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}

func storedFrom(checked Checked, now int64) Stored {
	return Stored{
		ID: checked.Target.ID,
		Check: bookmarks.LinkCheck{
			Status:    string(checked.Verdict.Status),
			CheckedAt: now,
			Detail:    checked.Verdict.Detail,
		},
	}
}
