package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/henry40408/bkd/internal/db"
)

// UsersExist reports whether any account exists.
func UsersExist(ctx context.Context, pool *sql.DB) (bool, error) {
	var exists bool
	err := pool.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM users)").Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check for users: %w", err)
	}
	return exists, nil
}

// FirstAdmin is the account created by the setup form. Username must already be normalized.
type FirstAdmin struct {
	Username     string
	PasswordHash string
	Now          int64
}

// CreateFirstAdmin inserts the first admin and returns its id. created is
// false when any user already exists.
//
// Two browsers may submit the setup form at the same moment; exactly one must
// win. The check and the insert are one statement, so two callers cannot both
// see an empty table. BEGIN IMMEDIATE also takes the write lock up front, so a
// caller waits for a competing writer instead of failing with SQLITE_BUSY
// halfway. Do not split this into "check, then insert".
func CreateFirstAdmin(ctx context.Context, pool *sql.DB, admin FirstAdmin) (id int64, created bool, err error) {
	err = db.WithImmediateTx(ctx, pool, func(conn *sql.Conn) error {
		scanErr := conn.QueryRowContext(ctx,
			`INSERT INTO users (username, password_hash, is_admin, created_at)
             SELECT ?1, ?2, 1, ?3 WHERE NOT EXISTS (SELECT 1 FROM users)
             RETURNING id`,
			admin.Username, admin.PasswordHash, admin.Now).Scan(&id)
		if errors.Is(scanErr, sql.ErrNoRows) {
			return nil
		}
		created = scanErr == nil
		return scanErr
	})
	if err != nil {
		return 0, false, fmt.Errorf("create the first admin: %w", err)
	}
	return id, created, nil
}
