package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/henry40408/bkd/internal/auth"
)

// sessionFor signs a new non-admin user in without Argon2, which these tests do
// not exercise and which is slow under the race detector. The package has a
// time limit, so cheap set-up counts.
func (a *testApp) sessionFor(username string) string {
	a.t.Helper()
	var id int64
	err := a.db.QueryRow(
		"INSERT INTO users (username, password_hash, is_admin, created_at) VALUES (?1, 'unused', 0, 0) RETURNING id",
		username).Scan(&id)
	if err != nil {
		a.t.Fatal(err)
	}
	token, err := a.app.sessions.Create(context.Background(), auth.SessionStart{UserID: id, Now: auth.UnixNow()})
	if err != nil {
		a.t.Fatal(err)
	}
	return token
}

// createToken makes a token through the settings page and returns its secret.
func (a *testApp) createToken(session, name string) string {
	a.t.Helper()
	r := a.send(formPost("/settings/tokens", "name="+url.QueryEscape(name)).as(session))
	assertStatus(a.t, r, http.StatusOK)
	return secretIn(a.t, r)
}

func (a *testApp) tokenCount(username string) int {
	a.t.Helper()
	var count int
	query := "SELECT count(*) FROM api_tokens WHERE user_id = (SELECT id FROM users WHERE username = ?1)"
	if err := a.db.QueryRow(query, username).Scan(&count); err != nil {
		a.t.Fatal(err)
	}
	return count
}

func TestATokenNameIsTrimmedAndStored(t *testing.T) {
	app := runningApp(t)
	session := app.sessionFor("henry")
	secret := app.createToken(session, "  extension ")
	sum := sha256.Sum256([]byte(secret))
	var name string
	if err := app.db.QueryRow("SELECT name FROM api_tokens WHERE token_hash = ?1", hex.EncodeToString(sum[:])).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "extension" {
		t.Fatalf("name = %q", name)
	}
}

// The session check runs before the body is read, and a slow body can outlast a
// reset. The service therefore asks again at the insert.
func TestNoTokenIsMadeWhenTheSessionEndedAfterTheRequestWasAccepted(t *testing.T) {
	app := runningApp(t)
	session := app.sessionFor("henry")
	var id int64
	if err := app.db.QueryRow("SELECT id FROM users WHERE username = 'henry'").Scan(&id); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/settings/tokens", nil)
	request.Header.Set("Cookie", app.app.cookieName()+"="+session)
	user := auth.SessionUser{UserID: id, Username: "henry"}
	if _, fault := app.app.createAPIToken(request, user, "ok"); fault != nil {
		t.Fatalf("with a live session: %v", fault)
	}
	if _, err := app.db.Exec("DELETE FROM sessions WHERE user_id = ?1", id); err != nil {
		t.Fatal(err)
	}
	_, fault := app.app.createAPIToken(request, user, "late")
	if fault == nil || fault.status != http.StatusUnauthorized {
		t.Fatalf("after the session ended: %v", fault)
	}
	if got := app.tokenCount("henry"); got != 1 {
		t.Fatalf("tokens = %d", got)
	}
}

func TestACookieUserWithoutACookieCannotMakeATokenInTheService(t *testing.T) {
	app := runningApp(t)
	app.sessionFor("henry")
	request := httptest.NewRequest(http.MethodPost, "/settings/tokens", nil)
	_, fault := app.app.createAPIToken(request, auth.SessionUser{UserID: 1, Username: "henry"}, "x")
	if fault == nil || fault.status != http.StatusUnauthorized {
		t.Fatalf("fault = %v", fault)
	}
}

// The secret must not be kept by a browser or a cache.
func TestTheAnswerThatCarriesASecretIsNotCached(t *testing.T) {
	app := runningApp(t)
	session := app.sessionFor("henry")
	page := app.send(formPost("/settings/tokens", "name=b").as(session))
	if got := page.headers.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
}

func TestTokenFormsNeedASession(t *testing.T) {
	app := runningApp(t)
	for _, request := range []sent{
		formPost("/settings/tokens", "name=a"),
		formPost("/settings/tokens/0123456789abcdef/revoke", ""),
	} {
		mustRedirect(t, app.send(request), "/login")
	}
}

func TestAcceptedTokenNamesAtTheLimit(t *testing.T) {
	app := runningApp(t)
	session := app.sessionFor("henry")
	for _, name := range []string{strings.Repeat("a", maxTokenNameChars), strings.Repeat("語", maxTokenNameChars)} {
		app.createToken(session, name)
	}
}

func TestRefusedTokenNamesAreExplained(t *testing.T) {
	app := runningApp(t)
	session := app.sessionFor("henry")
	cases := []struct{ name, error string }{
		{"", "name is required"},
		{" \t ", "name is required"},
		{strings.Repeat("a", maxTokenNameChars+1), "name is too long"},
		{strings.Repeat("語", maxTokenNameChars+1), "name is too long"},
		{"line\nbreak", "name contains a control character"},
	}
	for _, c := range cases {
		r := app.send(formPost("/settings/tokens", "name="+url.QueryEscape(c.name)).as(session))
		assertStatus(t, r, http.StatusBadRequest)
		mustContain(t, r, c.error)
	}
	if got := app.tokenCount("henry"); got != 0 {
		t.Fatalf("a refused name made %d tokens", got)
	}
}

func TestAnotherUsersTokenCannotBeEnded(t *testing.T) {
	app := runningApp(t)
	mine := app.sessionFor("henry")
	theirs := app.sessionFor("mei")
	app.createToken(mine, "a")
	var id string
	if err := app.db.QueryRow("SELECT substr(token_hash, 1, 16) FROM api_tokens").Scan(&id); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, app.send(formPost("/settings/tokens/"+id+"/revoke", "").as(theirs)), http.StatusNotFound)
	if got := app.tokenCount("henry"); got != 1 {
		t.Fatalf("the owner's tokens = %d", got)
	}
}

func TestUserTheProxySignedInMayHaveTokens(t *testing.T) {
	app := newTestApp(t, forwardAuthConfig(t))
	r := app.send(viaProxy(formPost("/settings/tokens", "name=script"), "alice"))
	assertStatus(t, r, http.StatusOK)
	if got := app.tokenCount("alice"); got != 1 {
		t.Fatalf("tokens = %d", got)
	}
}

func tokenRevokeAction(t *testing.T, page reply) string {
	t.Helper()
	end := strings.Index(page.text, `/revoke"`)
	start := strings.LastIndex(page.text[:max(end, 0)], "/settings/tokens/")
	if end < 0 || start < 0 {
		t.Fatal("no revoke button for a token")
	}
	return page.text[start:end] + "/revoke"
}

func TestSettingsPageCreatesATokenAndShowsItsSecretOnce(t *testing.T) {
	app := runningApp(t)
	session := app.sessionFor("henry")
	mustContain(t, app.send(get("/settings").as(session)), "API tokens", "Create token")

	made := app.send(formPost("/settings/tokens", "name=Browser+extension").as(session))
	assertStatus(t, made, http.StatusOK)
	mustContain(t, made, "Copy this token now", "Browser extension", "never used")
	secret := secretIn(t, made)
	if len(secret) != 64 {
		t.Fatalf("secret = %q", secret)
	}

	again := app.send(get("/settings").as(session))
	mustContain(t, again, "Browser extension")
	mustLack(t, again, secret)
	mustLack(t, again, "Copy this token now")
}

func secretIn(t *testing.T, page reply) string {
	t.Helper()
	marker := `aria-label="New API token" value="`
	start := strings.Index(page.text, marker)
	if start < 0 {
		t.Fatal("no new token on the page")
	}
	rest := page.text[start+len(marker):]
	secret, _, _ := strings.Cut(rest, `"`)
	return secret
}

func TestSecretFromTheSettingsPageIsTheOneStored(t *testing.T) {
	app := runningApp(t)
	session := app.sessionFor("henry")
	made := app.send(formPost("/settings/tokens", "name=a").as(session))
	secret := secretIn(t, made)
	sum := sha256.Sum256([]byte(secret))
	hash := hex.EncodeToString(sum[:])
	var count int
	if err := app.db.QueryRow("SELECT count(*) FROM api_tokens WHERE token_hash = ?1", hash).Scan(&count); err != nil || count != 1 {
		t.Fatalf("stored = %d, %v", count, err)
	}
}

func TestSettingsPageEndsATokenAndSaysSo(t *testing.T) {
	app := runningApp(t)
	session := app.sessionFor("henry")
	app.send(formPost("/settings/tokens", "name=a").as(session))
	page := app.send(get("/settings").as(session))
	ended := app.send(formPost(tokenRevokeAction(t, page), "").as(session))
	mustRedirect(t, ended, "/settings?notice=token-revoked")
	after := app.send(get(locationOf(ended)).as(session))
	mustContain(t, after, "That token was ended.")
	mustLack(t, after, "/revoke\"")
	assertStatus(t, app.send(formPost("/settings/tokens/0123456789abcdef/revoke", "").as(session)), http.StatusNotFound)
}

func TestRefusedTokenNameShowsTheFormAgainWithWhatWasTyped(t *testing.T) {
	app := runningApp(t)
	session := app.sessionFor("henry")
	tooLong := strings.Repeat("a", maxTokenNameChars+1)
	r := app.send(formPost("/settings/tokens", "name="+tooLong).as(session))
	assertStatus(t, r, http.StatusBadRequest)
	mustContain(t, r, `role="alert"`, "name is too long", `value="`+tooLong+`"`)
	mustLack(t, r, "Copy this token now")

	blank := app.send(formPost("/settings/tokens", "name=+").as(session))
	assertStatus(t, blank, http.StatusBadRequest)
	mustContain(t, blank, "name is required")
}

func TestSettingsPageRefusesATokenAtTheLimit(t *testing.T) {
	app := runningApp(t)
	session := app.sessionFor("henry")
	for range auth.MaxAPITokensPerUser {
		app.createToken(session, "a")
	}
	r := app.send(formPost("/settings/tokens", "name=one+too+many").as(session))
	assertStatus(t, r, http.StatusConflict)
	mustContain(t, r, "at most 20 API tokens are allowed", "one too many")
	mustLack(t, r, "Copy this token now")
}

func TestTokenFormNeedsAnameFieldAndTheSameOrigin(t *testing.T) {
	app := runningApp(t)
	session := app.sessionFor("henry")
	assertStatus(t, app.send(formPost("/settings/tokens", "other=1").as(session)), http.StatusUnprocessableEntity)
	foreign := formPost("/settings/tokens", "name=a").as(session).originated("https://evil.example")
	assertStatus(t, app.send(foreign), http.StatusForbidden)
	assertStatus(t, app.send(formPost("/settings/tokens", "name=a")), http.StatusSeeOther)
}

func TestTokenPagesAnswer500WhenTheTokenTableFails(t *testing.T) {
	app := runningApp(t)
	session := app.sessionFor("henry")
	app.breakTable("api_tokens")
	cases := []sent{
		get("/settings"),
		formPost("/settings/tokens", "name=a"),
		formPost("/settings/tokens/0123456789abcdef/revoke", ""),
	}
	for _, c := range cases {
		r := app.send(c.as(session))
		if r.status != http.StatusInternalServerError {
			t.Errorf("%s %s: status = %d, want 500; body %q", c.method, c.uri, r.status, r.text)
		}
		if strings.Contains(r.text, "api_tokens") {
			t.Errorf("%s %s shows the cause: %q", c.method, c.uri, r.text)
		}
	}
}

func TestResettingAPasswordEndsTheUsersTokens(t *testing.T) {
	app, admin, member := adminWithMember(t)
	var memberTokens int
	app.exec("INSERT INTO api_tokens (token_hash, user_id, name, created_at) VALUES ('h', ?1, 'a', 0)", member)
	uri := fmt.Sprintf("/users/%d/password", member)
	mustRedirect(t, app.send(formPost(uri, "password=a+brand+new+secret").as(admin)), "/users?notice=user-changed")
	if err := app.db.QueryRow("SELECT count(*) FROM api_tokens WHERE user_id = ?1", member).Scan(&memberTokens); err != nil || memberTokens != 0 {
		t.Fatalf("tokens left = %d, %v", memberTokens, err)
	}
}
