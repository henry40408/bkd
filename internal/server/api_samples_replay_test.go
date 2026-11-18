package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	passwordBob   = "sturdy umbrella pass"
	passwordCarol = "violet river lantern"
	jsonType      = "application/json"
)

func j(sample, method, path, who string, body any) call {
	return call{sample: sample, method: method, path: path, who: who, json: body}
}

func rawCall(sample, method, path, who, contentType, body string) call {
	return call{sample: sample, method: method, path: path, who: who, raw: text(body), rawType: contentType}
}

func repeated(char string, n int) string { return strings.Repeat(char, n) }

func ids(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i + 1
	}
	return out
}

type tags = []string
type obj = map[string]any

func bookmarkJSON(address, title, note string, tags tags) obj {
	return obj{"url": address, "title": title, "note": note, "tags": tags}
}

// sampleCalls is the capture script, minus the pages.
func sampleCalls(setupToken string) []call {
	const admin, bob = "admin", "bob"
	calls := []call{
		j("state-setup-required", "GET", "/api/state", "", nil),
		j("setup-bad-token", "POST", "/api/setup", "", obj{"token": "AAAAA-AAAAA", "username": "alice", "password": testPassword}),
		rawCall("setup-bad-json", "POST", "/api/setup", "", jsonType, "{"),
		j("setup-missing-field", "POST", "/api/setup", "", obj{"token": setupToken}),
		rawCall("setup-wrong-content-type", "POST", "/api/setup", "", "text/plain", "x"),
		j("setup-bad-username", "POST", "/api/setup", "", obj{"token": setupToken, "username": "A!", "password": testPassword}),
		j("setup-short-password", "POST", "/api/setup", "", obj{"token": setupToken, "username": "alice", "password": "short"}),
		{sample: "setup-cross-origin", method: "POST", path: "/api/setup", json: obj{"token": setupToken}, headers: map[string]string{"Origin": "http://evil.example"}},
		{sample: "setup-ok", method: "POST", path: "/api/setup", json: obj{"token": setupToken, "username": "Alice", "password": testPassword}, keep: admin},
		j("setup-again", "POST", "/api/setup", "", obj{"token": setupToken, "username": "alice", "password": testPassword}),
		j("state-signed-in", "GET", "/api/state", admin, nil),
		j("state-signed-out", "GET", "/api/state", "", nil),
		j("login-wrong-password", "POST", "/api/login", "", obj{"username": "alice", "password": "wrong password!!"}),
		j("login-unknown-user", "POST", "/api/login", "", obj{"username": "nobody", "password": "wrong password!!"}),
		j("login-ok", "POST", "/api/login", "", obj{"username": "ALICE", "password": testPassword}),
		j("logout", "POST", "/api/logout", "", nil),
		j("bookmarks-unauthenticated", "GET", "/api/bookmarks", "", nil),
		{sample: "bookmarks-bad-cookie", method: "GET", path: "/api/bookmarks", cookie: "0000000000000000000000000000000000000000000000000000000000000000"},
		{sample: "bookmarks-bad-cookie-unauth-bad-body", method: "POST", path: "/api/bookmarks", raw: text("{"), rawType: jsonType, cookie: "0000000000000000000000000000000000000000000000000000000000000000"},
		j("method-not-allowed", "DELETE", "/api/state", "", nil),
		j("method-not-allowed-bookmarks", "PATCH", "/api/bookmarks", admin, nil),
		j("api-not-found", "GET", "/api/nothing", "", nil),
		j("path-id-invalid", "DELETE", "/api/bookmarks/abc", admin, nil),
		j("path-id-negative", "DELETE", "/api/bookmarks/-1", admin, nil),
		j("query-invalid", "GET", "/api/bookmarks?limit=abc", admin, nil),
		j("bookmarks-empty", "GET", "/api/bookmarks", admin, nil),
		j("random-empty", "GET", "/api/bookmarks/random", admin, nil),
		j("tags-empty", "GET", "/api/tags", admin, nil),
		j("bookmark-create", "POST", "/api/bookmarks", admin, bookmarkJSON("https://Example.com/a?x=1&y=<2>", " Example \"A\" ", "A <note> & more\n", tags{"Rust", "web", "rust", " "})),
		j("bookmark-create-2", "POST", "/api/bookmarks", admin, bookmarkJSON("http://example.org/b", "Beta", "", tags{"web", "tools"})),
		j("bookmark-create-3", "POST", "/api/bookmarks", admin, bookmarkJSON("https://rust-lang.org/", "Rust language", "systems", tags{"rust"})),
		j("bookmark-create-bad-url", "POST", "/api/bookmarks", admin, bookmarkJSON("ftp://x", "t", "", tags{})),
		j("bookmark-create-invalid-url", "POST", "/api/bookmarks", admin, bookmarkJSON("https://a b", "t", "", tags{})),
		j("bookmark-create-long-url", "POST", "/api/bookmarks", admin, bookmarkJSON("https://"+repeated("a", 2048), "t", "", tags{})),
		j("bookmark-create-long-title", "POST", "/api/bookmarks", admin, bookmarkJSON("https://x.example/", repeated("t", 301), "", tags{})),
		j("bookmark-create-empty-title", "POST", "/api/bookmarks", admin, bookmarkJSON("https://x.example/", "  ", "", tags{})),
		j("bookmark-create-long-note", "POST", "/api/bookmarks", admin, bookmarkJSON("https://x.example/", "t", repeated("n", 5001), tags{})),
		j("bookmark-create-long-tag", "POST", "/api/bookmarks", admin, bookmarkJSON("https://x.example/", "t", "", tags{repeated("g", 51)})),
		j("bookmark-create-control-tag", "POST", "/api/bookmarks", admin, bookmarkJSON("https://x.example/", "t", "", tags{"a\u0001b"})),
		j("bookmark-create-many-tags", "POST", "/api/bookmarks", admin, bookmarkJSON("https://x.example/", "t", "", numberedTags("t", 21))),
		j("bookmark-create-missing-field", "POST", "/api/bookmarks", admin, obj{"url": "https://x.example/"}),
		j("bookmark-create-wrong-type", "POST", "/api/bookmarks", admin, obj{"url": 1, "title": "t", "note": "", "tags": tags{}}),
		rawCall("bookmark-create-too-large", "POST", "/api/bookmarks", admin, jsonType, repeated("x", 70000)),
		rawCall("bookmark-create-415", "POST", "/api/bookmarks", admin, "text/plain", "{}"),
		rawCall("bookmark-create-no-content-type", "POST", "/api/bookmarks", admin, "", "{}"),
		j("bookmarks-list", "GET", "/api/bookmarks", admin, nil),
		j("bookmarks-search", "GET", "/api/bookmarks?q=rust&limit=1&offset=0", admin, nil),
		j("bookmarks-tag", "GET", "/api/bookmarks?tag=web", admin, nil),
		j("bookmarks-status", "GET", "/api/bookmarks?status=unchecked&limit=0", admin, nil),
		j("bookmarks-limit-clamp", "GET", "/api/bookmarks?limit=9999&offset=1", admin, nil),
		{sample: "bookmark-get-random", method: "GET", path: "/api/bookmarks/random", who: admin, anyBody: true},
		j("tags", "GET", "/api/tags", admin, nil),
		j("bookmark-update", "PUT", "/api/bookmarks/1", admin, bookmarkJSON("https://example.com/a2", "Example 2", "n", tags{"x"})),
		j("bookmark-update-missing", "PUT", "/api/bookmarks/999", admin, bookmarkJSON("https://example.com/a2", "Example 2", "n", tags{"x"})),
		j("bookmark-delete-missing", "DELETE", "/api/bookmarks/999", admin, nil),
		j("bulk-add-tags", "POST", "/api/bookmarks/bulk", admin, obj{"ids": []int{1, 2, 2}, "action": "add_tags", "tags": tags{"bulk", "Rust"}}),
		j("bulk-remove-tags", "POST", "/api/bookmarks/bulk", admin, obj{"ids": []int{1, 2}, "action": "remove_tags", "tags": tags{"bulk"}}),
		j("bulk-empty-ids", "POST", "/api/bookmarks/bulk", admin, obj{"ids": []int{}, "action": "delete"}),
		j("bulk-too-many-ids", "POST", "/api/bookmarks/bulk", admin, obj{"ids": ids(201), "action": "delete"}),
		j("bulk-empty-tags", "POST", "/api/bookmarks/bulk", admin, obj{"ids": []int{1}, "action": "add_tags", "tags": tags{}}),
		j("bulk-missing-tags-field", "POST", "/api/bookmarks/bulk", admin, obj{"ids": []int{1}, "action": "add_tags"}),
		j("bulk-unknown-action", "POST", "/api/bookmarks/bulk", admin, obj{"ids": []int{1}, "action": "explode"}),
		j("bulk-missing-ids", "POST", "/api/bookmarks/bulk", admin, obj{"action": "delete"}),
		j("bulk-not-found", "POST", "/api/bookmarks/bulk", admin, obj{"ids": []int{1, 999}, "action": "delete"}),
		j("bulk-too-many-tags", "POST", "/api/bookmarks/bulk", admin, obj{"ids": []int{1}, "action": "add_tags", "tags": numberedTags("q", 20)}),
		j("settings-get", "GET", "/api/settings", admin, nil),
		j("settings-put", "PUT", "/api/settings", admin, obj{"link_check_enabled": true}),
		j("settings-put-missing", "PUT", "/api/settings", admin, obj{}),
		j("settings-get-after", "GET", "/api/settings", admin, nil),
		j("settings-put-off", "PUT", "/api/settings", admin, obj{"link_check_enabled": false}),
		j("export-html", "GET", "/api/export?format=html", admin, nil),
		j("export-json", "GET", "/api/export?format=json", admin, nil),
		j("export-bad-format", "GET", "/api/export?format=csv", admin, nil),
		j("export-no-format", "GET", "/api/export", admin, nil),
		j("check-empty", "POST", "/api/bookmarks/check", admin, obj{"ids": []int{}}),
		j("check-not-found", "POST", "/api/bookmarks/check", admin, obj{"ids": []int{999}}),
		j("check-sse", "POST", "/api/bookmarks/check", admin, obj{"ids": []int{3}}),
		rawCall("import-ok", "POST", "/api/import", admin, "text/html; charset=utf-8", netscapeFile),
		rawCall("import-again", "POST", "/api/import", admin, "text/html", netscapeFile),
		rawCall("import-415", "POST", "/api/import", admin, jsonType, netscapeFile),
		rawCall("import-no-content-type", "POST", "/api/import", admin, "", netscapeFile),
		j("export-html-after-import", "GET", "/api/export?format=html", admin, nil),
	}
	return append(calls, accountCalls()...)
}

const netscapeFile = `<!DOCTYPE NETSCAPE-Bookmark-file-1><DL><p><DT><H3>Dev</H3><DL><p>` +
	`<DT><A HREF="https://imported.example/one" ADD_DATE="1600000000" TAGS="a,b">One &amp; only</A>` +
	`<DD>a note<DT><A HREF="https://example.org/b">dup</A>` +
	`<DT><A HREF="notaurl">bad</A></DL><p></DL><p>`

func numberedTags(prefix string, n int) tags {
	out := make(tags, n)
	for i := range out {
		out[i] = prefix + strconv.Itoa(i)
	}
	return out
}

func accountCalls() []call {
	const admin, bob = "admin", "bob"
	return []call{
		j("users-list", "GET", "/api/users", admin, nil),
		j("users-create-reauth", "POST", "/api/users", admin, obj{"username": "bob", "password": passwordBob}),
		j("account-confirm-wrong", "POST", "/api/account/confirm", admin, obj{"password": "nope nope nope nope"}),
		j("account-confirm", "POST", "/api/account/confirm", admin, obj{"password": testPassword}),
		j("users-create", "POST", "/api/users", admin, obj{"username": "Bob", "password": passwordBob, "is_admin": false}),
		j("users-create-taken", "POST", "/api/users", admin, obj{"username": "bob", "password": passwordBob, "is_admin": false}),
		j("users-create-contains-name", "POST", "/api/users", admin, obj{"username": "dave", "password": "my-dave-password", "is_admin": false}),
		j("users-create-carol", "POST", "/api/users", admin, obj{"username": "carol", "password": passwordCarol, "is_admin": false}),
		j("users-role", "PUT", "/api/users/2/role", admin, obj{"is_admin": true}),
		j("users-role-back", "PUT", "/api/users/2/role", admin, obj{"is_admin": false}),
		j("users-role-last-admin", "PUT", "/api/users/1/role", admin, obj{"is_admin": false}),
		j("users-role-missing", "PUT", "/api/users/99/role", admin, obj{"is_admin": false}),
		j("users-disable", "PUT", "/api/users/3/disabled", admin, obj{"is_disabled": true}),
		j("users-disable-self-last-admin", "PUT", "/api/users/1/disabled", admin, obj{"is_disabled": true}),
		j("users-password", "PUT", "/api/users/3/password", admin, obj{"password": passwordCarol + "2"}),
		j("users-password-short", "PUT", "/api/users/3/password", admin, obj{"password": "short"}),
		j("users-delete-missing", "DELETE", "/api/users/99", admin, nil),
		j("users-list-after", "GET", "/api/users", admin, nil),
		{sample: "login-bob", method: "POST", path: "/api/login", json: obj{"username": "bob", "password": passwordBob}, keep: bob},
		j("users-list-forbidden", "GET", "/api/users", bob, nil),
		j("users-create-forbidden", "POST", "/api/users", bob, obj{"username": "eve", "password": passwordBob}),
		j("login-disabled", "POST", "/api/login", "", obj{"username": "carol", "password": passwordCarol + "2"}),
		j("random-bob-empty", "GET", "/api/bookmarks/random", bob, nil),
		j("bookmark-update-not-mine", "PUT", "/api/bookmarks/1", bob, bookmarkJSON("https://example.com/a2", "x", "", tags{})),
		j("account-change-password-wrong", "PUT", "/api/account/password", bob, obj{"current": "wrong wrong wrong", "new": "another long password"}),
		j("account-change-password-same", "PUT", "/api/account/password", bob, obj{"current": passwordBob, "new": passwordBob}),
		j("account-change-password-short", "PUT", "/api/account/password", bob, obj{"current": passwordBob, "new": "short"}),
		j("account-sessions-list", "GET", "/api/account/sessions", bob, nil),
		j("account-sessions-revoke-missing", "DELETE", "/api/account/sessions/0000000000000000", bob, nil),
		j("account-sessions-revoke-others", "DELETE", "/api/account/sessions", bob, nil),
		j("account-change-password", "PUT", "/api/account/password", bob, obj{"current": passwordBob, "new": passwordBob + "!"}),
		j("bookmarks-after-check", "GET", "/api/bookmarks?limit=1", admin, nil),
		j("asset-app.css", "GET", "/app.css", "", nil),
		j("asset-app.js", "GET", "/app.js", "", nil),
		j("asset-favicon.svg", "GET", "/favicon.svg", "", nil),
		j("asset-favicon.ico", "GET", "/favicon.ico", "", nil),
		j("asset-apple-touch-icon.png", "GET", "/apple-touch-icon.png", "", nil),
		j("login-rate-limited", "POST", "/api/login", "", obj{"username": "nobody", "password": "wrong password!!"}),
	}
}

func (a *testApp) sendCall(c call, cast map[string]string) reply {
	request := sent{method: c.method, uri: c.path, headers: map[string]string{"User-Agent": "bkd-fixture"}}
	for name, value := range c.headers {
		request.headers[name] = value
	}
	request = request.from("127.0.0.1")
	switch {
	case c.json != nil:
		request.body = c.json
	case c.raw != nil:
		request = request.withRaw(c.rawType, *c.raw)
	}
	if c.who != "" {
		request = request.as(cast[c.who])
	}
	if c.cookie != "" {
		request = request.as(c.cookie)
	}
	if c.method != "GET" && request.headers["Origin"] == "" {
		request = request.originated("http://bkd.test")
	}
	return a.send(request)
}

func TestApiSamplesMatchRust(t *testing.T) {
	app := awaitingSetupApp(t)
	cast := map[string]string{}
	calls := sampleCalls(app.setupDisplay)
	for _, c := range calls {
		if c.sample == "login-rate-limited" {
			for range 10 {
				app.sendCall(call{method: "POST", path: "/api/login", json: obj{"username": "nobody", "password": "wrong password!!"}}, cast)
			}
		}
		if c.sample == "logout" {
			c.who = "logout-session"
		}
		var restore func()
		if c.sample == "check-sse" {
			restore = answeringBookmark(app, 3)
		}
		got := app.sendCall(c, cast)
		if restore != nil {
			restore()
		}
		if c.keep != "" {
			cast[c.keep] = sessionToken(t, got)
		}
		if c.sample == "login-ok" {
			cast["logout-session"] = sessionToken(t, got)
		}
		app.compareSample(t, c, got)
	}
	requireEverySampleReplayed(t, calls)
}

// answeringBookmark points a bookmark at a local site that answers 200, because
// the capture reached a real one. The returned function puts the address back.
func answeringBookmark(app *testApp, id int64) (restore func()) {
	site := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	var original string
	if err := app.db.QueryRow("SELECT url FROM bookmarks WHERE id = ?1", id).Scan(&original); err != nil {
		app.t.Fatal(err)
	}
	app.exec("UPDATE bookmarks SET url = ?1 WHERE id = ?2", site.URL+"/", id)
	return func() {
		site.Close()
		app.exec("UPDATE bookmarks SET url = ?1 WHERE id = ?2", original, id)
	}
}

func requireEverySampleReplayed(t *testing.T, calls []call) {
	t.Helper()
	replayed := map[string]bool{}
	for _, c := range calls {
		replayed[c.sample] = true
	}
	files, err := filepath.Glob(samplesDir + "*.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".txt")
		if !replayed[name] {
			t.Errorf("sample %s is not replayed", name)
		}
	}
}
