package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func (a *testApp) createToken(session, name string) map[string]any {
	a.t.Helper()
	r := a.send(post("/api/account/tokens", map[string]any{"name": name}).as(session))
	assertStatus(a.t, r, http.StatusCreated)
	return r.json()
}

func TestTokenIsCreatedListedAndEndedThroughTheAPI(t *testing.T) {
	app := runningApp(t)
	session := app.sessionFor("henry")
	created := app.createToken(session, "  extension ")
	secret, _ := created["token"].(string)
	if len(secret) != 64 || created["name"] != "extension" || created["last_used_at"] != nil {
		t.Fatalf("created = %v", created)
	}

	list := app.send(get("/api/account/tokens").as(session))
	assertStatus(t, list, http.StatusOK)
	if strings.Contains(list.text, secret) {
		t.Fatal("the list shows the secret")
	}
	items := list.jsonList()
	if len(items) != 1 || items[0].(map[string]any)["id"] != created["id"] {
		t.Fatalf("list = %s", list.text)
	}

	id, _ := created["id"].(string)
	assertStatus(t, app.send(del("/api/account/tokens/"+id).as(session)), http.StatusNoContent)
	assertStatus(t, app.send(del("/api/account/tokens/"+id).as(session)), http.StatusNotFound)
	if left := app.send(get("/api/account/tokens").as(session)).jsonList(); len(left) != 0 {
		t.Fatalf("tokens left: %v", left)
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
	request := httptest.NewRequest(http.MethodPost, "/api/account/tokens", nil)
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
	if got := app.send(get("/api/account/tokens").as(app.sessionFor("other"))).jsonList(); len(got) != 0 {
		t.Fatalf("another user sees %v", got)
	}
	var held int
	if err := app.db.QueryRow("SELECT count(*) FROM api_tokens WHERE user_id = ?1", id).Scan(&held); err != nil || held != 1 {
		t.Fatalf("tokens = %d, %v", held, err)
	}
}

func TestACookieUserWithoutACookieCannotMakeATokenInTheService(t *testing.T) {
	app := runningApp(t)
	app.sessionFor("henry")
	request := httptest.NewRequest(http.MethodPost, "/api/account/tokens", nil)
	_, fault := app.app.createAPIToken(request, auth.SessionUser{UserID: 1, Username: "henry"}, "x")
	if fault == nil || fault.status != http.StatusUnauthorized {
		t.Fatalf("fault = %v", fault)
	}
}

// The secret must not be kept by a browser or a cache.
func TestTheAnswersThatCarryASecretAreNotCached(t *testing.T) {
	app := runningApp(t)
	session := app.sessionFor("henry")
	api := app.send(post("/api/account/tokens", map[string]any{"name": "a"}).as(session))
	page := app.send(formPost("/settings/tokens", "name=b").as(session))
	for name, r := range map[string]reply{"api": api, "page": page} {
		if got := r.headers.Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q", name, got)
		}
	}
}

func TestListOfNoTokensIsAnEmptyArray(t *testing.T) {
	app := runningApp(t)
	session := app.sessionFor("henry")
	if r := app.send(get("/api/account/tokens").as(session)); strings.TrimSpace(r.text) != "[]" {
		t.Fatalf("body = %q", r.text)
	}
}

func TestTokenRoutesNeedASession(t *testing.T) {
	app := runningApp(t)
	for _, request := range []sent{
		get("/api/account/tokens"),
		post("/api/account/tokens", map[string]any{"name": "a"}),
		del("/api/account/tokens/0123456789abcdef"),
	} {
		assertStatus(t, app.send(request), http.StatusUnauthorized)
	}
}

func TestRefusedTokenNamesAreExplained(t *testing.T) {
	app := runningApp(t)
	session := app.sessionFor("henry")
	cases := []struct {
		body   map[string]any
		status int
		error  string
	}{
		{map[string]any{}, http.StatusUnprocessableEntity, ""},
		{map[string]any{"name": ""}, http.StatusBadRequest, "name is required"},
		{map[string]any{"name": " \t "}, http.StatusBadRequest, "name is required"},
		{map[string]any{"name": strings.Repeat("a", maxTokenNameChars+1)}, http.StatusBadRequest, "name is too long"},
		{map[string]any{"name": strings.Repeat("語", maxTokenNameChars+1)}, http.StatusBadRequest, "name is too long"},
		{map[string]any{"name": "line\nbreak"}, http.StatusBadRequest, "name contains a control character"},
	}
	for _, c := range cases {
		r := app.send(post("/api/account/tokens", c.body).as(session))
		assertStatus(t, r, c.status)
		if c.error != "" && r.json()["error"] != c.error {
			t.Errorf("%v: error = %v, want %q", c.body, r.json()["error"], c.error)
		}
	}
	for _, name := range []string{strings.Repeat("a", maxTokenNameChars), strings.Repeat("語", maxTokenNameChars)} {
		app.createToken(session, name)
	}
}

func TestOnlyTheMaximumNumberOfTokensCanBeMade(t *testing.T) {
	app := runningApp(t)
	session := app.sessionFor("henry")
	for range auth.MaxAPITokensPerUser {
		app.createToken(session, "a")
	}
	r := app.send(post("/api/account/tokens", map[string]any{"name": "one too many"}).as(session))
	assertStatus(t, r, http.StatusConflict)
	if r.json()["error"] != "at most 20 API tokens are allowed" {
		t.Fatalf("error = %v", r.json()["error"])
	}
}

func TestAnotherUsersTokenCannotBeEnded(t *testing.T) {
	app := runningApp(t)
	mine := app.sessionFor("henry")
	theirs := app.sessionFor("mei")
	id, _ := app.createToken(mine, "a")["id"].(string)
	assertStatus(t, app.send(del("/api/account/tokens/"+id).as(theirs)), http.StatusNotFound)
	if got := app.send(get("/api/account/tokens").as(mine)).jsonList(); len(got) != 1 {
		t.Fatalf("the owner's tokens = %v", got)
	}
	if got := app.send(get("/api/account/tokens").as(theirs)).jsonList(); len(got) != 0 {
		t.Fatalf("the stranger sees %v", got)
	}
}

func TestStrangerPostOriginIsRefusedOnTokenRoutes(t *testing.T) {
	app := runningApp(t)
	session := app.sessionFor("henry")
	r := app.send(post("/api/account/tokens", map[string]any{"name": "a"}).as(session).originated("https://evil.example"))
	assertStatus(t, r, http.StatusForbidden)
}

func TestUserTheProxySignedInMayHaveTokens(t *testing.T) {
	app := newTestApp(t, forwardAuthConfig(t))
	r := app.send(viaProxy(post("/api/account/tokens", map[string]any{"name": "script"}), "alice"))
	assertStatus(t, r, http.StatusCreated)
	if got := app.send(viaProxy(get("/api/account/tokens"), "alice")).jsonList(); len(got) != 1 {
		t.Fatalf("tokens = %v", got)
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
		get("/api/account/tokens"),
		post("/api/account/tokens", map[string]any{"name": "a"}),
		del("/api/account/tokens/0123456789abcdef"),
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
