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

	r := app.send(formPost("/settings/password", passwordChangeFields(testPassword, "a brand new secret", "a brand new secret")).as(token))

	assertStatus(t, r, http.StatusConflict)
	if cookie := r.headers.Get("Set-Cookie"); cookie != "" {
		t.Errorf("a refused change set a cookie: %q", cookie)
	}
	assertStatus(t, app.login("alice", testPassword), http.StatusSeeOther)
}

func TestPasswordChangeThatCannotStartASessionAsksToSignInAgain(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("alice", false)
	app.skipChanges("sessions", "INSERT")

	r := app.send(formPost("/settings/password", passwordChangeFields(testPassword, "a brand new secret", "a brand new secret")).as(token))

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

	r := app.send(formPost("/setup", "token="+app.setupDisplay+"&username=henry&password=correct+horse+battery"))

	assertStatus(t, r, http.StatusConflict)
	if !strings.Contains(r.text, "setup is already complete") {
		t.Errorf("body = %q", r.text)
	}
}

func TestConfirmAndPasswordChangeShareTheLoginRateLimit(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("alice", false)
	wrong := formPost("/confirm", "password=not+the+password").as(token)

	var last reply
	for range 12 {
		last = app.send(wrong)
		if last.status == http.StatusTooManyRequests {
			break
		}
	}

	assertStatus(t, last, http.StatusTooManyRequests)
	assertStatus(t, app.send(formPost("/settings/password", passwordChangeFields(testPassword, "a brand new secret", "a brand new secret")).as(token)), http.StatusTooManyRequests)
}

func TestSignInAndSetupAnswer500WhenTheUserTableFails(t *testing.T) {
	app := awaitingSetupApp(t)
	app.breakTable("users")
	expectEvery500(t, app, "", []sent{
		formPost("/login", "username=alice&password=correct+horse+battery"),
		formPost("/setup", "token="+app.setupDisplay+"&username=henry&password=correct+horse+battery"),
	})
}

func TestLoggingOutWithoutASessionIsAQuietSuccess(t *testing.T) {
	app := runningApp(t)

	assertStatus(t, app.send(formPost("/logout", "")), http.StatusSeeOther)
}

func TestASingleSignOnUserIsRefusedTheRoutesThatNeedAPassword(t *testing.T) {
	app := newTestApp(t, forwardAuthConfig(t))
	passwords := passwordChangeFields(testPassword, "a brand new secret", "a brand new secret")
	mismatch := passwordChangeFields(testPassword, "a brand new secret", "another new secret")
	for _, request := range []sent{
		formPost("/settings/password", passwords),
		formPost("/settings/password", mismatch),
		formPost("/settings/sessions/revoke-others", ""),
		formPost("/settings/sessions/0123456789abcdef/revoke", ""),
		formPost("/confirm", "password=correct+horse+battery"),
	} {
		r := app.send(viaProxy(request, "alice"))
		if r.status != http.StatusForbidden || !strings.Contains(r.text, "single sign-on") {
			t.Errorf("%s %s: status = %d, body %q", request.method, request.uri, r.status, r.text)
		}
	}
}

func TestASingleSignOnUserSeesTheSettingsWithoutSessionsAndPassword(t *testing.T) {
	app := newTestApp(t, forwardAuthConfig(t))

	r := app.send(viaProxy(get("/settings"), "alice"))

	assertStatus(t, r, http.StatusOK)
	mustContain(t, r, "<h2>Link check</h2>", "<h2>User-Agent</h2>", "<h2>API tokens</h2>")
	for _, unwanted := range []string{"<h2>Sessions</h2>", "<h2>Change password</h2>", "/settings/password", "/settings/sessions/", `href="/account"`} {
		mustLack(t, r, unwanted)
	}
}

func TestASingleSignOnUserFollowsTheOldAccountAddressToTheSettings(t *testing.T) {
	app := newTestApp(t, forwardAuthConfig(t))

	mustRedirect(t, app.send(viaProxy(get("/account"), "alice")), "/settings")
}
