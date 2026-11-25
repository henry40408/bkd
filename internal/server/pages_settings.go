package server

import (
	"net/http"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/linkcheck"
	"github.com/henry40408/bkd/internal/views"
)

const unknownBrowser = "Unknown browser"

// formError is a refusal shown on the form again.
type formError struct {
	status  int
	message string
}

// pageAccountMoved keeps the address of the account page, which the settings
// page took over, working for bookmarks and old links.
func pageAccountMoved(w http.ResponseWriter, _ *http.Request, _ auth.SessionUser) *pageError {
	redirect(w, "/settings")
	return nil
}

func (a *App) pageSettings(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError {
	view, failure := a.settingsView(r, user, r.URL.Query().Get("notice"))
	if failure != nil {
		return failure
	}
	writeHTML(w, http.StatusOK, views.SettingsPage(view))
	return nil
}

func (a *App) settingsView(r *http.Request, user auth.SessionUser, notice string) (views.SettingsView, *pageError) {
	shell, failure := a.shellFor(r.Context(), user, views.SectionSettings, notice)
	if failure != nil {
		return views.SettingsView{}, failure
	}
	saved, fault := a.linkCheckSettings(r.Context(), user)
	if fault != nil {
		return views.SettingsView{}, fromAPI(fault)
	}
	tokens, err := a.apiTokens.ListForUser(r.Context(), user.UserID)
	if err != nil {
		return views.SettingsView{}, fromAPI(internal(err))
	}
	now := auth.UnixNow()
	sessions, failure := a.sessionRows(r, user, now)
	if failure != nil {
		return views.SettingsView{}, failure
	}
	return views.SettingsView{
		Shell:              shell,
		LinkCheckEnabled:   saved.LinkCheckEnabled,
		LinkCheckUserAgent: saved.LinkCheckUserAgent,
		DefaultUserAgent:   linkcheck.DefaultUserAgent(),
		MaxUserAgentLength: linkcheck.MaxUserAgentLength,
		ServerChecksLinks:  a.backgroundOn,
		Now:                now,
		Tokens:             tokenRows(tokens),
		MaxTokenNameLength: maxTokenNameChars,
		Sessions:           sessions,
	}, nil
}

// sessionRows lists the person's browsers. A user the proxy signed in has no
// password and no sessions here.
func (a *App) sessionRows(r *http.Request, user auth.SessionUser, now int64) ([]views.SessionRow, *pageError) {
	if user.ViaProxy {
		return nil, nil
	}
	infos, err := a.sessions.ListForUser(r.Context(), user.UserID, a.callerOf(r).token, now)
	if err != nil {
		return nil, fromAPI(internal(err))
	}
	rows := make([]views.SessionRow, len(infos))
	for i, info := range infos {
		agent := info.UserAgent
		if agent == "" {
			agent = unknownBrowser
		}
		rows[i] = views.SessionRow{
			ID: info.ID, Agent: agent, IP: info.IP, LastUsedAt: info.LastUsedAt, IsCurrent: info.IsCurrent,
		}
	}
	return rows, nil
}

// refusedSettings is a settings form that was refused, with what was typed in it.
type refusedSettings struct {
	formError
	enabled   bool
	userAgent string
}

// writeSettingsError shows the form again with the refused values, not the stored ones.
func (a *App) writeSettingsError(w http.ResponseWriter, r *http.Request, user auth.SessionUser, refused refusedSettings) *pageError {
	view, failure := a.settingsView(r, user, "")
	if failure != nil {
		return failure
	}
	view.LinkCheckEnabled = refused.enabled
	view.LinkCheckUserAgent = refused.userAgent
	view.Error = refused.message
	writeHTML(w, refused.status, views.SettingsPage(view))
	return nil
}

func (a *App) pageSaveSettings(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError {
	fields, failure := formOf(r)
	if failure != nil {
		return failure
	}
	// A ticked checkbox sends a value; an unticked one sends nothing.
	_, enabled := fields.lookup("link_check_enabled")
	// A post without the box (a script, or a page from before the upgrade) keeps the User-Agent.
	change := linkCheckChange{Enabled: enabled}
	agent, hasAgent := fields.lookup("link_check_user_agent")
	if hasAgent {
		change.UserAgent = &agent
	}
	_, fault := a.saveLinkCheckSettings(r.Context(), user, change)
	if fault == nil {
		redirectWithNotice(w, "/settings", "settings-saved")
		return nil
	}
	status, message, other := formRefusal(fault)
	if other != nil {
		return other
	}
	return a.writeSettingsError(w, r, user, refusedSettings{
		formError: formError{status, message}, enabled: enabled, userAgent: agent,
	})
}

func (a *App) writePasswordError(w http.ResponseWriter, r *http.Request, user auth.SessionUser, refusal formError) *pageError {
	view, failure := a.settingsView(r, user, "")
	if failure != nil {
		return failure
	}
	view.PasswordError = refusal.message
	writeHTML(w, refusal.status, views.SettingsPage(view))
	return nil
}

func (a *App) pageChangePassword(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError {
	_, values, failure := requiredFields(r, "current", "new", "again")
	if failure != nil {
		return failure
	}
	// Before the two new passwords are compared: the settings page of a proxy
	// user has no password form to show the mismatch on.
	if fault := refuseProxyUser(user); fault != nil {
		return fromAPI(fault)
	}
	current, next, again := values[0], values[1], values[2]
	if next != again {
		return a.writePasswordError(w, r, user, formError{http.StatusBadRequest, "The two new passwords are different."})
	}
	token, fault := a.changePassword(r, a.callerOf(r), user, passwordChange{Current: current, New: next})
	if fault == nil {
		w.Header().Set("Set-Cookie", a.sessionCookie(token))
		redirectWithNotice(w, "/settings", "password-changed")
		return nil
	}
	status, message, other := formRefusal(fault)
	if other != nil {
		return other
	}
	return a.writePasswordError(w, r, user, formError{status, message})
}

func (a *App) pageRevokeSession(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError {
	if fault := a.revokeSession(r, user, r.PathValue("id")); fault != nil {
		return fromAPI(fault)
	}
	redirectWithNotice(w, "/settings", "session-ended")
	return nil
}

func (a *App) pageRevokeOtherSessions(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError {
	if fault := a.revokeOtherSessions(r, a.callerOf(r), user); fault != nil {
		return fromAPI(fault)
	}
	redirectWithNotice(w, "/settings", "others-ended")
	return nil
}
