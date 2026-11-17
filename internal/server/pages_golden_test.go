package server

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/henry40408/bkd/internal/bookmarks"
)

// Each golden is a response the Rust server gave; its first line is a comment with
// the request, the status and the Location.
const goldenDir = "../views/testdata/"

var yearsAgo = regexp.MustCompile(`\d+ years? ago`)

// A bookmark made during the test carries the time it was made.
var madeNow = regexp.MustCompile(`datetime="20\d\d-[^"]*" title="20\d\d-[^"]*"`)

func normalised(page string) string {
	return madeNow.ReplaceAllString(yearsAgo.ReplaceAllString(page, "N"), "T")
}

// formField is one form formField; a slice keeps the order the browser would send.
type formField struct{ name, value string }

type step struct {
	golden string
	method string
	path   string
	form   []formField
	// as is the session token; empty is signed out.
	as      string
	headers map[string]string
	// noOrigin leaves out the Origin header that a browser always sends on a POST.
	noOrigin bool
	multi    string
	// statusOnly skips the body, for a page that shows a random pick.
	statusOnly bool
}

func encodeForm(fields []formField) string {
	pairs := make([]string, len(fields))
	for i, f := range fields {
		pairs[i] = url.QueryEscape(f.name) + "=" + url.QueryEscape(f.value)
	}
	return strings.Join(pairs, "&")
}

func (a *testApp) play(t *testing.T, s step) {
	t.Helper()
	request := sent{method: s.method, uri: s.path, headers: s.headers}
	switch {
	case s.multi != "":
		request = request.withRaw("multipart/form-data; boundary=zzz", s.multi)
	case s.form != nil:
		request = request.withRaw("application/x-www-form-urlencoded", encodeForm(s.form))
	}
	if s.as != "" {
		request = request.as(s.as)
	}
	if s.method != "GET" && !s.noOrigin && s.headers["Origin"] == "" {
		request = request.originated("http://bkd.test")
	}
	got := a.send(request)
	compareGolden(t, s.golden, got, s.statusOnly)
}

func compareGolden(t *testing.T, name string, got reply, statusOnly bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(goldenDir, name+".golden.html"))
	if err != nil {
		t.Fatal(err)
	}
	comment, body, _ := strings.Cut(string(raw), "\n")
	var wantStatus, wantLocation string
	fields := strings.Fields(strings.TrimSuffix(strings.TrimPrefix(comment, "<!-- "), " -->"))
	for i, word := range fields {
		if word == "->" {
			wantStatus = fields[i+1]
			if len(fields) > i+2 {
				wantLocation = fields[i+2]
			}
		}
	}
	if wantStatus == "" {
		t.Fatalf("%s: golden has no status", name)
	}
	if want := wantStatus; strings.TrimSpace(want) != strconv.Itoa(got.status) {
		t.Errorf("%s: status = %d, want %s; body %q", name, got.status, want, got.text)
	}
	if location := got.headers.Get("Location"); location != wantLocation {
		t.Errorf("%s: Location = %q, want %q", name, location, wantLocation)
	}
	if statusOnly {
		return
	}
	gotText, wantText := normalised(got.text), normalised(body)
	if gotText != wantText {
		at := firstDifference(gotText, wantText)
		t.Errorf("%s: body differs at %d\n got: %s\nwant: %s", name, at, around(gotText, at), around(wantText, at))
	}
}

func TestGoldensBeforeSetup(t *testing.T) {
	app := awaitingSetupApp(t)
	for _, s := range []step{
		{golden: "setup-form", method: "GET", path: "/setup"},
		{golden: "login-redirects-to-setup", method: "GET", path: "/login"},
		{golden: "home-signed-out-redirect", method: "GET", path: "/"},
		{golden: "setup-bad-token-page", method: "POST", path: "/setup", form: []formField{
			{"token", "AAAAA-AAAAA"}, {"username", "alice"}, {"password", testPassword}}},
		{golden: "setup-cross-origin-page", method: "POST", path: "/setup",
			form: []formField{{"token", app.setupDisplay}}, headers: map[string]string{"Origin": "http://evil.example"}},
		{golden: "setup-no-origin-page", method: "POST", path: "/setup",
			form: []formField{{"token", app.setupDisplay}}, noOrigin: true},
	} {
		app.play(t, s)
	}
}

func TestGoldensAfterSetup(t *testing.T) {
	app := runningApp(t)
	admin := app.signIn("alice", true)
	for _, s := range []step{
		{golden: "setup-after-complete", method: "GET", path: "/setup"},
		{golden: "login-form", method: "GET", path: "/login"},
		{golden: "login-form-next", method: "GET", path: "/login?next=%2Fusers&notice=session-ended"},
		{golden: "login-form-open-redirect", method: "GET", path: "/login?next=%2F%2Fevil.example"},
		{golden: "login-wrong-password-page", method: "POST", path: "/login",
			form: []formField{{"username", "alice"}, {"password", "wrong password!!"}}},
		{golden: "login-ok-page", method: "POST", path: "/login",
			form: []formField{{"username", "alice"}, {"password", testPassword}, {"next", "/users"}}},
		{golden: "not-found-page", method: "GET", path: "/nothing", as: admin},
		{golden: "not-found-page-signed-out", method: "GET", path: "/nothing"},
		{golden: "method-not-allowed-page", method: "DELETE", path: "/", as: admin},
		{golden: "home-empty", method: "GET", path: "/", as: admin},
	} {
		app.play(t, s)
	}
	for range 10 {
		app.login("nobody", "wrong password!!")
	}
	app.play(t, step{golden: "login-rate-limited-page", method: "POST", path: "/login",
		form: []formField{{"username", "nobody"}, {"password", "wrong password!!"}}})
}

type fixtureTokens struct{ alice, bob string }

// seededApp has what the capture had: alice (admin), bob and a disabled carol, three
// tagged bookmarks and sixty plain ones, all from January 2020.
func seededApp(t *testing.T) (*testApp, fixtureTokens) {
	t.Helper()
	app := runningApp(t)
	tokens := fixtureTokens{alice: app.signIn("alice", true), bob: app.signIn("bob", false)}
	app.addUser("carol", false)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := app.db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("UPDATE users SET disabled_at = 1 WHERE username = 'carol'")
	exec("UPDATE sessions SET confirmed_at = strftime('%s', 'now')")
	for _, seed := range []struct {
		url, title, note string
		tags             []string
	}{
		{"https://example.com/a?x=1&y=<2>", `Example "A"`, "A <note> & more", []string{"rust", "web"}},
		{"http://example.org/b", "Beta", "", []string{"web", "tools"}},
		{"https://rust-lang.org/", "Rust language", "systems", []string{"rust"}},
	} {
		valid, err := bookmarks.Input{URL: seed.url, Title: seed.title, Note: seed.note, Tags: seed.tags}.Validate()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := app.app.bookmarks.Create(context.Background(), 1, valid, 0); err != nil {
			t.Fatal(err)
		}
	}
	exec("UPDATE bookmarks SET created_at = 1577836800 + id * 86400, updated_at = 1577836800 + id * 86400")
	exec("UPDATE users SET created_at = 1577836800 + id * 3600")
	exec("UPDATE bookmarks SET link_status='broken', link_checked_at=1577900000, link_detail='HTTP 404' WHERE id=2")
	exec("UPDATE bookmarks SET link_status='redirected', link_checked_at=1577900000, link_detail='https://www.rust-lang.org/' WHERE id=3")
	exec("UPDATE bookmarks SET link_status='ok', link_checked_at=1577900000, link_detail=NULL WHERE id=1")
	for i := range 60 {
		exec("INSERT INTO bookmarks (user_id, url, title, note, created_at, updated_at) VALUES (1, ?1, ?2, '', ?3, ?3)",
			"https://page.example/"+strconv.Itoa(i), "Page "+strconv.Itoa(i), 1577836800+1000+i)
	}
	return app, tokens
}

func TestGoldensWithBookmarks(t *testing.T) {
	app, who := seededApp(t)
	a, b := who.alice, who.bob
	post := func(golden, path, as string, form ...formField) step {
		return step{golden: golden, method: "POST", path: path, form: append([]formField{}, form...), as: as}
	}
	get := func(golden, path, as string) step { return step{golden: golden, method: "GET", path: path, as: as} }
	steps := []step{
		get("home-list", "/", a),
		get("home-page-2", "/?page=2", a),
		get("home-search", "/?q=rust+-tag%3Atools", a),
		get("home-search-no-results", "/?q=zzzz", a),
		get("home-search-highlight-escape", "/?q=%22A+%3Cnote%3E%22", a),
		get("home-tag", "/?tag=web", a),
		get("home-status-broken", "/?status=broken", a),
		get("home-status-redirected", "/?status=redirected", a),
		get("home-status-ok-tag", "/?status=ok&tag=rust", a),
		get("home-bad-page", "/?page=abc", a),
		get("home-huge-page", "/?page=99999999", a),
		get("home-bob-empty", "/", b),
		get("random", "/random", b),
		step{golden: "random-signed-in", method: "GET", path: "/random", as: a, statusOnly: true},
		get("edit-form", "/bookmarks/1/edit", a),
		get("edit-form-missing", "/bookmarks/999/edit", a),
		get("edit-form-not-mine", "/bookmarks/1/edit", b),
		post("edit-submit", "/bookmarks/1/edit", a, formField{"url", "https://example.com/new"}, formField{"title", "New title"}, formField{"note", "n"}, formField{"tags", "x, y,x"}),
		post("edit-submit-bad", "/bookmarks/1/edit", a, formField{"url", "nope"}, formField{"title", ""}, formField{"note", "n"}, formField{"tags", "x"}),
		post("edit-submit-missing", "/bookmarks/999/edit", a, formField{"url", "https://a.example"}, formField{"title", ""}, formField{"note", ""}, formField{"tags", ""}),
		get("delete-form", "/bookmarks/2/delete", a),
		get("delete-form-missing", "/bookmarks/999/delete", a),
		post("create-ok", "/bookmarks", a, formField{"url", "https://created.example/x"}, formField{"title", ""}, formField{"note", ""}, formField{"tags", "a,b"}),
		post("create-bad", "/bookmarks", a, formField{"url", "bad"}, formField{"title", ""}, formField{"note", ""}, formField{"tags", ""}),
		post("bulk-no-selection", "/bookmarks/bulk", a, formField{"action", "delete"}),
		post("bulk-no-action", "/bookmarks/bulk", a, formField{"id-1", "on"}),
		post("bulk-delete-confirm", "/bookmarks/bulk", a, formField{"action", "delete"}, formField{"id-1", "on"}, formField{"id-2", "on"}, formField{"q", "x"}, formField{"tag", "web"}, formField{"status", "ok"}, formField{"page", "2"}),
		post("bulk-add-tags-done", "/bookmarks/bulk", a, formField{"action", "add_tags"}, formField{"tags", "bulk"}, formField{"id-1", "on"}, formField{"id-2", "on"}),
		post("bulk-add-tags-empty", "/bookmarks/bulk", a, formField{"action", "add_tags"}, formField{"tags", ""}, formField{"id-1", "on"}),
		post("bulk-remove-tags-none", "/bookmarks/bulk", a, formField{"action", "remove_tags"}, formField{"tags", "nonexistent"}, formField{"id-1", "on"}),
		post("bulk-missing-id", "/bookmarks/bulk", a, formField{"action", "delete"}, formField{"confirm", "yes"}, formField{"id-999", "on"}),
		post("bulk-bad-action", "/bookmarks/bulk", a, formField{"action", "explode"}, formField{"id-1", "on"}),
		post("bookmarks-delete-submit", "/bookmarks/delete", a, formField{"id-3", "on"}),
		get("notice-bookmark-saved", "/?notice=bookmark-saved", a),
	}
	for _, n := range []string{"password-changed", "session-ended", "others-ended", "user-changed", "confirmed", "bookmark-deleted", "settings-saved", "bulk-done", "check-incomplete", "nothing-selected", "bogus"} {
		steps = append(steps, get("notice-"+n, "/?notice="+n, a))
	}
	steps = append(steps,
		get("settings", "/settings", a),
		post("settings-save", "/settings", a, formField{"link_check_enabled", "on"}),
		get("settings-saved", "/settings?notice=settings-saved", a),
		get("transfer", "/transfer", a),
		step{golden: "import-report", method: "POST", path: "/transfer/import", as: a, multi: uploadHead + importedHTML + "\r\n--zzz--\r\n"},
		step{golden: "import-no-file", method: "POST", path: "/transfer/import", as: a, multi: "--zzz--\r\n"},
		step{golden: "import-unreadable", method: "POST", path: "/transfer/import", as: a, multi: "garbage"},
		get("users-forbidden", "/users", b),
		get("users-reauth-redirect", "/users", a),
		get("confirm-form", "/confirm", a),
		get("confirm-form-next", "/confirm?next=%2Fusers", a),
		post("confirm-wrong", "/confirm", a, formField{"password", "wrong wrong wrong"}),
		post("confirm-ok", "/confirm", a, formField{"password", testPassword}, formField{"next", "/users"}),
		get("users-list", "/users", a),
		post("users-create-page", "/users", a, formField{"username", "dave"}, formField{"password", "dave-long-password!"}, formField{"is_admin", "on"}),
		post("users-create-page-bad", "/users", a, formField{"username", "dave"}, formField{"password", "x"}),
		get("users-password-form", "/users/2/password", a),
		get("users-password-form-missing", "/users/99/password", a),
		post("users-password-submit", "/users/2/password", a, formField{"password", testPassword + "?"}),
		post("users-password-submit-bad", "/users/2/password", a, formField{"password", "short"}),
		get("users-delete-form", "/users/2/delete", a),
		get("users-delete-form-self", "/users/1/delete", a),
		post("users-role-page", "/users/2/role", a, formField{"is_admin", "on"}),
		post("users-role-last-admin-page", "/users/1/role", a),
		post("users-disabled-page", "/users/2/disabled", a, formField{"is_disabled", "on"}),
		post("users-delete-submit", "/users/3/delete", a),
		post("users-delete-submit-missing", "/users/99/delete", a),
		post("account-change-password-page-wrong", "/account/password", b, formField{"current", "wrong"}, formField{"new", "another long password"}, formField{"again", "another long password"}),
		post("account-change-password-mismatch", "/account/password", b, formField{"current", testPassword}, formField{"new", "another long password"}, formField{"again", "different"}),
		post("account-sessions-revoke-others", "/account/sessions/revoke-others", b),
		post("account-session-revoke-missing", "/account/sessions/0000000000000000/revoke", b),
		post("logout-page", "/logout", b),
	)
	for _, s := range steps {
		app.play(t, s)
	}
}

const uploadHead = "--zzz\r\nContent-Disposition: form-data; name=\"file\"; filename=\"b.html\"\r\nContent-Type: text/html\r\n\r\n"

const importedHTML = `<DL><DT><A HREF="https://imp.example/1" TAGS="t">One</A><DT><A HREF="ftp://nope">Bad</A></DL>`

func firstDifference(a, b string) int {
	for i := 0; i < min(len(a), len(b)); i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}

func around(text string, at int) string {
	return text[max(at-80, 0):min(at+160, len(text))]
}
