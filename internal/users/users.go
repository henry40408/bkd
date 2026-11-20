// Package users holds the users table: login lookups and account management.
//
// Every change that could remove an admin runs in a BEGIN IMMEDIATE transaction
// that first checks another active admin remains. Two admins demoting each other
// at the same moment are serialized, so the second one is refused and the system
// never ends up with nobody who can manage it. There is no command-line recovery.
package users

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/db"
)

// noPassword is the stored hash of an account that has no password. It is not a
// valid Argon2 string, so no password verifies against it.
const noPassword = "!"

const anotherActiveAdminQuery = `SELECT EXISTS (SELECT 1 FROM users
    WHERE id <> ?1 AND is_admin = 1 AND disabled_at IS NULL)`

type LoginRecord struct {
	ID           int64
	PasswordHash string
	IsDisabled   bool
}

// FindForLogin looks an account up by username, which must already be
// normalized (lowercase). found is false when there is no such account.
func FindForLogin(ctx context.Context, pool *sql.DB, username string) (record LoginRecord, found bool, err error) {
	var disabledAt sql.NullInt64
	err = pool.QueryRowContext(ctx,
		"SELECT id, password_hash, disabled_at FROM users WHERE username = ?1", username).
		Scan(&record.ID, &record.PasswordHash, &disabledAt)
	if errors.Is(err, sql.ErrNoRows) {
		return LoginRecord{}, false, nil
	}
	if err != nil {
		return LoginRecord{}, false, fmt.Errorf("find user for login: %w", err)
	}
	record.IsDisabled = disabledAt.Valid
	return record, true, nil
}

// ForwardedAccount is what a trusted proxy said about the caller.
type ForwardedAccount struct {
	// Username must already be normalized.
	Username string
	// IsAdmin is non-nil when the proxy decides the role (an admin group is configured).
	IsAdmin *bool
	Now     int64
}

// AdoptedUser is the account a forwarded identity resolved to.
type AdoptedUser struct {
	ID      int64
	IsAdmin bool
}

// AdoptForwarded finds the account a trusted proxy named, creating it on first
// sight, and applies the role the proxy decided. found is false when the caller
// gets no access: the account is disabled.
//
// When no admin group decides the role, the first account of an empty database
// is the admin, as in setup. The emptiness check and the insert are one
// statement inside BEGIN IMMEDIATE, so two first requests at once cannot both
// become admin.
//
// An account of the same name that has a password is the same person, so it is
// reused.
func AdoptForwarded(ctx context.Context, pool *sql.DB, logger *slog.Logger, account ForwardedAccount) (adopted AdoptedUser, found bool, err error) {
	existing, found, err := findForwarded(ctx, pool, account.Username)
	if err != nil {
		return AdoptedUser{}, false, err
	}
	if found {
		return settleForwarded(ctx, pool, logger, existing, account.IsAdmin)
	}
	if err := insertForwarded(ctx, pool, logger, account); err != nil {
		return AdoptedUser{}, false, err
	}
	existing, found, err = findForwarded(ctx, pool, account.Username)
	if err != nil || !found {
		return AdoptedUser{}, false, err
	}
	return settleForwarded(ctx, pool, logger, existing, account.IsAdmin)
}

func insertForwarded(ctx context.Context, pool *sql.DB, logger *slog.Logger, account ForwardedAccount) error {
	var id int64
	var isAdmin bool
	created := false
	err := db.WithImmediateTx(ctx, pool, func(conn *sql.Conn) error {
		scanErr := conn.QueryRowContext(ctx,
			`INSERT INTO users (username, password_hash, is_admin, created_at)
             VALUES (?1, ?2, COALESCE(?3, NOT EXISTS (SELECT 1 FROM users)), ?4)
             ON CONFLICT (username) DO NOTHING RETURNING id, is_admin`,
			account.Username, noPassword, account.IsAdmin, account.Now).Scan(&id, &isAdmin)
		if errors.Is(scanErr, sql.ErrNoRows) {
			return nil
		}
		created = scanErr == nil
		return scanErr
	})
	if err != nil {
		return fmt.Errorf("create forwarded user: %w", err)
	}
	if created {
		logger.Info("account created from forward auth", "user_id", id, "is_admin", isAdmin)
	}
	return nil
}

type forwardedRow struct {
	id         int64
	isAdmin    bool
	isDisabled bool
}

func findForwarded(ctx context.Context, pool *sql.DB, username string) (row forwardedRow, found bool, err error) {
	var disabledAt sql.NullInt64
	err = pool.QueryRowContext(ctx,
		"SELECT id, is_admin, disabled_at FROM users WHERE username = ?1", username).
		Scan(&row.id, &row.isAdmin, &disabledAt)
	if errors.Is(err, sql.ErrNoRows) {
		return forwardedRow{}, false, nil
	}
	if err != nil {
		return forwardedRow{}, false, fmt.Errorf("find forwarded user: %w", err)
	}
	row.isDisabled = disabledAt.Valid
	return row, true, nil
}

func settleForwarded(ctx context.Context, pool *sql.DB, logger *slog.Logger, row forwardedRow, wantedAdmin *bool) (AdoptedUser, bool, error) {
	if row.isDisabled {
		return AdoptedUser{}, false, nil
	}
	if wantedAdmin == nil || *wantedAdmin == row.isAdmin {
		return AdoptedUser{ID: row.id, IsAdmin: row.isAdmin}, true, nil
	}
	isAdmin, err := changeForwardedRole(ctx, pool, row.id, *wantedAdmin)
	if err != nil {
		return AdoptedUser{}, false, err
	}
	if isAdmin == *wantedAdmin {
		logger.Info("role set by forward auth", "user_id", row.id, "is_admin", isAdmin)
	}
	return AdoptedUser{ID: row.id, IsAdmin: isAdmin}, true, nil
}

// rowQuerier is what the lookups need; *sql.DB and *sql.Conn both fit, so the
// same query runs inside or outside a transaction.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func anotherActiveAdmin(ctx context.Context, query rowQuerier, id int64) (bool, error) {
	var found bool
	if err := query.QueryRowContext(ctx, anotherActiveAdminQuery, id).Scan(&found); err != nil {
		return false, fmt.Errorf("look for another admin: %w", err)
	}
	return found, nil
}

// changeForwardedRole returns the role the account has afterwards. Like every
// demotion, this keeps the last active admin: the proxy cannot lock everyone
// out of user management.
func changeForwardedRole(ctx context.Context, pool *sql.DB, id int64, isAdmin bool) (bool, error) {
	// The last admin asks for this on every request while outside the group, so
	// the common refusal is decided without the write lock.
	if !isAdmin {
		another, err := anotherActiveAdmin(ctx, pool, id)
		if err != nil || !another {
			return true, err
		}
	}
	changed := false
	err := db.WithImmediateTx(ctx, pool, func(conn *sql.Conn) error {
		if !isAdmin {
			another, err := anotherActiveAdmin(ctx, conn, id)
			if err != nil || !another {
				return err
			}
		}
		_, err := conn.ExecContext(ctx, "UPDATE users SET is_admin = ?1 WHERE id = ?2", isAdmin, id)
		changed = err == nil
		return err
	})
	if err != nil {
		return false, fmt.Errorf("set forwarded role: %w", err)
	}
	if !changed {
		return true, nil
	}
	return isAdmin, nil
}

// UsernameOf returns the username of an account; found is false when it is gone.
func UsernameOf(ctx context.Context, pool *sql.DB, id int64) (username string, found bool, err error) {
	err = pool.QueryRowContext(ctx, "SELECT username FROM users WHERE id = ?1", id).Scan(&username)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("find username: %w", err)
	}
	return username, true, nil
}

func LinkCheckEnabled(ctx context.Context, pool *sql.DB, id int64) (bool, error) {
	return queryFlag(ctx, pool, "SELECT link_check_enabled FROM users WHERE id = ?1", id)
}

// WantsBackgroundCheck is true for an account that opted in and may still sign
// in: the two things the background check needs.
func WantsBackgroundCheck(ctx context.Context, pool *sql.DB, id int64) (bool, error) {
	return queryFlag(ctx, pool,
		"SELECT link_check_enabled AND disabled_at IS NULL FROM users WHERE id = ?1", id)
}

// queryFlag is false for an account that does not exist.
func queryFlag(ctx context.Context, pool *sql.DB, query string, id int64) (bool, error) {
	var flag bool
	err := pool.QueryRowContext(ctx, query, id).Scan(&flag)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read user flag: %w", err)
	}
	return flag, nil
}

func SetLinkCheckEnabled(ctx context.Context, pool *sql.DB, id int64, enabled bool) error {
	_, err := pool.ExecContext(ctx,
		"UPDATE users SET link_check_enabled = ?2 WHERE id = ?1", id, enabled)
	if err != nil {
		return fmt.Errorf("set link check: %w", err)
	}
	return nil
}

type UserRecord struct {
	ID         int64
	Username   string
	IsAdmin    bool
	IsDisabled bool
	CreatedAt  int64
}

// Change says what happened to an account change.
type Change int

const (
	ChangeDone Change = iota
	ChangeNotFound
	// ChangeLastAdmin: refused, it would leave no active admin.
	ChangeLastAdmin
	// ChangeCallerNotAdmin: refused, the caller is no longer an active admin. The
	// route checked this before a slow password hash; a demotion may have landed
	// since, so it is checked again inside the transaction.
	ChangeCallerNotAdmin
	// ChangeUsernameTaken: refused, only Create reports it.
	ChangeUsernameTaken
)

type NewAccount struct {
	// Username must already be normalized.
	Username     string
	PasswordHash string
	IsAdmin      bool
	Now          int64
}

func List(ctx context.Context, pool *sql.DB) ([]UserRecord, error) {
	rows, err := pool.QueryContext(ctx,
		"SELECT id, username, is_admin, disabled_at, created_at FROM users ORDER BY username")
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer func() { _ = rows.Close() }()

	records := []UserRecord{}
	for rows.Next() {
		var record UserRecord
		var disabledAt sql.NullInt64
		if err := rows.Scan(&record.ID, &record.Username, &record.IsAdmin, &disabledAt, &record.CreatedAt); err != nil {
			return nil, fmt.Errorf("list users: %w", err)
		}
		record.IsDisabled = disabledAt.Valid
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	return records, nil
}

// Create adds an account on behalf of caller. The record is valid only when the
// change is ChangeDone; ChangeUsernameTaken means the name is in use and
// nothing was overwritten.
func Create(ctx context.Context, pool *sql.DB, caller int64, account NewAccount) (UserRecord, Change, error) {
	change := ChangeDone
	record := UserRecord{
		Username:  account.Username,
		IsAdmin:   account.IsAdmin,
		CreatedAt: account.Now,
	}
	err := db.WithImmediateTx(ctx, pool, func(conn *sql.Conn) error {
		allowed, err := callerIsActiveAdmin(ctx, conn, caller)
		if err != nil {
			return err
		}
		if !allowed {
			change = ChangeCallerNotAdmin
			return nil
		}
		scanErr := conn.QueryRowContext(ctx,
			`INSERT INTO users (username, password_hash, is_admin, created_at)
             VALUES (?1, ?2, ?3, ?4) ON CONFLICT (username) DO NOTHING RETURNING id`,
			account.Username, account.PasswordHash, account.IsAdmin, account.Now).Scan(&record.ID)
		if errors.Is(scanErr, sql.ErrNoRows) {
			change = ChangeUsernameTaken
			return nil
		}
		return scanErr
	})
	if err != nil {
		return UserRecord{}, ChangeDone, fmt.Errorf("create user: %w", err)
	}
	return record, change, nil
}

// accountChange is one change that caller makes to target. runChange refuses it
// or applies it, all inside one BEGIN IMMEDIATE transaction. removesAdmin says
// the change would take away the target's power to manage users.
type accountChange struct {
	caller       int64
	target       int64
	removesAdmin bool
	apply        func(conn *sql.Conn) error
}

func runChange(ctx context.Context, pool *sql.DB, change accountChange) (Change, error) {
	result := ChangeDone
	err := db.WithImmediateTx(ctx, pool, func(conn *sql.Conn) error {
		refusal, err := check(ctx, conn, change)
		if err != nil {
			return err
		}
		if refusal != ChangeDone {
			result = refusal
			return nil
		}
		return change.apply(conn)
	})
	if err != nil {
		return ChangeDone, fmt.Errorf("change user: %w", err)
	}
	return result, nil
}

func SetAdmin(ctx context.Context, pool *sql.DB, caller, id int64, isAdmin bool) (Change, error) {
	return runChange(ctx, pool, accountChange{
		caller:       caller,
		target:       id,
		removesAdmin: !isAdmin,
		apply: func(conn *sql.Conn) error {
			_, err := conn.ExecContext(ctx, "UPDATE users SET is_admin = ?1 WHERE id = ?2", isAdmin, id)
			return err
		},
	})
}

// SetDisabled records disabledAt, or enables the account when it is nil.
// Disabling also ends every session of the account; enabling does not restore them.
func SetDisabled(ctx context.Context, pool *sql.DB, caller, id int64, disabledAt *int64) (Change, error) {
	return runChange(ctx, pool, accountChange{
		caller:       caller,
		target:       id,
		removesAdmin: disabledAt != nil,
		apply: func(conn *sql.Conn) error {
			if _, err := conn.ExecContext(ctx, "UPDATE users SET disabled_at = ?1 WHERE id = ?2", disabledAt, id); err != nil {
				return err
			}
			if disabledAt == nil {
				return nil
			}
			return auth.RevokeAllForUser(ctx, conn, id)
		},
	})
}

// ResetPassword replaces the password and ends every session, so a stolen
// session dies with the old password.
func ResetPassword(ctx context.Context, pool *sql.DB, caller, id int64, passwordHash string) (Change, error) {
	return runChange(ctx, pool, accountChange{
		caller: caller,
		target: id,
		apply: func(conn *sql.Conn) error {
			if _, err := conn.ExecContext(ctx, "UPDATE users SET password_hash = ?1 WHERE id = ?2", passwordHash, id); err != nil {
				return err
			}
			return auth.RevokeAllForUser(ctx, conn, id)
		},
	})
}

// OwnPasswordChange replaces a user's own password. VerifiedHash is the hash
// the caller checked the current password against.
type OwnPasswordChange struct {
	ID           int64
	VerifiedHash string
	NewHash      string
}

// ChangeOwnPassword replaces a user's own password and ends every session, but
// only if the stored hash is still VerifiedHash. The caller spent tens of
// milliseconds in Argon2 checking the current password; a reset or another
// change in that gap must win. changed is false when the hash changed or the
// account is gone.
func ChangeOwnPassword(ctx context.Context, pool *sql.DB, change OwnPasswordChange) (changed bool, err error) {
	err = db.WithImmediateTx(ctx, pool, func(conn *sql.Conn) error {
		result, err := conn.ExecContext(ctx,
			`UPDATE users SET password_hash = ?3
             WHERE id = ?1 AND password_hash = ?2 AND disabled_at IS NULL`,
			change.ID, change.VerifiedHash, change.NewHash)
		if err != nil {
			return err
		}
		updated, err := result.RowsAffected()
		if err != nil || updated != 1 {
			return err
		}
		changed = true
		return auth.RevokeAllForUser(ctx, conn, change.ID)
	})
	if err != nil {
		return false, fmt.Errorf("change own password: %w", err)
	}
	return changed, nil
}

// Delete removes the account with its sessions, bookmarks and tags (foreign keys cascade).
func Delete(ctx context.Context, pool *sql.DB, caller, id int64) (Change, error) {
	return runChange(ctx, pool, accountChange{
		caller:       caller,
		target:       id,
		removesAdmin: true,
		apply: func(conn *sql.Conn) error {
			_, err := conn.ExecContext(ctx, "DELETE FROM users WHERE id = ?1", id)
			return err
		},
	})
}

func callerIsActiveAdmin(ctx context.Context, conn *sql.Conn, caller int64) (bool, error) {
	var allowed bool
	err := conn.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT 1 FROM users WHERE id = ?1 AND is_admin = 1 AND disabled_at IS NULL)",
		caller).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("check caller: %w", err)
	}
	return allowed, nil
}

// check returns the reason to stop, or ChangeDone to go ahead. A disabled
// account does not count as an admin, so only an active admin can be the last one.
func check(ctx context.Context, conn *sql.Conn, change accountChange) (Change, error) {
	allowed, err := callerIsActiveAdmin(ctx, conn, change.caller)
	if err != nil {
		return ChangeDone, err
	}
	if !allowed {
		return ChangeCallerNotAdmin, nil
	}
	var isAdmin bool
	var disabledAt sql.NullInt64
	err = conn.QueryRowContext(ctx,
		"SELECT is_admin, disabled_at FROM users WHERE id = ?1", change.target).Scan(&isAdmin, &disabledAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ChangeNotFound, nil
	}
	if err != nil {
		return ChangeDone, fmt.Errorf("check target: %w", err)
	}
	isActiveAdmin := isAdmin && !disabledAt.Valid
	if !change.removesAdmin || !isActiveAdmin {
		return ChangeDone, nil
	}
	another, err := anotherActiveAdmin(ctx, conn, change.target)
	if err != nil {
		return ChangeDone, err
	}
	if !another {
		return ChangeLastAdmin, nil
	}
	return ChangeDone, nil
}
