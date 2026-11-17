package server

import (
	"net/http"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/views"
)

// Setup, sign-in, sign-out and the password confirmation, as forms. Each one calls
// the service the JSON route calls, so the rate limits, the timing guards and the
// session rules exist once.

// formOf reads the form post a page handler takes.
func formOf(r *http.Request) (formFields, *pageError) {
	fields, failure := readForm(r)
	if failure != nil {
		return formFields{}, plainRefusal(failure)
	}
	return fields, nil
}

// requiredFields reads the form and the named fields in one step.
func requiredFields(r *http.Request, names ...string) (formFields, []string, *pageError) {
	fields, failure := formOf(r)
	if failure != nil {
		return formFields{}, nil, failure
	}
	values, missing := fields.require(names...)
	if missing != nil {
		return formFields{}, nil, plainRefusal(missing)
	}
	return fields, values, nil
}

// writeSessionCookie is what the redirect after sign-in carries.
func (a *App) redirectWithSession(w http.ResponseWriter, token, location string) {
	w.Header().Set("Set-Cookie", a.sessionCookie(token))
	redirect(w, location)
}

func (a *App) pageLoginForm(w http.ResponseWriter, r *http.Request) *pageError {
	query := r.URL.Query()
	next := pathOrHome(query.Get("next"))
	_, signedIn, fault := a.userFromRequest(r)
	if fault != nil {
		return fromAPI(fault)
	}
	if signedIn {
		redirect(w, next)
		return nil
	}
	if a.currentSetupToken() != nil {
		redirect(w, "/setup")
		return nil
	}
	view := views.LoginView{Next: next, SessionEnded: query.Get("notice") == sessionEndedNotice}
	writeHTML(w, http.StatusOK, views.LoginPage(view))
	return nil
}

func (a *App) pageLoginSubmit(w http.ResponseWriter, r *http.Request) *pageError {
	fields, values, failure := requiredFields(r, "username", "password")
	if failure != nil {
		return failure
	}
	username, password := values[0], values[1]
	next := pathOrHome(fields.text("next"))
	token, fault := a.login(r, a.callerOf(r), loginInput{Username: username, Password: password})
	if fault == nil {
		a.redirectWithSession(w, token, next)
		return nil
	}
	status, message, other := formRefusal(fault)
	if other != nil {
		return other
	}
	writeHTML(w, status, views.LoginPage(views.LoginView{Error: message, Username: username, Next: next}))
	return nil
}

func (a *App) pageSetupForm(w http.ResponseWriter, _ *http.Request) *pageError {
	if a.currentSetupToken() == nil {
		redirect(w, "/login")
		return nil
	}
	writeHTML(w, http.StatusOK, views.SetupPage(views.SetupView{}))
	return nil
}

func (a *App) pageSetupSubmit(w http.ResponseWriter, r *http.Request) *pageError {
	_, values, failure := requiredFields(r, "token", "username", "password")
	if failure != nil {
		return failure
	}
	token, username := values[0], values[1]
	session, fault := a.setup(r, a.callerOf(r), setupInput{Token: token, Username: username, Password: values[2]})
	if fault == nil {
		a.redirectWithSession(w, session, "/")
		return nil
	}
	status, message, other := formRefusal(fault)
	if other != nil {
		return other
	}
	writeHTML(w, status, views.SetupPage(views.SetupView{Error: message, Token: token, Username: username}))
	return nil
}

func (a *App) pageLogout(w http.ResponseWriter, r *http.Request) *pageError {
	if fault := a.logout(r, a.callerOf(r)); fault != nil {
		return fromAPI(fault)
	}
	w.Header().Set("Set-Cookie", a.clearedCookie())
	redirect(w, "/login")
	return nil
}

func (a *App) pageConfirmForm(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError {
	shell, failure := a.shellFor(r.Context(), user, views.SectionUsers, r.URL.Query().Get("notice"))
	if failure != nil {
		return failure
	}
	writeHTML(w, http.StatusOK, views.ConfirmPage(views.ConfirmView{Shell: shell}))
	return nil
}

func (a *App) pageConfirmSubmit(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError {
	_, values, failure := requiredFields(r, "password")
	if failure != nil {
		return failure
	}
	fault := a.confirmPassword(r, a.callerOf(r), user, values[0])
	if fault == nil {
		redirectWithNotice(w, "/users", "confirmed")
		return nil
	}
	status, message, other := formRefusal(fault)
	if other != nil {
		return other
	}
	shell, failure := a.shellFor(r.Context(), user, views.SectionUsers, "")
	if failure != nil {
		return failure
	}
	writeHTML(w, status, views.ConfirmPage(views.ConfirmView{Shell: shell, Error: message}))
	return nil
}
