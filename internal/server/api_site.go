package server

import (
	"net/http"
	"strings"

	"github.com/henry40408/bkd/internal/auth"
)

// handleSitePreview answers the add and edit forms: what the page at a URL
// says about itself, so the form can show it before anything is saved.
func (a *App) handleSitePreview(w http.ResponseWriter, r *http.Request, user auth.SessionUser) {
	var request struct {
		URL *string `json:"url"`
	}
	if failure := decodeJSON(r, &request); failure != nil {
		failure.write(w)
		return
	}
	if failure := requireFields(field{"url", request.URL != nil}); failure != nil {
		failure.write(w)
		return
	}
	site, fault := a.sitePreview(r.Context(), user, strings.TrimSpace(*request.URL))
	if fault != nil {
		a.writeAPIError(w, fault)
		return
	}
	writeJSON(w, http.StatusOK, site)
}
