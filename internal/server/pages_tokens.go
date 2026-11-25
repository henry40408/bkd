package server

import (
	"net/http"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/views"
)

func tokenRows(tokens []auth.APIToken) []views.TokenRow {
	rows := make([]views.TokenRow, len(tokens))
	for i, token := range tokens {
		rows[i] = views.TokenRow{
			ID: token.ID, Name: token.Name, CreatedAt: token.CreatedAt, LastUsedAt: token.LastUsedAt,
		}
	}
	return rows
}

// pageCreateToken answers with the settings page itself, because the secret
// cannot go through a redirect: it would end up in an address or a cookie, and a
// reload would show it again.
func (a *App) pageCreateToken(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError {
	_, values, failure := requiredFields(r, "name")
	if failure != nil {
		return failure
	}
	name := values[0]
	issued, fault := a.createAPIToken(r, user, name)
	if fault != nil {
		status, message, other := formRefusal(fault)
		if other != nil {
			return other
		}
		return a.writeTokenForm(w, r, user, tokenForm{status: status, name: name, refusal: message})
	}
	return a.writeTokenForm(w, r, user, tokenForm{status: http.StatusOK, secret: issued.Token})
}

// tokenForm is the token part of the settings page after a post: the secret that
// was just made, or what was refused and what was typed.
type tokenForm struct {
	status  int
	secret  string
	name    string
	refusal string
}

func (a *App) writeTokenForm(w http.ResponseWriter, r *http.Request, user auth.SessionUser, form tokenForm) *pageError {
	view, failure := a.settingsView(r, user, "")
	if failure != nil {
		return failure
	}
	view.NewToken = form.secret
	view.TokenName = form.name
	view.TokenError = form.refusal
	writeHTML(w, form.status, views.SettingsPage(view))
	return nil
}

func (a *App) pageRevokeToken(w http.ResponseWriter, r *http.Request, user auth.SessionUser) *pageError {
	if fault := a.revokeAPIToken(r.Context(), user, r.PathValue("id")); fault != nil {
		return fromAPI(fault)
	}
	redirectWithNotice(w, "/settings", "token-revoked")
	return nil
}
