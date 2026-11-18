package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
)

// Login sessions. The browser holds a random token; the database holds only its
// SHA-256, so a leaked database cannot be replayed as cookies.

const (
	SessionLifetimeSeconds int64 = 30 * 24 * 60 * 60
	// SessionIdleSeconds: a session unused for this long ends, even before
	// SessionLifetimeSeconds is over.
	SessionIdleSeconds int64 = 7 * 24 * 60 * 60
	// idleRefreshSeconds: last_used_at moves forward at most this often, so a
	// busy session is not a write per request.
	idleRefreshSeconds int64 = 60 * 60
	// ConfirmWindowSeconds is how long a password confirmation stays good for the
	// admin changes that ask for one.
	ConfirmWindowSeconds int64 = 5 * 60
)

// SessionUser is the account a valid session belongs to, read fresh from the
// database on every request so that a role change or a disable takes effect at once.
type SessionUser struct {
	UserID   int64
	Username string
	IsAdmin  bool
	// ViaProxy: signed in by a trusted proxy, not by a password and cookie. Such a
	// user has no password here and no session of their own, so the proxy also
	// decides when they leave.
	ViaProxy bool
}

// SessionStart says who starts a session, and from where. IP and UserAgent are
// only shown in the session list, so they may be blank.
type SessionStart struct {
	UserID    int64
	Now       int64
	IP        string
	UserAgent string
}

// SessionInfo is one row of the session list. ID is how the owner names a
// session to end: the start of the token's SHA-256, which is never reused (a
// rowid would be) and cannot be turned back into the cookie. Use it only
// together with the user id.
type SessionInfo struct {
	ID         string `json:"id"`
	CreatedAt  int64  `json:"created_at"`
	LastUsedAt int64  `json:"last_used_at"`
	IP         string `json:"ip"`
	UserAgent  string `json:"user_agent"`
	IsCurrent  bool   `json:"is_current"`
}

// Sessions reads and writes the sessions table.
type Sessions struct {
	db     *sql.DB
	logger *slog.Logger
}

func NewSessions(db *sql.DB, logger *slog.Logger) *Sessions {
	return &Sessions{db: db, logger: logger}
}

func newSessionToken() string {
	return toHex(randomBytes(32))
}

// Create starts a session and returns the raw token for the cookie. Starting
// one proves the password, so it also counts as a confirmation.
func (sessions *Sessions) Create(ctx context.Context, start SessionStart) (string, error) {
	token := newSessionToken()
	_, err := sessions.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, created_at, expires_at, last_used_at,
                               confirmed_at, ip, user_agent)
         VALUES (?1, ?2, ?3, ?4, ?3, ?3, ?5, ?6)`,
		sha256Hex(token), start.UserID, start.Now, start.Now+SessionLifetimeSeconds,
		start.IP, start.UserAgent)
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	return token, nil
}

// CreateIfUnchanged starts a session only if the account is still active and
// still has the password hash the caller just verified. Login spends tens of
// milliseconds in Argon2; a password reset or a disable that lands in that gap
// must not be followed by a session. ok is false when the account changed.
func (sessions *Sessions) CreateIfUnchanged(ctx context.Context, start SessionStart, verifiedHash string) (token string, ok bool, err error) {
	token = newSessionToken()
	result, err := sessions.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, created_at, expires_at, last_used_at,
                               confirmed_at, ip, user_agent)
         SELECT ?1, id, ?3, ?4, ?3, ?3, ?6, ?7 FROM users
         WHERE id = ?2 AND password_hash = ?5 AND disabled_at IS NULL`,
		sha256Hex(token), start.UserID, start.Now, start.Now+SessionLifetimeSeconds,
		verifiedHash, start.IP, start.UserAgent)
	if err != nil {
		return "", false, fmt.Errorf("create session: %w", err)
	}
	created, err := result.RowsAffected()
	if err != nil {
		return "", false, fmt.Errorf("create session: %w", err)
	}
	if created != 1 {
		return "", false, nil
	}
	return token, true, nil
}

// Lookup returns the user for a live session. ok is false when the token is
// unknown, expired, idle for too long, or belongs to a disabled account. A hit
// moves the idle clock forward.
func (sessions *Sessions) Lookup(ctx context.Context, token string, now int64) (user SessionUser, ok bool, err error) {
	tokenHash := sha256Hex(token)
	err = sessions.db.QueryRowContext(ctx,
		`SELECT users.id, users.username, users.is_admin
         FROM sessions JOIN users ON users.id = sessions.user_id
         WHERE sessions.token_hash = ?1 AND sessions.expires_at > ?2
           AND sessions.last_used_at > ?3 AND users.disabled_at IS NULL`,
		tokenHash, now, now-SessionIdleSeconds).Scan(&user.UserID, &user.Username, &user.IsAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionUser{}, false, nil
	}
	if err != nil {
		return SessionUser{}, false, fmt.Errorf("look up session: %w", err)
	}
	sessions.refreshIdleClock(ctx, tokenHash, now)
	return user, true, nil
}

// refreshIdleClock never fails the request: the session is already proven
// valid, and a busy database only delays the refresh to the next request.
func (sessions *Sessions) refreshIdleClock(ctx context.Context, tokenHash string, now int64) {
	_, err := sessions.db.ExecContext(ctx,
		"UPDATE sessions SET last_used_at = ?2 WHERE token_hash = ?1 AND last_used_at <= ?3",
		tokenHash, now, now-idleRefreshSeconds)
	if err != nil {
		sessions.logger.Warn("cannot refresh the session idle clock", "error", err)
	}
}

func (sessions *Sessions) Revoke(ctx context.Context, token string) error {
	_, err := sessions.db.ExecContext(ctx,
		"DELETE FROM sessions WHERE token_hash = ?1", sha256Hex(token))
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

// Execer is what RevokeAllForUser needs; *sql.DB and *sql.Conn both fit, so
// callers can run it inside their own transaction.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// RevokeAllForUser logs a user out everywhere. Use it when the account is
// disabled, deleted or its password is reset.
func RevokeAllForUser(ctx context.Context, exec Execer, userID int64) error {
	_, err := exec.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?1", userID)
	if err != nil {
		return fmt.Errorf("revoke sessions of user %d: %w", userID, err)
	}
	return nil
}

// Confirm records that the owner just typed the password in this session. The
// caller verified it. A session revoked in the meantime (a reset ends all of
// them) stays gone.
func (sessions *Sessions) Confirm(ctx context.Context, token string, now int64) error {
	_, err := sessions.db.ExecContext(ctx,
		"UPDATE sessions SET confirmed_at = ?2 WHERE token_hash = ?1", sha256Hex(token), now)
	if err != nil {
		return fmt.Errorf("confirm session: %w", err)
	}
	return nil
}

func (sessions *Sessions) IsConfirmed(ctx context.Context, token string, now int64) (bool, error) {
	var count int64
	err := sessions.db.QueryRowContext(ctx,
		"SELECT count(*) FROM sessions WHERE token_hash = ?1 AND confirmed_at > ?2",
		sha256Hex(token), now-ConfirmWindowSeconds).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check confirmation: %w", err)
	}
	return count == 1, nil
}

// ListForUser returns the live sessions of one user, most recently used first.
// currentToken marks the caller's own.
func (sessions *Sessions) ListForUser(ctx context.Context, userID int64, currentToken string, now int64) ([]SessionInfo, error) {
	rows, err := sessions.db.QueryContext(ctx,
		`SELECT substr(token_hash, 1, 16), created_at, last_used_at, ip, user_agent, token_hash = ?2
         FROM sessions
         WHERE user_id = ?1 AND expires_at > ?3 AND last_used_at > ?4
         ORDER BY last_used_at DESC, created_at DESC`,
		userID, sha256Hex(currentToken), now, now-SessionIdleSeconds)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	list := []SessionInfo{}
	for rows.Next() {
		var info SessionInfo
		err := rows.Scan(&info.ID, &info.CreatedAt, &info.LastUsedAt, &info.IP, &info.UserAgent, &info.IsCurrent)
		if err != nil {
			return nil, fmt.Errorf("list sessions: %w", err)
		}
		list = append(list, info)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	return list, nil
}

// RevokeByID ends one of the user's own sessions. found is false when the id is not theirs.
func (sessions *Sessions) RevokeByID(ctx context.Context, userID int64, id string) (found bool, err error) {
	result, err := sessions.db.ExecContext(ctx,
		"DELETE FROM sessions WHERE substr(token_hash, 1, 16) = ?1 AND user_id = ?2", id, userID)
	if err != nil {
		return false, fmt.Errorf("revoke session by id: %w", err)
	}
	return rowsAffected(result) == 1, nil
}

// RevokeOthers ends every session of the user except the one holding keepToken.
func (sessions *Sessions) RevokeOthers(ctx context.Context, userID int64, keepToken string) (int64, error) {
	result, err := sessions.db.ExecContext(ctx,
		"DELETE FROM sessions WHERE user_id = ?1 AND token_hash != ?2", userID, sha256Hex(keepToken))
	if err != nil {
		return 0, fmt.Errorf("revoke other sessions: %w", err)
	}
	return rowsAffected(result), nil
}

func (sessions *Sessions) DeleteExpired(ctx context.Context, now int64) (int64, error) {
	result, err := sessions.db.ExecContext(ctx,
		"DELETE FROM sessions WHERE expires_at <= ?1 OR last_used_at <= ?2", now, now-SessionIdleSeconds)
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	return rowsAffected(result), nil
}

// rowsAffected reports 0 when the driver cannot count; SQLite always can.
func rowsAffected(result sql.Result) int64 {
	count, err := result.RowsAffected()
	if err != nil {
		return 0
	}
	return count
}
