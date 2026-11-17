package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/henry40408/bkd/internal/auth"
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

func (r reply) jsonList() []any {
	var out []any
	_ = json.Unmarshal([]byte(r.text), &out)
	return out
}

func get(uri string) sent               { return sent{method: "GET", uri: uri} }
func del(uri string) sent               { return sent{method: "DELETE", uri: uri} }
func post(uri string, body any) sent    { return sent{method: "POST", uri: uri, body: body} }
func put(uri string, body any) sent     { return sent{method: "PUT", uri: uri, body: body} }
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
	return a.send(post("/api/login", map[string]any{"username": username, "password": password}))
}

// signIn adds a user and returns the session token.
func (a *testApp) signIn(username string, admin bool) string {
	a.t.Helper()
	a.addUser(username, admin)
	r := a.login(username, testPassword)
	if r.status != http.StatusOK {
		a.t.Fatalf("login: %d %s", r.status, r.text)
	}
	return sessionToken(a.t, r)
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

func (a *testApp) createBookmark(token, title string, tags ...string) map[string]any {
	a.t.Helper()
	url := "https://example.com/" + strings.ReplaceAll(title, " ", "-")
	r := a.send(post("/api/bookmarks", bookmarkBody(url, title, tags...)).as(token))
	if r.status != http.StatusCreated {
		a.t.Fatalf("create: %d %s", r.status, r.text)
	}
	return r.json()
}

func bookmarkBody(url, title string, tags ...string) map[string]any {
	if tags == nil {
		tags = []string{}
	}
	return map[string]any{"url": url, "title": title, "note": "", "tags": tags}
}

func titlesOf(t *testing.T, r reply) []string {
	t.Helper()
	titles := []string{}
	for _, item := range r.json()["bookmarks"].([]any) {
		titles = append(titles, item.(map[string]any)["title"].(string))
	}
	return titles
}

func assertStatus(t *testing.T, r reply, want int) {
	t.Helper()
	if r.status != want {
		t.Fatalf("status = %d, want %d; body %q", r.status, want, r.text)
	}
}
