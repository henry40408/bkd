package server

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/henry40408/bkd/internal/auth"
)

// The linkding-compatible API (/api/bookmarks/, /api/tags/) is for programs that
// send `Authorization: Token <token>` (or `Bearer`, as linkding allows). It answers in linkding's shapes, so its
// errors are {"detail": "..."} where the rest of the API sends {"error": "..."}.
//
// These routes take the token and nothing else: no cookie, no forwarded user. A
// browser extension sends an Origin of its own (chrome-extension://...), so the
// same-origin check of the other routes is left out, and with it the reason for
// it: a page on another site cannot make the browser add an Authorization header.

const (
	messageNoCredentials = "Authentication credentials were not provided."
	messageNoToken       = "Invalid token header. No credentials provided."
	messageSpacedToken   = "Invalid token header. Token string should not contain spaces."
	messageBadToken      = "Invalid token."
	messageNotFound      = "Not found."
)

// tokenGuarded runs the handler for the owner of the token, or answers 401 before
// the body is read.
func (a *App) tokenGuarded(handle userHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, fault := a.userFromToken(r)
		if fault != nil {
			a.writeDetailError(w, fault)
			return
		}
		handle(w, r, user)
	})
}

// userFromToken reads the header the way Django REST framework's token
// authentication does, so a client sees the messages it knows.
func (a *App) userFromToken(r *http.Request) (auth.SessionUser, *apiError) {
	fields := strings.Fields(r.Header.Get("Authorization"))
	if len(fields) == 0 || !isTokenKeyword(fields[0]) {
		return auth.SessionUser{}, unauthenticated(messageNoCredentials)
	}
	if len(fields) == 1 {
		return auth.SessionUser{}, unauthenticated(messageNoToken)
	}
	if len(fields) > 2 {
		return auth.SessionUser{}, unauthenticated(messageSpacedToken)
	}
	user, ok, err := a.apiTokens.Lookup(r.Context(), fields[1], auth.UnixNow())
	if err != nil {
		return auth.SessionUser{}, internal(err)
	}
	if !ok {
		return auth.SessionUser{}, unauthenticated(messageBadToken)
	}
	return user, nil
}

// isTokenKeyword accepts the two words linkding does, in any case: clients send
// "Token" (its own documentation) or "Bearer" (what many HTTP libraries expect).
func isTokenKeyword(word string) bool {
	return strings.EqualFold(word, "Token") || strings.EqualFold(word, "Bearer")
}

func unauthenticated(message string) *apiError {
	return &apiError{status: http.StatusUnauthorized, message: message}
}

func detailNotFound() *apiError {
	return &apiError{status: http.StatusNotFound, message: messageNotFound}
}

type detailBody struct {
	Detail string `json:"detail"`
}

// writeDetailError answers in linkding's error shapes and logs internal faults:
// {"detail": "..."}, or {"<field>": ["..."]} for a field that was refused.
func (a *App) writeDetailError(w http.ResponseWriter, e *apiError) {
	if e.cause != nil {
		a.logger.Error("internal error", slog.Any("error", e.cause))
	}
	if e.status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Token")
	}
	if e.field != "" {
		writeJSON(w, e.status, map[string][]string{e.field: {e.message}})
		return
	}
	writeJSON(w, e.status, detailBody{Detail: e.message})
}
