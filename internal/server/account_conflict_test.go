package server

import (
	"net/http"
	"strings"
	"testing"
)

// A write the database silently skips (RAISE(IGNORE)) stands for a change that
// lost a race: the account changed, or another request won. The route must
// answer with a refusal, never with a session or a success.
func (a *testApp) skipChanges(table, event string) {
	a.t.Helper()
	a.exec("CREATE TRIGGER skip_" + table + "_" + event + " BEFORE " + event + " ON " + table +
		" BEGIN SELECT RAISE(IGNORE); END")
}

func TestPasswordChangeLosingARaceIsRefusedWithoutANewSession(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("alice", false)
	app.skipChanges("users", "UPDATE")

	r := app.send(put("/api/account/password",
		map[string]any{"current": testPassword, "new": "a brand new secret"}).as(token))

	assertStatus(t, r, http.StatusConflict)
	if cookie := r.headers.Get("Set-Cookie"); cookie != "" {
		t.Errorf("a refused change set a cookie: %q", cookie)
	}
	assertStatus(t, app.login("alice", testPassword), http.StatusOK)
}

func TestPasswordChangeThatCannotStartASessionAsksToSignInAgain(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("alice", false)
	app.skipChanges("sessions", "INSERT")

	r := app.send(put("/api/account/password",
		map[string]any{"current": testPassword, "new": "a brand new secret"}).as(token))

	assertStatus(t, r, http.StatusConflict)
	if !strings.Contains(r.text, "sign in again") {
		t.Errorf("body = %q, want it to ask for a new sign-in", r.text)
	}
}

func TestLoginWhoseSessionIsNotStoredIsRefusedAsInvalidCredentials(t *testing.T) {
	app := runningApp(t)
	app.addUser("alice", false)
	app.skipChanges("sessions", "INSERT")

	r := app.login("alice", testPassword)

	assertStatus(t, r, http.StatusUnauthorized)
	if cookie := r.headers.Get("Set-Cookie"); cookie != "" {
		t.Errorf("a refused login set a cookie: %q", cookie)
	}
}

func TestSetupThatLosesTheRaceForTheFirstAdminIsRefused(t *testing.T) {
	app := awaitingSetupApp(t)
	app.skipChanges("users", "INSERT")

	r := app.send(post("/api/setup", setupBody(app, app.setupDisplay, "henry", testPassword)))

	assertStatus(t, r, http.StatusConflict)
	if !strings.Contains(r.text, "setup is already complete") {
		t.Errorf("body = %q", r.text)
	}
}

func TestASingleSignOnUserCannotConfirmOrChangeAPassword(t *testing.T) {
	app := newTestApp(t, forwardAuthConfig(t))
	confirm := viaProxy(post("/api/account/confirm", map[string]any{"password": testPassword}), "alice")
	change := viaProxy(put("/api/account/password",
		map[string]any{"current": testPassword, "new": "a brand new secret"}), "alice")

	for _, request := range []sent{confirm, change} {
		r := app.send(request)
		assertStatus(t, r, http.StatusForbidden)
		if !strings.Contains(r.text, "single sign-on") {
			t.Errorf("%s: body = %q", request.uri, r.text)
		}
	}
}

func TestConfirmAndPasswordChangeShareTheLoginRateLimit(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("alice", false)
	wrong := post("/api/account/confirm", map[string]any{"password": "not the password"}).as(token)

	var last reply
	for range 12 {
		last = app.send(wrong)
		if last.status == http.StatusTooManyRequests {
			break
		}
	}

	assertStatus(t, last, http.StatusTooManyRequests)
	change := put("/api/account/password", map[string]any{"current": testPassword, "new": "a brand new secret"}).as(token)
	assertStatus(t, app.send(change), http.StatusTooManyRequests)
}
