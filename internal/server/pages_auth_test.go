package server

import (
	"html"
	"net/http"
	"strings"
	"testing"
)

// These tests keep what the JSON account routes used to prove, now through
// the forms: the cookie, the lock-out, the identical refusals and the headers.

func TestSetupAcceptsASloppilyTypedToken(t *testing.T) {
	app := awaitingSetupApp(t)
	sloppy := strings.ReplaceAll(strings.ToLower(app.setupDisplay), "-", "+")
	r := app.send(formPost("/setup", "token="+sloppy+"&username=henry&password=correct+horse+battery"))
	mustRedirect(t, r, "/")
}

func TestSetupCanOnlySucceedOnce(t *testing.T) {
	app := awaitingSetupApp(t)
	fields := "token=" + app.setupDisplay + "&username=henry&password=correct+horse+battery"
	mustRedirect(t, app.send(formPost("/setup", fields)), "/")
	again := app.send(formPost("/setup", "token="+app.setupDisplay+"&username=mei&password=correct+horse+battery"))
	assertStatus(t, again, http.StatusConflict)
}

func TestSetupIsClosedWhenTheServerStartedWithUsers(t *testing.T) {
	app := runningApp(t)
	r := app.send(formPost("/setup", "token=00000-00000&username=henry&password=correct+horse+battery"))
	assertStatus(t, r, http.StatusConflict)
}

func TestSetupRejectsBadCredentialsWithoutUsingUpTheToken(t *testing.T) {
	app := awaitingSetupApp(t)
	// More attempts than the limit allows: only a refund keeps them all at 400.
	for attempt := range 8 {
		fields := "token=" + app.setupDisplay + "&username=x&password=correct+horse+battery"
		if attempt%2 == 1 {
			fields = "token=" + app.setupDisplay + "&username=henry&password=short"
		}
		assertStatus(t, app.send(formPost("/setup", fields)), http.StatusBadRequest)
	}
	good := "token=" + app.setupDisplay + "&username=henry&password=correct+horse+battery"
	mustRedirect(t, app.send(formPost("/setup", good)), "/")
}

func TestTheSetupLimitIsPerIP(t *testing.T) {
	app := awaitingSetupApp(t)
	wrong := formPost("/setup", "token=00000-00000&username=henry&password=correct+horse+battery")
	for range setupAttemptsPerWindow {
		assertStatus(t, app.send(wrong), http.StatusForbidden)
	}
	right := formPost("/setup", "token="+app.setupDisplay+"&username=henry&password=correct+horse+battery")
	assertStatus(t, app.send(right), http.StatusTooManyRequests)
	mustRedirect(t, app.send(right.from("198.51.100.7")), "/")
}

func TestLoginSetsAnHTTPOnlyLaxCookie(t *testing.T) {
	app := runningApp(t)
	app.addUser("henry", false)
	r := app.login("Henry", testPassword)
	mustRedirect(t, r, "/")
	want := "bkd_session=" + sessionToken(t, r) + "; Path=/; HttpOnly; SameSite=Lax; Max-Age="
	if cookie := r.headers.Get("Set-Cookie"); !strings.HasPrefix(cookie, want) || strings.Contains(cookie, "Secure") {
		t.Fatalf("cookie = %q", cookie)
	}
}

func TestSecureCookiesUseTheHostPrefixAndHSTS(t *testing.T) {
	app := newTestApp(t, Config{SecureCookies: true})
	app.addUser("henry", false)
	r := app.login("henry", testPassword)
	cookie := r.headers.Get("Set-Cookie")
	if !strings.HasPrefix(cookie, "__Host-bkd_session=") || !strings.HasSuffix(cookie, "; Secure") {
		t.Fatalf("cookie = %q", cookie)
	}
	if r.headers.Get("Strict-Transport-Security") == "" {
		t.Fatal("no HSTS")
	}
}

func TestEveryLoginFailureLooksTheSame(t *testing.T) {
	app := runningApp(t)
	app.addUser("henry", false)
	app.addUser("mei", false)
	app.exec("UPDATE users SET disabled_at = 1 WHERE username = 'mei'")
	const refusal = "invalid username or password"
	var firstPage string
	for _, attempt := range [][2]string{
		{"henry", "not the password"}, {"nobody", testPassword}, {"mei", testPassword}, {"not a name!", testPassword},
	} {
		r := app.login(attempt[0], attempt[1])
		assertStatus(t, r, http.StatusUnauthorized)
		mustContain(t, r, refusal)
		if r.headers.Get("Set-Cookie") != "" {
			t.Fatalf("%q got a cookie", attempt[0])
		}
		// The page echoes the typed name; with it removed, no page may differ.
		page := strings.ReplaceAll(r.text, html.EscapeString(attempt[0]), "USER")
		if firstPage == "" {
			firstPage = page
		}
		if page != firstPage {
			t.Errorf("%q got a different page from %q", attempt[0], "henry")
		}
	}
}

func TestRepeatedFailuresLockOutTheIPEvenForTheRightPassword(t *testing.T) {
	app := runningApp(t)
	app.addUser("henry", false)
	for range loginAttemptsPerWindow {
		assertStatus(t, app.login("henry", "wrong"), http.StatusUnauthorized)
	}
	blocked := app.login("henry", testPassword)
	assertStatus(t, blocked, http.StatusTooManyRequests)
	if blocked.headers.Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
}

func TestASuccessfulLoginDoesNotEraseEarlierFailures(t *testing.T) {
	app := runningApp(t)
	app.addUser("henry", false)
	for range loginAttemptsPerWindow - 1 {
		app.login("henry", "wrong")
	}
	assertStatus(t, app.login("henry", testPassword), http.StatusSeeOther)
	assertStatus(t, app.login("henry", testPassword), http.StatusSeeOther)
	assertStatus(t, app.login("henry", "wrong"), http.StatusUnauthorized)
	assertStatus(t, app.login("henry", testPassword), http.StatusTooManyRequests)
}

func TestLogoutEndsTheSessionAndClearsTheCookie(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	r := app.send(formPost("/logout", "").as(token))
	mustRedirect(t, r, "/login")
	if !strings.Contains(r.headers.Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("cookie = %q", r.headers.Get("Set-Cookie"))
	}
	assertStatus(t, app.send(get("/settings").as(token)), http.StatusSeeOther)
}

func TestDisablingAUserCutsOffTheExistingSession(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	assertStatus(t, app.send(get("/settings").as(token)), http.StatusOK)
	app.exec("UPDATE users SET disabled_at = 1")
	assertStatus(t, app.send(get("/settings").as(token)), http.StatusSeeOther)
}

func TestChangingThePasswordEndsOtherSessionsAndTheOldPassword(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	other := sessionToken(t, app.login("henry", testPassword))
	r := app.send(formPost("/settings/password", passwordChangeFields(testPassword, "a brand new password", "a brand new password")).as(token))
	mustRedirect(t, r, "/settings?notice=password-changed")
	fresh := sessionToken(t, r)
	for _, old := range []string{token, other} {
		assertStatus(t, app.send(get("/settings").as(old)), http.StatusSeeOther)
	}
	assertStatus(t, app.send(get("/settings").as(fresh)), http.StatusOK)
	assertStatus(t, app.login("henry", testPassword), http.StatusUnauthorized)
	assertStatus(t, app.login("henry", "a brand new password"), http.StatusSeeOther)
}

func TestAnonymousVisitorsCannotChangeAPassword(t *testing.T) {
	app := runningApp(t)
	app.addUser("henry", false)
	r := app.send(formPost("/settings/password", passwordChangeFields(testPassword, "a brand new password", "a brand new password")))
	assertStatus(t, r, http.StatusSeeOther)
	assertStatus(t, app.login("henry", testPassword), http.StatusSeeOther)
}

func TestEveryResponseCarriesTheSecurityHeaders(t *testing.T) {
	app := runningApp(t)
	// Pages keep the referrer inside the site; everything else sends none.
	referrerPolicy := map[string]string{
		"/login":          "same-origin",
		"/nowhere":        "same-origin",
		"/api/bookmarks/": "no-referrer",
		"/api/export":     "no-referrer",
		"/app.js":         "no-referrer",
	}
	for uri, referrer := range referrerPolicy {
		r := app.send(get(uri))
		for name, want := range map[string]string{
			"X-Frame-Options":              "DENY",
			"Cross-Origin-Opener-Policy":   "same-origin",
			"Cross-Origin-Resource-Policy": "same-origin",
			"X-Content-Type-Options":       "nosniff",
			"Referrer-Policy":              referrer,
		} {
			if got := r.headers.Get(name); got != want {
				t.Errorf("%s %s = %q, want %q", uri, name, got, want)
			}
		}
		if r.headers.Get("Permissions-Policy") == "" || r.headers.Get("Strict-Transport-Security") != "" {
			t.Errorf("%s: permissions or hsts wrong", uri)
		}
	}
}

func TestMethodNotAllowedListsTheAllowedMethods(t *testing.T) {
	app := runningApp(t)
	r := app.send(sent{method: "PATCH", uri: "/login"}.originated("http://bkd.test"))
	assertStatus(t, r, http.StatusMethodNotAllowed)
	if r.headers.Get("Allow") != "GET,HEAD,POST" {
		t.Fatalf("allow = %q", r.headers.Get("Allow"))
	}
	if r.text != "" {
		t.Fatalf("body = %q, want empty", r.text)
	}
}
