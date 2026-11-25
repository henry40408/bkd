package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/henry40408/bkd/internal/auth"
)

const maxTokenNameChars = 100

// The business rules of the API token routes, shared by the JSON handlers and
// the settings page. A user the proxy signed in may have tokens too: a program
// cannot pass through the proxy's sign-in page, so the token is how it gets in.

// createAPIToken makes a token and answers it with its secret, which is shown once.
func (a *App) createAPIToken(r *http.Request, user auth.SessionUser, rawName string) (auth.IssuedAPIToken, *apiError) {
	name := strings.TrimSpace(rawName)
	if name == "" {
		return auth.IssuedAPIToken{}, badRequest("name is required")
	}
	if utf8.RuneCountInString(name) > maxTokenNameChars {
		return auth.IssuedAPIToken{}, badRequest("name is too long")
	}
	if strings.ContainsFunc(name, unicode.IsControl) {
		return auth.IssuedAPIToken{}, badRequest("name contains a control character")
	}
	// A user the proxy signed in has no session; anyone else is tied to the one
	// they asked with, so that ending it also ends their right to make a token.
	sessionToken := ""
	if !user.ViaProxy {
		sessionToken = a.callerOf(r).token
		if sessionToken == "" {
			return auth.IssuedAPIToken{}, unauthorized()
		}
	}
	request := auth.NewAPIToken{UserID: user.UserID, Name: name, Now: auth.UnixNow(), SessionToken: sessionToken}
	issued, ok, err := a.apiTokens.Create(r.Context(), request)
	if errors.Is(err, auth.ErrCallerGone) {
		return auth.IssuedAPIToken{}, unauthorized()
	}
	if err != nil {
		return auth.IssuedAPIToken{}, internal(err)
	}
	if !ok {
		return auth.IssuedAPIToken{}, conflict(fmt.Sprintf("at most %d API tokens are allowed", auth.MaxAPITokensPerUser))
	}
	// The token itself is never logged.
	a.logger.Info("api token created", slog.Int64("user_id", user.UserID))
	return issued, nil
}

// revokeAPIToken ends one of the caller's own tokens. Another user's id answers
// 404, like a missing one.
func (a *App) revokeAPIToken(ctx context.Context, user auth.SessionUser, id string) *apiError {
	found, err := a.apiTokens.RevokeByID(ctx, user.UserID, id)
	if err != nil {
		return internal(err)
	}
	if !found {
		return notFound()
	}
	a.logger.Info("api token ended by its owner", slog.Int64("user_id", user.UserID))
	return nil
}
