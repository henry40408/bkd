package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
)

// API tokens. A program that cannot hold a cookie, such as a browser extension,
// sends one as `Authorization: Token <token>`. As with sessions the program holds
// a random token and the database holds only its SHA-256. A token has no expiry:
// its owner ends it, and so does disabling the account or resetting its password.

const (
	// MaxAPITokensPerUser keeps the list on the settings page short and bounds
	// what one account can store.
	MaxAPITokensPerUser = 20
	// tokenUseRefreshSeconds: last_used_at moves forward at most this often, so a
	// busy client is not a write per request.
	tokenUseRefreshSeconds int64 = 60 * 60
	// tokenIDLength is how many hex digits of the hash name a token.
	tokenIDLength = 16
)

// APIToken is one row of the token list. ID is how the owner names a token to
// end: the start of the token's SHA-256, which is never reused and cannot be
// turned back into the token. Use it only together with the user id.
type APIToken struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
	// LastUsedAt is nil until the token is first used.
	LastUsedAt *int64 `json:"last_used_at"`
}

// IssuedAPIToken is a new token with its secret, which is shown once and never stored.
type IssuedAPIToken struct {
	APIToken
	Token string `json:"token"`
}

// NewAPIToken says whose token to make and what to call it.
type NewAPIToken struct {
	UserID int64
	Name   string
	Now    int64
	// SessionToken is the cookie token of the session that asks. Create makes the
	// token only while that session still exists, so a request that was already
	// accepted cannot mint a token after the session was ended. Empty for a user
	// the proxy signed in, who has no session.
	SessionToken string
}

// ErrCallerGone means the account was disabled or deleted, or the session that
// asked was ended, between the check at the start of the request and the insert.
var ErrCallerGone = errors.New("the account or session that asked for the token is gone")

// APITokens reads and writes the api_tokens table.
type APITokens struct {
	db     *sql.DB
	logger *slog.Logger
}

func NewAPITokens(db *sql.DB, logger *slog.Logger) *APITokens {
	return &APITokens{db: db, logger: logger}
}

// Create makes a token. The result is the only time the secret can be read. ok
// is false when the user already has MaxAPITokensPerUser. The checks that the
// account is active, the session exists and the limit holds are one statement, so
// a reset or disable that lands while a slow request is still being read cannot
// be followed by a token (as with Sessions.CreateIfUnchanged). The error is
// ErrCallerGone when one of the first two fails.
func (tokens *APITokens) Create(ctx context.Context, request NewAPIToken) (issued IssuedAPIToken, ok bool, err error) {
	token := toHex(randomBytes(32))
	tokenHash := sha256Hex(token)
	sessionHash := ""
	if request.SessionToken != "" {
		sessionHash = sha256Hex(request.SessionToken)
	}
	result, err := tokens.db.ExecContext(ctx,
		`INSERT INTO api_tokens (token_hash, user_id, name, created_at)
         SELECT ?1, ?2, ?3, ?4
         WHERE EXISTS (SELECT 1 FROM users WHERE id = ?2 AND disabled_at IS NULL)
           AND (?6 = '' OR EXISTS (SELECT 1 FROM sessions WHERE token_hash = ?6 AND user_id = ?2))
           AND (SELECT count(*) FROM api_tokens WHERE user_id = ?2) < ?5`,
		tokenHash, request.UserID, request.Name, request.Now, MaxAPITokensPerUser, sessionHash)
	if err != nil {
		return IssuedAPIToken{}, false, fmt.Errorf("create api token: %w", err)
	}
	if rowsAffected(result) != 1 {
		return tokens.whyNotCreated(ctx, request.UserID)
	}
	info := APIToken{ID: tokenHash[:tokenIDLength], Name: request.Name, CreatedAt: request.Now}
	return IssuedAPIToken{APIToken: info, Token: token}, true, nil
}

// whyNotCreated tells a full list from a caller that is gone. Both are rare, and
// a revoke between the insert and this count only changes which answer is given.
func (tokens *APITokens) whyNotCreated(ctx context.Context, userID int64) (IssuedAPIToken, bool, error) {
	var held int
	err := tokens.db.QueryRowContext(ctx, "SELECT count(*) FROM api_tokens WHERE user_id = ?1", userID).Scan(&held)
	if err != nil {
		return IssuedAPIToken{}, false, fmt.Errorf("create api token: %w", err)
	}
	if held >= MaxAPITokensPerUser {
		return IssuedAPIToken{}, false, nil
	}
	return IssuedAPIToken{}, false, ErrCallerGone
}

// Lookup returns the user a token belongs to. ok is false when the token is
// unknown or its account is disabled. A hit records the use.
func (tokens *APITokens) Lookup(ctx context.Context, token string, now int64) (user SessionUser, ok bool, err error) {
	tokenHash := sha256Hex(token)
	var lastUsedAt sql.NullInt64
	err = tokens.db.QueryRowContext(ctx,
		`SELECT users.id, users.username, users.is_admin, api_tokens.last_used_at
         FROM api_tokens JOIN users ON users.id = api_tokens.user_id
         WHERE api_tokens.token_hash = ?1 AND users.disabled_at IS NULL`,
		tokenHash).Scan(&user.UserID, &user.Username, &user.IsAdmin, &lastUsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionUser{}, false, nil
	}
	if err != nil {
		return SessionUser{}, false, fmt.Errorf("look up api token: %w", err)
	}
	if !lastUsedAt.Valid || lastUsedAt.Int64 <= now-tokenUseRefreshSeconds {
		tokens.recordUse(ctx, tokenHash, now)
	}
	return user, true, nil
}

// recordUse never fails the request: the token is already proven valid, and a
// busy database only delays the record to a later request.
func (tokens *APITokens) recordUse(ctx context.Context, tokenHash string, now int64) {
	_, err := tokens.db.ExecContext(ctx,
		"UPDATE api_tokens SET last_used_at = ?2 WHERE token_hash = ?1", tokenHash, now)
	if err != nil {
		tokens.logger.Warn("cannot record the use of an api token", "error", err)
	}
}

// ListForUser returns the user's tokens, newest first.
func (tokens *APITokens) ListForUser(ctx context.Context, userID int64) ([]APIToken, error) {
	rows, err := tokens.db.QueryContext(ctx,
		`SELECT substr(token_hash, 1, 16), name, created_at, last_used_at
         FROM api_tokens WHERE user_id = ?1 ORDER BY created_at DESC, token_hash`, userID)
	if err != nil {
		return nil, fmt.Errorf("list api tokens: %w", err)
	}
	defer func() { _ = rows.Close() }()

	list := []APIToken{}
	for rows.Next() {
		var token APIToken
		var lastUsedAt sql.NullInt64
		if err := rows.Scan(&token.ID, &token.Name, &token.CreatedAt, &lastUsedAt); err != nil {
			return nil, fmt.Errorf("list api tokens: %w", err)
		}
		if lastUsedAt.Valid {
			token.LastUsedAt = &lastUsedAt.Int64
		}
		list = append(list, token)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list api tokens: %w", err)
	}
	return list, nil
}

// RevokeByID ends one of the user's own tokens. found is false when the id is not theirs.
func (tokens *APITokens) RevokeByID(ctx context.Context, userID int64, id string) (found bool, err error) {
	result, err := tokens.db.ExecContext(ctx,
		"DELETE FROM api_tokens WHERE substr(token_hash, 1, 16) = ?1 AND user_id = ?2", id, userID)
	if err != nil {
		return false, fmt.Errorf("revoke api token: %w", err)
	}
	return rowsAffected(result) == 1, nil
}

// RevokeAllTokensForUser ends every token of a user. Use it, with RevokeAllForUser,
// when the account is disabled or its password is reset: enabling the account
// again must not bring back a credential that was handed out before.
func RevokeAllTokensForUser(ctx context.Context, exec Execer, userID int64) error {
	_, err := exec.ExecContext(ctx, "DELETE FROM api_tokens WHERE user_id = ?1", userID)
	if err != nil {
		return fmt.Errorf("revoke api tokens of user %d: %w", userID, err)
	}
	return nil
}
