package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/henry40408/bkd/internal/auth"
)

const (
	cookieName = "bkd_session"
	// The __Host- prefix makes a browser refuse a cookie set by a sibling subdomain
	// or with a Domain attribute, which would otherwise let another site's owner
	// plant a session on this one. Browsers require Secure for it, so it applies
	// only then.
	secureCookieName = "__Host-bkd_session"
)

func (a *App) cookieName() string {
	if a.secureCookies {
		return secureCookieName
	}
	return cookieName
}

// readToken finds the session token among the request's cookies. It reads every
// Cookie header, because a client may send several.
func (a *App) readToken(r *http.Request) (string, bool) {
	wanted := a.cookieName()
	for _, header := range r.Header.Values("Cookie") {
		for pair := range strings.SplitSeq(header, ";") {
			name, value, found := strings.Cut(strings.TrimSpace(pair), "=")
			if found && name == wanted {
				return value, true
			}
		}
	}
	return "", false
}

// sessionCookie is HttpOnly so scripts cannot read the token. SameSite=Lax keeps the
// browser from sending it on a POST or a script request that starts on another
// site. A link followed from another site still carries it, so a shared bkd link
// opens signed in. It is written by hand: http.Cookie orders and spells the
// attributes differently from what the Rust server sent.
func (a *App) sessionCookie(token string) string {
	return a.buildCookie(token, auth.SessionLifetimeSeconds)
}

func (a *App) clearedCookie() string {
	return a.buildCookie("", 0)
}

func (a *App) buildCookie(value string, maxAge int64) string {
	cookie := a.cookieName() + "=" + value + "; Path=/; HttpOnly; SameSite=Lax; Max-Age=" +
		strconv.FormatInt(maxAge, 10)
	if a.secureCookies {
		cookie += "; Secure"
	}
	return cookie
}
