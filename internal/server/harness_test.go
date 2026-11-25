package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/bookmarks"
	"github.com/henry40408/bkd/internal/dbtest"
	"github.com/henry40408/bkd/internal/linkcheck"
)

const testPassword = "correct horse battery"

type testApp struct {
	t      *testing.T
	app    *App
	db     *sql.DB
	hasher *auth.Hasher
	// setupDisplay is the token an operator would read from the log.
	setupDisplay string
}

func newTestApp(t *testing.T, config Config) *testApp {
	t.Helper()
	pool := dbtest.Open(t)
	config.DB = pool
	if config.LinkChecker == nil {
		config.LinkChecker = linkcheck.New(linkcheck.Config{Allow: netip.Addr.IsLoopback})
	}
	config.Hasher = auth.NewHasher()
	return &testApp{t: t, app: New(config), db: pool, hasher: config.Hasher}
}

// runningApp has setup closed, as a server that started with users.
func runningApp(t *testing.T) *testApp { return newTestApp(t, Config{}) }

func awaitingSetupApp(t *testing.T) *testApp {
	t.Helper()
	token, display := auth.NewSetupToken()
	app := newTestApp(t, Config{SetupToken: token})
	app.setupDisplay = display
	return app
}

// sent describes one request. The zero value is a GET from 192.0.2.1.
type sent struct {
	method  string
	uri     string
	body    any
	raw     string
	rawType string
	cookie  string
	ip      string
	origin  string
	headers map[string]string
}

type reply struct {
	status  int
	headers http.Header
	text    string
}

func (r reply) json() map[string]any {
	var out map[string]any
	_ = json.Unmarshal([]byte(r.text), &out)
	return out
}

func get(uri string) sent               { return sent{method: "GET", uri: uri} }
func del(uri string) sent               { return sent{method: "DELETE", uri: uri} }
func post(uri string, body any) sent    { return sent{method: "POST", uri: uri, body: body} }
func put(uri string, body any) sent     { return sent{method: "PUT", uri: uri, body: body} }
func patch(uri string, body any) sent   { return sent{method: "PATCH", uri: uri, body: body} }
func (s sent) as(token string) sent     { s.cookie = "bkd_session=" + token; return s }
func (s sent) from(ip string) sent      { s.ip = ip; return s }
func (s sent) originated(o string) sent { s.origin = o; return s }
func (s sent) withRaw(contentType, body string) sent {
	s.raw, s.rawType = body, contentType
	return s
}
func (a *testApp) send(s sent) reply {
	a.t.Helper()
	var reader io.Reader
	contentType := ""
	switch {
	case s.body != nil:
		encoded, err := json.Marshal(s.body)
		if err != nil {
			a.t.Fatal(err)
		}
		reader, contentType = strings.NewReader(string(encoded)), "application/json"
	case s.rawType != "" || s.raw != "":
		reader, contentType = strings.NewReader(s.raw), s.rawType
	}
	request := httptest.NewRequest(s.method, s.uri, reader)
	request.Host = "bkd.test"
	request.RemoteAddr = orDefault(s.ip, "192.0.2.1") + ":50000"
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if s.cookie != "" {
		request.Header.Set("Cookie", s.cookie)
	}
	if s.origin != "" {
		request.Header.Set("Origin", s.origin)
	}
	for name, value := range s.headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	a.app.Handler().ServeHTTP(recorder, request)
	return reply{recorder.Code, recorder.Header(), recorder.Body.String()}
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func (a *testApp) addUser(username string, admin bool) int64 {
	a.t.Helper()
	hash, err := a.hasher.Hash(context.Background(), testPassword)
	if err != nil {
		a.t.Fatal(err)
	}
	var id int64
	err = a.db.QueryRow(
		"INSERT INTO users (username, password_hash, is_admin, created_at) VALUES (?1, ?2, ?3, 0) RETURNING id",
		username, hash, admin).Scan(&id)
	if err != nil {
		a.t.Fatal(err)
	}
	return id
}

func (a *testApp) login(username, password string) reply {
	fields := url.Values{"username": {username}, "password": {password}}
	return a.send(formPost("/login", fields.Encode()))
}

// signIn adds a user and returns the session token.
func (a *testApp) signIn(username string, admin bool) string {
	a.t.Helper()
	a.addUser(username, admin)
	r := a.login(username, testPassword)
	if r.status != http.StatusSeeOther {
		a.t.Fatalf("login: %d %s", r.status, r.text)
	}
	return sessionToken(a.t, r)
}

// confirmed signs in an admin and has them type their password again, as the
// pages that change accounts ask.
func (a *testApp) confirmed(username string) string {
	a.t.Helper()
	token := a.signIn(username, true)
	r := a.send(formPost("/confirm", "password="+url.QueryEscape(testPassword)).as(token))
	assertStatus(a.t, r, http.StatusSeeOther)
	return token
}

func sessionToken(t *testing.T, r reply) string {
	t.Helper()
	cookie := r.headers.Get("Set-Cookie")
	pair, _, _ := strings.Cut(cookie, ";")
	_, token, found := strings.Cut(pair, "=")
	if !found || token == "" {
		t.Fatalf("no session cookie in %q", cookie)
	}
	return token
}

// createBookmark saves a bookmark through the form of the signed-in user and
// returns it as stored. Its address is made from the title.
func (a *testApp) createBookmark(token, title string, tags ...string) bookmarks.Bookmark {
	a.t.Helper()
	address := "https://example.com/" + strings.ReplaceAll(title, " ", "-")
	return a.createBookmarkAt(token, address, title, tags...)
}

func (a *testApp) createBookmarkAt(token, address, title string, tags ...string) bookmarks.Bookmark {
	a.t.Helper()
	fields := url.Values{"url": {address}, "title": {title}, "tags": {strings.Join(tags, ",")}}
	a.saveThroughForm(token, fields.Encode())
	userID := a.ownerOf(token)
	var id int64
	query := "SELECT id FROM bookmarks WHERE url = ?1 AND user_id = ?2 ORDER BY id DESC LIMIT 1"
	if err := a.db.QueryRow(query, address, userID).Scan(&id); err != nil {
		a.t.Fatalf("the form saved nothing for user %d: %v", userID, err)
	}
	return a.storedBookmark(userID, id)
}

// ownerOf is the user a session cookie belongs to. Tests look rows up by it,
// so a bookmark saved under the wrong user is not found.
func (a *testApp) ownerOf(token string) int64 {
	a.t.Helper()
	sum := sha256.Sum256([]byte(token))
	var userID int64
	if err := a.db.QueryRow("SELECT user_id FROM sessions WHERE token_hash = ?1", hex.EncodeToString(sum[:])).Scan(&userID); err != nil {
		a.t.Fatalf("no session for the cookie: %v", err)
	}
	return userID
}

func assertStatus(t *testing.T, r reply, want int) {
	t.Helper()
	if r.status != want {
		t.Fatalf("status = %d, want %d; body %q", r.status, want, r.text)
	}
}
