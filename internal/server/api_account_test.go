package server

import (
	"net/http"
	"strings"
	"testing"
)

func setupBody(app *testApp, token, username, password string) map[string]any {
	return map[string]any{"token": token, "username": username, "password": password}
}

func TestStateReportsThatSetupIsNeeded(t *testing.T) {
	app := awaitingSetupApp(t)
	r := app.send(get("/api/state"))
	assertStatus(t, r, http.StatusOK)
	if r.text != `{"setup_required":true,"user":null}` {
		t.Fatalf("body = %s", r.text)
	}
}

func TestSetupCreatesASignedInAdmin(t *testing.T) {
	app := awaitingSetupApp(t)
	r := app.send(post("/api/setup", setupBody(app, app.setupDisplay, "Henry", testPassword)))
	assertStatus(t, r, http.StatusCreated)
	state := app.send(get("/api/state").as(sessionToken(t, r))).json()
	user := state["user"].(map[string]any)
	if state["setup_required"] != false || user["username"] != "henry" || user["is_admin"] != true {
		t.Fatalf("state = %v", state)
	}
}

func TestSetupAcceptsASloppilyTypedToken(t *testing.T) {
	app := awaitingSetupApp(t)
	sloppy := strings.ReplaceAll(strings.ToLower(app.setupDisplay), "-", " ")
	r := app.send(post("/api/setup", setupBody(app, sloppy, "henry", testPassword)))
	assertStatus(t, r, http.StatusCreated)
}

func TestSetupWithAWrongTokenIsRefused(t *testing.T) {
	app := awaitingSetupApp(t)
	r := app.send(post("/api/setup", setupBody(app, "00000-00000", "henry", testPassword)))
	assertStatus(t, r, http.StatusForbidden)
	if r.headers.Get("Set-Cookie") != "" {
		t.Fatal("a refused setup set a cookie")
	}
	if !strings.Contains(app.send(get("/api/state")).text, `"setup_required":true`) {
		t.Fatal("setup closed")
	}
}

func TestSetupCanOnlySucceedOnce(t *testing.T) {
	app := awaitingSetupApp(t)
	assertStatus(t, app.send(post("/api/setup", setupBody(app, app.setupDisplay, "henry", testPassword))), http.StatusCreated)
	r := app.send(post("/api/setup", setupBody(app, app.setupDisplay, "mei", testPassword)))
	assertStatus(t, r, http.StatusConflict)
}

func TestSetupIsClosedWhenTheServerStartedWithUsers(t *testing.T) {
	app := runningApp(t)
	r := app.send(post("/api/setup", setupBody(app, "00000-00000", "henry", testPassword)))
	assertStatus(t, r, http.StatusConflict)
}

func TestSetupRejectsBadCredentialsWithoutUsingUpTheToken(t *testing.T) {
	app := awaitingSetupApp(t)
	// More attempts than the limit allows: only a refund keeps them all at 400.
	for attempt := range 8 {
		username, password := "x", testPassword
		if attempt%2 == 1 {
			username, password = "henry", "short"
		}
		r := app.send(post("/api/setup", setupBody(app, app.setupDisplay, username, password)))
		assertStatus(t, r, http.StatusBadRequest)
	}
	assertStatus(t, app.send(post("/api/setup", setupBody(app, app.setupDisplay, "henry", testPassword))), http.StatusCreated)
}

func TestGuessingTheSetupTokenIsRateLimitedPerIP(t *testing.T) {
	app := awaitingSetupApp(t)
	wrong := setupBody(app, "00000-00000", "henry", testPassword)
	for range setupAttemptsPerWindow {
		assertStatus(t, app.send(post("/api/setup", wrong)), http.StatusForbidden)
	}
	blocked := app.send(post("/api/setup", wrong))
	assertStatus(t, blocked, http.StatusTooManyRequests)
	if blocked.headers.Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
	right := setupBody(app, app.setupDisplay, "henry", testPassword)
	assertStatus(t, app.send(post("/api/setup", right)), http.StatusTooManyRequests)
	assertStatus(t, app.send(post("/api/setup", right).from("198.51.100.7")), http.StatusCreated)
}

func TestLoginSetsAnHTTPOnlyLaxCookie(t *testing.T) {
	app := runningApp(t)
	app.addUser("henry", false)
	r := app.login("Henry", testPassword)
	assertStatus(t, r, http.StatusOK)
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
	if _, err := app.db.Exec("UPDATE users SET disabled_at = 1 WHERE username = 'mei'"); err != nil {
		t.Fatal(err)
	}
	first := app.login("henry", "not the password")
	for _, r := range []reply{first, app.login("nobody", testPassword), app.login("mei", testPassword), app.login("not a name!", testPassword)} {
		assertStatus(t, r, http.StatusUnauthorized)
		if r.text != first.text || r.headers.Get("Set-Cookie") != "" {
			t.Fatalf("distinguishable: %q", r.text)
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
	assertStatus(t, app.login("henry", testPassword), http.StatusOK)
	assertStatus(t, app.login("henry", testPassword), http.StatusOK)
	assertStatus(t, app.login("henry", "wrong"), http.StatusUnauthorized)
	assertStatus(t, app.login("henry", testPassword), http.StatusTooManyRequests)
}

func TestLogoutEndsTheSessionAndClearsTheCookie(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	r := app.send(post("/api/logout", map[string]any{}).as(token))
	assertStatus(t, r, http.StatusNoContent)
	if !strings.Contains(r.headers.Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("cookie = %q", r.headers.Get("Set-Cookie"))
	}
	assertStatus(t, app.send(get("/api/bookmarks").as(token)), http.StatusUnauthorized)
}

func TestDisablingAUserCutsOffTheExistingSession(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	if _, err := app.db.Exec("UPDATE users SET disabled_at = 1"); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, app.send(get("/api/bookmarks").as(token)), http.StatusUnauthorized)
}

func (a *testApp) changePassword(token, current, next string) reply {
	return a.send(put("/api/account/password", map[string]any{"current": current, "new": next}).as(token))
}

func TestChangingThePasswordEndsOtherSessionsAndKeepsThisOne(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	other := sessionToken(t, app.login("henry", testPassword))
	r := app.changePassword(token, testPassword, "a brand new password")
	assertStatus(t, r, http.StatusNoContent)
	fresh := sessionToken(t, r)
	for _, old := range []string{token, other} {
		assertStatus(t, app.send(get("/api/bookmarks").as(old)), http.StatusUnauthorized)
	}
	assertStatus(t, app.send(get("/api/bookmarks").as(fresh)), http.StatusOK)
	assertStatus(t, app.login("henry", testPassword), http.StatusUnauthorized)
	assertStatus(t, app.login("henry", "a brand new password"), http.StatusOK)
}

func TestChangingThePasswordNeedsTheCurrentOneAndAValidNewOne(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	assertStatus(t, app.changePassword(token, "not the password", "a brand new password"), http.StatusForbidden)
	for _, bad := range []string{"short", "henry is my name", testPassword} {
		assertStatus(t, app.changePassword(token, testPassword, bad), http.StatusBadRequest)
	}
	anonymous := app.send(put("/api/account/password", map[string]any{"current": testPassword, "new": "a brand new password"}))
	assertStatus(t, anonymous, http.StatusUnauthorized)
	assertStatus(t, app.send(get("/api/bookmarks").as(token)), http.StatusOK)
}

func TestGuessingTheCurrentPasswordIsRateLimited(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	for range loginAttemptsPerWindow {
		assertStatus(t, app.changePassword(token, "not the password", "a brand new password"), http.StatusForbidden)
	}
	assertStatus(t, app.changePassword(token, testPassword, "a brand new password"), http.StatusTooManyRequests)
}

func TestSessionsCanBeListedAndRevoked(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	other := sessionToken(t, app.login("henry", testPassword))
	list := app.send(get("/api/account/sessions").as(token)).jsonList()
	if len(list) != 2 {
		t.Fatalf("sessions = %v", list)
	}
	var otherID string
	for _, item := range list {
		session := item.(map[string]any)
		if session["is_current"] == false {
			otherID = session["id"].(string)
		}
	}
	assertStatus(t, app.send(del("/api/account/sessions/"+otherID).as(token)), http.StatusNoContent)
	assertStatus(t, app.send(get("/api/bookmarks").as(other)), http.StatusUnauthorized)
	assertStatus(t, app.send(del("/api/account/sessions/"+otherID).as(token)), http.StatusNotFound)
}

func TestRevokingOtherSessionsKeepsTheCurrentOne(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	other := sessionToken(t, app.login("henry", testPassword))
	assertStatus(t, app.send(del("/api/account/sessions").as(token)), http.StatusNoContent)
	assertStatus(t, app.send(get("/api/bookmarks").as(token)), http.StatusOK)
	assertStatus(t, app.send(get("/api/bookmarks").as(other)), http.StatusUnauthorized)
}

func TestConfirmingThePasswordNeedsTheRightOne(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", true)
	wrong := app.send(post("/api/account/confirm", map[string]any{"password": "nope"}).as(token))
	assertStatus(t, wrong, http.StatusForbidden)
	right := app.send(post("/api/account/confirm", map[string]any{"password": testPassword}).as(token))
	assertStatus(t, right, http.StatusNoContent)
}

func TestEveryResponseCarriesTheSecurityHeaders(t *testing.T) {
	app := runningApp(t)
	for _, uri := range []string{"/api/state", "/api/bookmarks", "/app.js"} {
		r := app.send(get(uri))
		for name, want := range map[string]string{
			"X-Frame-Options":              "DENY",
			"Cross-Origin-Opener-Policy":   "same-origin",
			"Cross-Origin-Resource-Policy": "same-origin",
			"X-Content-Type-Options":       "nosniff",
			"Referrer-Policy":              "no-referrer",
		} {
			if got := r.headers.Get(name); got != want {
				t.Errorf("%s %s = %q", uri, name, got)
			}
		}
		if r.headers.Get("Permissions-Policy") == "" || r.headers.Get("Strict-Transport-Security") != "" {
			t.Errorf("%s: permissions or hsts wrong", uri)
		}
	}
}

func TestStateChangingRequestsFromAnotherOriginAreRefused(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	cross := post("/api/bookmarks", bookmarkBody("https://a.b", "t")).as(token)
	assertStatus(t, app.send(cross.originated("https://evil.test")), http.StatusForbidden)
	assertStatus(t, app.send(cross.originated("http://bkd.test")), http.StatusCreated)
}

func TestMethodNotAllowedListsTheAllowedMethods(t *testing.T) {
	app := runningApp(t)
	r := app.send(sent{method: "PATCH", uri: "/api/bookmarks"})
	assertStatus(t, r, http.StatusMethodNotAllowed)
	if r.headers.Get("Allow") != "GET,HEAD,POST" || r.text != "" {
		t.Fatalf("allow = %q body = %q", r.headers.Get("Allow"), r.text)
	}
}

func TestBodyRejectionsFollowAxum(t *testing.T) {
	app := runningApp(t)
	login := func(contentType, body string) reply {
		return app.send(sent{method: "POST", uri: "/api/login"}.withRaw(contentType, body))
	}
	assertStatus(t, login("text/plain", "{}"), http.StatusUnsupportedMediaType)
	assertStatus(t, login("application/json", "{"), http.StatusBadRequest)
	assertStatus(t, login("application/json", `{"username":"a"}`), http.StatusUnprocessableEntity)
	assertStatus(t, login("application/json", strings.Repeat("a", maxBodyBytes+1)), http.StatusRequestEntityTooLarge)
}
