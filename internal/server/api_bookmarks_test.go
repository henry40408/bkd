package server

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestBookmarkRoutesNeedASession(t *testing.T) {
	app := runningApp(t)
	body := bookmarkBody("https://a.b", "t")
	for _, request := range []sent{
		get("/api/bookmarks"), post("/api/bookmarks", body), put("/api/bookmarks/1", body),
		del("/api/bookmarks/1"), get("/api/tags"), get("/api/bookmarks/random"),
		post("/api/bookmarks/bulk", map[string]any{"ids": []int{1}, "action": "delete"}),
		get("/api/settings"), get("/api/export?format=json"),
	} {
		assertStatus(t, app.send(request), http.StatusUnauthorized)
	}
	assertStatus(t, app.send(get("/api/bookmarks").as("0123456789abcdef")), http.StatusUnauthorized)
}

func TestABookmarkCanBeCreatedListedChangedAndDeleted(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	created := app.createBookmark(token, "Rust book", "Rust", "book")
	if !reflect.DeepEqual(created["tags"], []any{"book", "rust"}) {
		t.Fatalf("tags = %v", created["tags"])
	}
	uri := fmt.Sprintf("/api/bookmarks/%v", created["id"])
	if got := titlesOf(t, app.send(get("/api/bookmarks").as(token))); !reflect.DeepEqual(got, []string{"Rust book"}) {
		t.Fatalf("titles = %v", got)
	}
	changed := app.send(put(uri, bookmarkBody("https://example.com/new", "New title", "x")).as(token))
	assertStatus(t, changed, http.StatusOK)
	if changed.json()["title"] != "New title" {
		t.Fatalf("body = %s", changed.text)
	}
	assertStatus(t, app.send(del(uri).as(token)), http.StatusNoContent)
	assertStatus(t, app.send(del(uri).as(token)), http.StatusNotFound)
	if empty := app.send(get("/api/bookmarks").as(token)); empty.text != `{"bookmarks":[]}` {
		t.Fatalf("empty list = %s", empty.text)
	}
}

func TestInvalidBookmarksAreRejectedWithAReason(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	for _, body := range []map[string]any{
		bookmarkBody("javascript:alert(1)", "t"),
		bookmarkBody("https://a.b", "   "),
	} {
		assertStatus(t, app.send(post("/api/bookmarks", body).as(token)), http.StatusBadRequest)
	}
	assertStatus(t, app.send(post("/api/bookmarks", map[string]any{"url": "https://a.b"}).as(token)), http.StatusUnprocessableEntity)
	r := app.send(post("/api/bookmarks", bookmarkBody("ftp://a.b", "t")).as(token))
	assertStatus(t, r, http.StatusBadRequest)
	if !strings.Contains(r.json()["error"].(string), "http") {
		t.Fatalf("reason = %s", r.text)
	}
}

func TestAnonymousRequestsWithABadBodyGet401(t *testing.T) {
	app := runningApp(t)
	r := app.send(sent{method: "POST", uri: "/api/bookmarks"}.withRaw("text/plain", "x"))
	assertStatus(t, r, http.StatusUnauthorized)
}

func TestBadPathIDsAndQueriesAre400(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	r := app.send(del("/api/bookmarks/abc").as(token))
	assertStatus(t, r, http.StatusBadRequest)
	if r.text != "Invalid URL: Cannot parse `abc` to a `i64`" {
		t.Fatalf("body = %q", r.text)
	}
	r = app.send(get("/api/bookmarks?limit=x").as(token))
	assertStatus(t, r, http.StatusBadRequest)
	if r.text != "Failed to deserialize query string: limit: invalid digit found in string" {
		t.Fatalf("body = %q", r.text)
	}
}

func TestUsersCannotSeeOrTouchEachOthersBookmarks(t *testing.T) {
	app := runningApp(t)
	henry := app.signIn("henry", false)
	mei := app.signIn("mei", false)
	private := app.createBookmark(henry, "henry secret", "private")
	uri := fmt.Sprintf("/api/bookmarks/%v", private["id"])

	if got := titlesOf(t, app.send(get("/api/bookmarks?q=secret").as(mei))); len(got) != 0 {
		t.Fatalf("leaked %v", got)
	}
	if tags := app.send(get("/api/tags").as(mei)); tags.text != `{"tags":[],"links":[]}` {
		t.Fatalf("tags = %s", tags.text)
	}
	assertStatus(t, app.send(put(uri, bookmarkBody("https://evil.test", "hijacked")).as(mei)), http.StatusNotFound)
	assertStatus(t, app.send(del(uri).as(mei)), http.StatusNotFound)
	if got := titlesOf(t, app.send(get("/api/bookmarks").as(henry))); !reflect.DeepEqual(got, []string{"henry secret"}) {
		t.Fatalf("titles = %v", got)
	}
}

func TestSearchAndTagFiltersWorkThroughTheAPI(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	app.createBookmark(token, "學習 Rust", "教學")
	app.createBookmark(token, "Rust async", "rust")
	app.createBookmark(token, "Cooking", "rust")
	for uri, want := range map[string][]string{
		"/api/bookmarks?q=rust%20async":         {"Rust async"},
		"/api/bookmarks?q=%E5%AD%B8%E7%BF%92":   {"學習 Rust"},
		"/api/bookmarks?tag=Rust":               {"Cooking", "Rust async"},
		"/api/bookmarks?tag=rust&q=cook":        {"Cooking"},
		"/api/bookmarks?status=unchecked&q=zzz": {},
	} {
		if got := titlesOf(t, app.send(get(uri).as(token))); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v, want %v", uri, got, want)
		}
	}
	tags := app.send(get("/api/tags").as(token)).json()["tags"]
	want := []any{map[string]any{"tag": "rust", "count": float64(2)}, map[string]any{"tag": "教學", "count": float64(1)}}
	if !reflect.DeepEqual(tags, want) {
		t.Fatalf("tags = %v", tags)
	}
}

func TestRandomPicksFromTheFilteredBookmarksOfTheCallerOnly(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	other := app.signIn("mei", false)
	app.createBookmark(token, "Rust async", "rust")
	app.createBookmark(token, "Cooking")
	app.createBookmark(other, "Mei only", "rust")

	if got := app.send(get("/api/bookmarks/random?tag=rust").as(token)).json()["title"]; got != "Rust async" {
		t.Fatalf("by tag = %v", got)
	}
	assertStatus(t, app.send(get("/api/bookmarks/random?q=zzz").as(token)), http.StatusNotFound)
	assertStatus(t, app.send(get("/api/bookmarks/random?q=Mei").as(token)), http.StatusNotFound)
}

func TestRandomDoesNotAlwaysPickTheSameBookmark(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	for n := range 10 {
		app.createBookmark(token, fmt.Sprintf("item %d", n))
	}
	seen := map[any]bool{}
	for range 40 {
		seen[app.send(get("/api/bookmarks/random").as(token)).json()["id"]] = true
	}
	if len(seen) < 2 {
		t.Fatal("always the same bookmark")
	}
}

func (a *testApp) bulk(token string, body map[string]any) reply {
	return a.send(post("/api/bookmarks/bulk", body).as(token))
}

func TestBulkTagsReportHowManyBookmarksChanged(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	a := app.createBookmark(token, "a", "rust")
	b := app.createBookmark(token, "b")
	ids := []any{a["id"], b["id"], a["id"]}

	added := app.bulk(token, map[string]any{"ids": ids, "action": "add_tags", "tags": []string{" Rust ", "New"}})
	assertStatus(t, added, http.StatusOK)
	if added.json()["changed"] != float64(2) {
		t.Fatalf("added = %s", added.text)
	}
	removed := app.bulk(token, map[string]any{"ids": ids, "action": "remove_tags", "tags": []string{"rust"}})
	assertStatus(t, removed, http.StatusOK)
	deleted := app.bulk(token, map[string]any{"ids": ids, "action": "delete"})
	assertStatus(t, deleted, http.StatusOK)
	if got := titlesOf(t, app.send(get("/api/bookmarks").as(token))); len(got) != 0 {
		t.Fatalf("left %v", got)
	}
}

func TestBulkRefusesForeignIDsAndBadInput(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	other := app.signIn("mei", false)
	theirs := app.createBookmark(other, "theirs")
	mine := app.createBookmark(token, "mine")

	foreign := app.bulk(token, map[string]any{"ids": []any{mine["id"], theirs["id"]}, "action": "delete"})
	assertStatus(t, foreign, http.StatusNotFound)
	assertStatus(t, app.bulk(token, map[string]any{"ids": []int{}, "action": "delete"}), http.StatusBadRequest)
	assertStatus(t, app.bulk(token, map[string]any{"ids": []any{mine["id"]}, "action": "explode"}), http.StatusUnprocessableEntity)
	if got := titlesOf(t, app.send(get("/api/bookmarks").as(token))); !reflect.DeepEqual(got, []string{"mine"}) {
		t.Fatalf("titles = %v", got)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	if r := app.send(get("/api/settings").as(token)); r.text != `{"link_check_enabled":false}` {
		t.Fatalf("settings = %s", r.text)
	}
	r := app.send(put("/api/settings", map[string]any{"link_check_enabled": true}).as(token))
	assertStatus(t, r, http.StatusOK)
	if got := app.send(get("/api/settings").as(token)).text; got != `{"link_check_enabled":true}` {
		t.Fatalf("settings = %s", got)
	}
	assertStatus(t, app.send(put("/api/settings", map[string]any{}).as(token)), http.StatusUnprocessableEntity)
}
