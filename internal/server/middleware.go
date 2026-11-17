package server

import (
	"net/http"
	"strings"
)

// securityHeaders sets what every response carries, API and static files alike.
// A handler that wants another value (a page sets its own Referrer-Policy) just
// overwrites it. HSTS is sent only when the operator says the site is served over
// HTTPS: on plain HTTP browsers ignore it.
func (a *App) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Frame-Options", "DENY")
		header.Set("Referrer-Policy", "no-referrer")
		header.Set("Cross-Origin-Opener-Policy", "same-origin")
		header.Set("Cross-Origin-Resource-Policy", "same-origin")
		header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		if a.secureCookies {
			header.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

// apiHeaders keeps API responses out of shared caches and stops the browser from
// guessing types.
func apiHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("Cache-Control", "no-store")
		header.Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// sameOriginOnly refuses a state-changing request whose Origin names another site.
// Together with SameSite=Lax and the JSON-only bodies, this closes cross-site
// request forgery even for a browser that ignores SameSite. It is not
// http.CrossOriginProtection: that one trusts Sec-Fetch-Site and has other rules
// for a missing Origin.
func (a *App) sameOriginOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isSafeMethod(r.Method) && !originMatchesHost(r) {
			a.writeAPIError(w, forbidden("cross-site request refused"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isSafeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// originMatchesHost: a request without Origin (a command-line client) passes. It
// cannot be a browser acting on a logged-in user's behalf, because browsers send
// Origin on these.
func originMatchesHost(r *http.Request) bool {
	origin, sent := r.Header["Origin"]
	if !sent {
		return true
	}
	host := r.Host
	if host == "" {
		return false
	}
	_, authority, found := strings.Cut(origin[0], "://")
	return found && authority == host
}

// limitBody caps what a handler can read. Reading past it fails with
// *http.MaxBytesError, which the body readers turn into 413.
func limitBody(limit int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}
