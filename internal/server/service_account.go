package server

import (
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/users"
)

// The business rules of the account routes. The JSON handlers and the pages both
// call these, so each rule lives in one place and a fault maps to one status.

// caller says who is asking, as far as the session list and the rate limits care.
type caller struct {
	addr      netip.Addr
	userAgent string
	// token is the session cookie value, empty without one.
	token string
}

const maxUserAgentChars = 200

func (a *App) callerOf(r *http.Request) caller {
	token, _ := a.readToken(r)
	return caller{addr: peerAddr(r), userAgent: r.UserAgent(), token: token}
}

// sessionStart says where a session starts, for the session list. A client can send
// any User-Agent, so it is cut short: it is displayed, never trusted.
func (c caller) sessionStart(userID int64) auth.SessionStart {
	userAgent := []rune(c.userAgent)
	if len(userAgent) > maxUserAgentChars {
		userAgent = userAgent[:maxUserAgentChars]
	}
	return auth.SessionStart{
		UserID:    userID,
		Now:       auth.UnixNow(),
		IP:        c.addr.String(),
		UserAgent: string(userAgent),
	}
}

// acquire charges one attempt to the caller's address. Callers refund it on success.
func (a *App) acquire(limiter *auth.Limiter, c caller) *apiError {
	ok, wait := limiter.TryAcquire(c.addr, time.Now())
	if ok {
		return nil
	}
	return tooManyRequests(wait)
}

type setupInput struct {
	Token    string
	Username string
	Password string
}

// setup creates the first admin and returns the token of their new session.
func (a *App) setup(r *http.Request, c caller, input setupInput) (string, *apiError) {
	ctx := r.Context()
	if fault := a.acquire(a.setupLimiter, c); fault != nil {
		return "", fault
	}
	setupToken := a.currentSetupToken()
	if setupToken == nil {
		return "", conflict("setup is already complete")
	}
	if !setupToken.Matches(input.Token) {
		a.logger.Warn("setup refused: wrong setup token", slog.String("ip", c.addr.String()))
		return "", forbidden("invalid setup token")
	}
	// The caller proved they can read the server log, so a typo in the form below
	// should not cost them an attempt.
	a.setupLimiter.Refund(c.addr)

	username, err := auth.NormalizeUsername(input.Username)
	if err != nil {
		return "", badRequest(err.Error())
	}
	if err := auth.ValidatePassword(input.Password, username); err != nil {
		return "", badRequest(err.Error())
	}
	hash, err := a.hasher.Hash(ctx, input.Password)
	if err != nil {
		return "", internal(err)
	}
	userID, created, err := auth.CreateFirstAdmin(ctx, a.db, auth.FirstAdmin{
		Username: username, PasswordHash: hash, Now: auth.UnixNow(),
	})
	if err != nil {
		return "", internal(err)
	}
	// Whoever lost the race, or arrived after the winner, must not see the form again.
	a.clearSetupToken()
	if !created {
		return "", conflict("setup is already complete")
	}
	a.logger.Info("first admin created, setup token discarded",
		slog.String("ip", c.addr.String()), slog.Int64("user_id", userID))
	token, err := a.sessions.Create(ctx, c.sessionStart(userID))
	if err != nil {
		return "", internal(err)
	}
	return token, nil
}

type loginInput struct {
	Username string
	Password string
}

// login returns the token of a new session.
func (a *App) login(r *http.Request, c caller, input loginInput) (string, *apiError) {
	ctx := r.Context()
	if fault := a.acquire(a.loginLimiter, c); fault != nil {
		return "", fault
	}
	record, fault := a.authenticate(r, input)
	if fault != nil {
		return "", fault
	}
	if record == nil {
		a.logger.Warn("login failed", slog.String("ip", c.addr.String()))
		return "", invalidCredentials()
	}
	token, ok, err := a.sessions.CreateIfUnchanged(ctx, c.sessionStart(record.ID), record.PasswordHash)
	if err != nil {
		return "", internal(err)
	}
	if !ok {
		a.logger.Warn("login refused: the account changed while the password was checked",
			slog.String("ip", c.addr.String()))
		return "", invalidCredentials()
	}
	a.loginLimiter.Refund(c.addr)
	return token, nil
}

// authenticate returns the account when the credentials are right and the account
// is active, nil otherwise. Every failure costs about the same time, so timing does
// not reveal which usernames exist.
func (a *App) authenticate(r *http.Request, input loginInput) (*users.LoginRecord, *apiError) {
	ctx := r.Context()
	var record users.LoginRecord
	found := false
	if username, err := auth.NormalizeUsername(input.Username); err == nil {
		var lookupErr error
		record, found, lookupErr = users.FindForLogin(ctx, a.db, username)
		if lookupErr != nil {
			return nil, internal(lookupErr)
		}
	}
	if !found || record.IsDisabled {
		if err := a.hasher.VerifyDummy(ctx, input.Password); err != nil {
			return nil, internal(err)
		}
		return nil, nil
	}
	correct, err := a.hasher.Verify(ctx, input.Password, record.PasswordHash)
	if err != nil {
		return nil, internal(err)
	}
	if !correct {
		return nil, nil
	}
	return &record, nil
}

// refuseProxyUser: a user the proxy signed in has no password here to change or confirm.
func refuseProxyUser(user auth.SessionUser) *apiError {
	if user.ViaProxy {
		return forbidden("your sign-in is managed by the single sign-on provider")
	}
	return nil
}

type passwordChange struct {
	Current string
	New     string
}

// changePassword changes the caller's own password. Every session ends, this one
// included, so it returns the token of a fresh session for this browser only.
func (a *App) changePassword(r *http.Request, c caller, user auth.SessionUser, change passwordChange) (string, *apiError) {
	ctx := r.Context()
	if fault := refuseProxyUser(user); fault != nil {
		return "", fault
	}
	// A stolen session must not be a free oracle for the current password.
	if fault := a.acquire(a.loginLimiter, c); fault != nil {
		return "", fault
	}
	if err := auth.ValidatePassword(change.New, user.Username); err != nil {
		return "", badRequest(err.Error())
	}
	if change.New == change.Current {
		return "", badRequest("new password must differ from the current one")
	}
	record, found, err := users.FindForLogin(ctx, a.db, user.Username)
	if err != nil {
		return "", internal(err)
	}
	if !found {
		return "", unauthorized()
	}
	correct, err := a.hasher.Verify(ctx, change.Current, record.PasswordHash)
	if err != nil {
		return "", internal(err)
	}
	if !correct {
		a.logger.Warn("password change refused: wrong current password",
			slog.String("ip", c.addr.String()), slog.Int64("user_id", user.UserID))
		return "", forbidden("current password is wrong")
	}
	return a.storeNewPassword(r, c, user, record.PasswordHash, change.New)
}

func (a *App) storeNewPassword(r *http.Request, c caller, user auth.SessionUser, storedHash, newPassword string) (string, *apiError) {
	ctx := r.Context()
	newHash, err := a.hasher.Hash(ctx, newPassword)
	if err != nil {
		return "", internal(err)
	}
	changed, err := users.ChangeOwnPassword(ctx, a.db, users.OwnPasswordChange{
		ID: user.UserID, VerifiedHash: storedHash, NewHash: newHash,
	})
	if err != nil {
		return "", internal(err)
	}
	if !changed {
		return "", conflict("password changed meanwhile, try again")
	}
	a.loginLimiter.Refund(c.addr)
	a.logger.Info("password changed", slog.Int64("user_id", user.UserID))
	// Not Create: a reset that lands after the change must not be followed by a session.
	token, ok, err := a.sessions.CreateIfUnchanged(ctx, c.sessionStart(user.UserID), newHash)
	if err != nil {
		return "", internal(err)
	}
	if !ok {
		return "", conflict("password changed meanwhile, sign in again")
	}
	return token, nil
}

// confirmPassword proves the person at the keyboard knows the password, for the
// admin changes that ask for it. It shares the login rate limit: it is a password
// oracle for a stolen session.
func (a *App) confirmPassword(r *http.Request, c caller, user auth.SessionUser, password string) *apiError {
	ctx := r.Context()
	if fault := refuseProxyUser(user); fault != nil {
		return fault
	}
	if fault := a.acquire(a.loginLimiter, c); fault != nil {
		return fault
	}
	record, found, err := users.FindForLogin(ctx, a.db, user.Username)
	if err != nil {
		return internal(err)
	}
	if !found {
		return unauthorized()
	}
	correct, err := a.hasher.Verify(ctx, password, record.PasswordHash)
	if err != nil {
		return internal(err)
	}
	if !correct {
		a.logger.Warn("confirmation refused: wrong password",
			slog.String("ip", c.addr.String()), slog.Int64("user_id", user.UserID))
		return forbidden("password is wrong")
	}
	if c.token == "" {
		return unauthorized()
	}
	if err := a.sessions.Confirm(ctx, c.token, auth.UnixNow()); err != nil {
		return internal(err)
	}
	a.loginLimiter.Refund(c.addr)
	return nil
}

// revokeSession ends one of the caller's own sessions. Another user's id answers
// 404, like a missing one.
func (a *App) revokeSession(r *http.Request, user auth.SessionUser, id string) *apiError {
	found, err := a.sessions.RevokeByID(r.Context(), user.UserID, id)
	if err != nil {
		return internal(err)
	}
	if !found {
		return notFound()
	}
	a.logger.Info("session ended by its owner", slog.Int64("user_id", user.UserID))
	return nil
}

func (a *App) revokeOtherSessions(r *http.Request, c caller, user auth.SessionUser) *apiError {
	count, err := a.sessions.RevokeOthers(r.Context(), user.UserID, c.token)
	if err != nil {
		return internal(err)
	}
	a.logger.Info("other sessions ended by their owner",
		slog.Int64("user_id", user.UserID), slog.Int64("count", count))
	return nil
}

func (a *App) logout(r *http.Request, c caller) *apiError {
	if c.token == "" {
		return nil
	}
	if err := a.sessions.Revoke(r.Context(), c.token); err != nil {
		return internal(err)
	}
	return nil
}
