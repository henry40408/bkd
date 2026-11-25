package server

import (
	"net/http"

	"github.com/henry40408/bkd/internal/auth"
)

type userView struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	IsAdmin  bool   `json:"is_admin"`
	// Forwarded: signed in by the reverse proxy, no password and no Sign out here.
	Forwarded bool `json:"forwarded"`
}

type stateView struct {
	// SetupRequired is true until the first admin exists: the front end shows the
	// setup form.
	SetupRequired bool      `json:"setup_required"`
	User          *userView `json:"user"`
}

// handleState is what the front end needs to pick its first screen. Open to everyone.
func (a *App) handleState(w http.ResponseWriter, r *http.Request) {
	user, signedIn, fault := a.userFromRequest(r)
	if fault != nil {
		a.writeAPIError(w, fault)
		return
	}
	view := stateView{SetupRequired: a.currentSetupToken() != nil}
	if signedIn {
		view.User = &userView{
			ID: user.UserID, Username: user.Username, IsAdmin: user.IsAdmin, Forwarded: user.ViaProxy,
		}
	}
	writeJSON(w, http.StatusOK, view)
}

// writeSession answers with the session cookie and no body.
func (a *App) writeSession(w http.ResponseWriter, token string, status int) {
	w.Header().Set("Set-Cookie", a.sessionCookie(token))
	w.WriteHeader(status)
}

func (a *App) handleSetup(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Token    *string `json:"token"`
		Username *string `json:"username"`
		Password *string `json:"password"`
	}
	if failure := decodeJSON(r, &request); failure != nil {
		failure.write(w)
		return
	}
	if failure := requireFields(
		field{"token", request.Token != nil},
		field{"username", request.Username != nil},
		field{"password", request.Password != nil},
	); failure != nil {
		failure.write(w)
		return
	}
	input := setupInput{Token: *request.Token, Username: *request.Username, Password: *request.Password}
	token, fault := a.setup(r, a.callerOf(r), input)
	if fault != nil {
		a.writeAPIError(w, fault)
		return
	}
	a.writeSession(w, token, http.StatusCreated)
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Username *string `json:"username"`
		Password *string `json:"password"`
	}
	if failure := decodeJSON(r, &request); failure != nil {
		failure.write(w)
		return
	}
	if failure := requireFields(
		field{"username", request.Username != nil},
		field{"password", request.Password != nil},
	); failure != nil {
		failure.write(w)
		return
	}
	token, fault := a.login(r, a.callerOf(r), loginInput{Username: *request.Username, Password: *request.Password})
	if fault != nil {
		a.writeAPIError(w, fault)
		return
	}
	a.writeSession(w, token, http.StatusOK)
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if fault := a.logout(r, a.callerOf(r)); fault != nil {
		a.writeAPIError(w, fault)
		return
	}
	w.Header().Set("Set-Cookie", a.clearedCookie())
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleChangePassword(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	var request struct {
		Current *string `json:"current"`
		New     *string `json:"new"`
	}
	if failure := decodeJSON(r, &request); failure != nil {
		failure.write(w)
		return
	}
	if failure := requireFields(
		field{"current", request.Current != nil},
		field{"new", request.New != nil},
	); failure != nil {
		failure.write(w)
		return
	}
	change := passwordChange{Current: *request.Current, New: *request.New}
	token, fault := a.changePassword(r, a.callerOf(r), user, change)
	if fault != nil {
		a.writeAPIError(w, fault)
		return
	}
	a.writeSession(w, token, http.StatusNoContent)
}

func (a *App) handleConfirmPassword(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	var request struct {
		Password *string `json:"password"`
	}
	if failure := decodeJSON(r, &request); failure != nil {
		failure.write(w)
		return
	}
	if failure := requireFields(field{"password", request.Password != nil}); failure != nil {
		failure.write(w)
		return
	}
	if fault := a.confirmPassword(r, a.callerOf(r), user, *request.Password); fault != nil {
		a.writeAPIError(w, fault)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListSessions shows the caller's live sessions, so they can spot a browser
// they do not know.
func (a *App) handleListSessions(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	sessions, err := a.sessions.ListForUser(r.Context(), user.UserID, a.callerOf(r).token, auth.UnixNow())
	if err != nil {
		a.writeAPIError(w, internal(err))
		return
	}
	if sessions == nil {
		sessions = []auth.SessionInfo{}
	}
	writeJSON(w, http.StatusOK, sessions)
}

func (a *App) handleRevokeSession(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	if fault := a.revokeSession(r, user, r.PathValue("id")); fault != nil {
		a.writeAPIError(w, fault)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleRevokeOtherSessions(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	if fault := a.revokeOtherSessions(r, a.callerOf(r), user); fault != nil {
		a.writeAPIError(w, fault)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type settingsView struct {
	// LinkCheckEnabled: whether the server may check this user's bookmarks in the background.
	LinkCheckEnabled bool `json:"link_check_enabled"`
	// LinkCheckUserAgent is the User-Agent of this user's link checks; empty
	// means the built-in one.
	LinkCheckUserAgent string `json:"link_check_user_agent"`
}

// handleListTokens shows the caller's API tokens, without their secrets.
func (a *App) handleListTokens(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	tokens, err := a.apiTokens.ListForUser(r.Context(), user.UserID)
	if err != nil {
		a.writeAPIError(w, internal(err))
		return
	}
	writeJSON(w, http.StatusOK, tokens)
}

// handleCreateToken answers the new token with its secret, the only time it is shown.
func (a *App) handleCreateToken(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	var request struct {
		Name *string `json:"name"`
	}
	if failure := decodeJSON(r, &request); failure != nil {
		failure.write(w)
		return
	}
	if failure := requireFields(field{"name", request.Name != nil}); failure != nil {
		failure.write(w)
		return
	}
	issued, fault := a.createAPIToken(r, user, *request.Name)
	if fault != nil {
		a.writeAPIError(w, fault)
		return
	}
	writeJSON(w, http.StatusCreated, issued)
}

func (a *App) handleRevokeToken(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	if fault := a.revokeAPIToken(r.Context(), user, r.PathValue("id")); fault != nil {
		a.writeAPIError(w, fault)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
