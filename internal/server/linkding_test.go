package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/bookmarks"
)

// linkdingUser makes a user with a token. Neither needs Argon2 or a session, which
// the server tests cannot afford many of under the race detector.
func (a *testApp) linkdingUser(username string) (token string, userID int64) {
	a.t.Helper()
	err := a.db.QueryRow(
		"INSERT INTO users (username, password_hash, is_admin, created_at) VALUES (?1, 'unused', 0, 0) RETURNING id",
		username).Scan(&userID)
	if err != nil {
		a.t.Fatal(err)
	}
	issued, ok, err := a.app.apiTokens.Create(context.Background(), auth.NewAPIToken{UserID: userID, Name: "test", Now: auth.UnixNow()})
	if err != nil || !ok {
		a.t.Fatalf("token: %v, %v", ok, err)
	}
	return issued.Token, userID
}

func (a *testApp) saveBookmark(userID int64, input bookmarks.Input, now int64) bookmarks.Bookmark {
	a.t.Helper()
	valid, err := input.Validate()
	if err != nil {
		a.t.Fatal(err)
	}
	saved, err := a.app.bookmarks.Create(context.Background(), userID, valid, now)
	if err != nil {
		a.t.Fatal(err)
	}
	return saved
}

func withToken(request sent, token string) sent {
	request.headers = map[string]string{"Authorization": "Token " + token}
	return request
}

func (r reply) results() []map[string]any {
	items, _ := r.json()["results"].([]any)
	out := make([]map[string]any, len(items))
	for i, item := range items {
		out[i], _ = item.(map[string]any)
	}
	return out
}

func resultIDs(r reply) []float64 {
	ids := []float64{}
	for _, item := range r.results() {
		id, _ := item["id"].(float64)
		ids = append(ids, id)
	}
	return ids
}

func TestLinkdingRoutesAnswerOnlyToATokenInLinkdingsWords(t *testing.T) {
	app := runningApp(t)
	token, _ := app.linkdingUser("henry")
	cases := []struct {
		name   string
		header string
		detail string
	}{
		{"no header", "", messageNoCredentials},
		{"another scheme", "Basic " + token, messageNoCredentials},
		{"a keyword that is only a prefix", "Tokens " + token, messageNoCredentials},
		{"no token", "Token", messageNoToken},
		{"spaces in the token", "Token " + token + " more", messageSpacedToken},
		{"a wrong token", "Token " + strings.Repeat("0", 64), messageBadToken},
	}
	for _, c := range cases {
		request := get("/api/bookmarks/")
		if c.header != "" {
			request.headers = map[string]string{"Authorization": c.header}
		}
		r := app.send(request)
		assertStatus(t, r, http.StatusUnauthorized)
		if r.json()["detail"] != c.detail || r.headers.Get("WWW-Authenticate") != "Token" {
			t.Errorf("%s: %q, WWW-Authenticate %q", c.name, r.text, r.headers.Get("WWW-Authenticate"))
		}
	}
}

// linkding takes "Bearer" as well as "Token", and clients send either.
func TestLinkdingKeywordIsTokenOrBearerInAnyCase(t *testing.T) {
	app := runningApp(t)
	token, _ := app.linkdingUser("henry")
	for _, keyword := range []string{"Token", "tOkEn", "Bearer", "BEARER"} {
		request := get("/api/bookmarks/")
		request.headers = map[string]string{"Authorization": keyword + "  " + token}
		assertStatus(t, app.send(request), http.StatusOK)
	}
}

func TestLinkdingRoutesAnswerHEADLikeGET(t *testing.T) {
	app := runningApp(t)
	token, _ := app.linkdingUser("henry")
	request := withToken(get("/api/tags/"), token)
	request.method = http.MethodHead
	assertStatus(t, app.send(request), http.StatusOK)
}

// A cookie must not open these routes: they skip the same-origin check, which is
// what protects a cookie.
func TestLinkdingRoutesRefuseASessionCookie(t *testing.T) {
	app := runningApp(t)
	session := app.sessionFor("henry")
	for _, path := range []string{"/api/bookmarks/", "/api/bookmarks/1/", "/api/bookmarks/check/?url=https://a.b", "/api/tags/"} {
		assertStatus(t, app.send(get(path).as(session)), http.StatusUnauthorized)
	}
}

func TestLinkdingRoutesRefuseAForwardedUser(t *testing.T) {
	app := newTestApp(t, forwardAuthConfig(t))
	assertStatus(t, app.send(viaProxy(get("/api/bookmarks/"), "alice")), http.StatusUnauthorized)
}

func TestLinkdingRoutesIgnoreTheOriginOfAnExtension(t *testing.T) {
	app := runningApp(t)
	token, _ := app.linkdingUser("henry")
	r := app.send(withToken(get("/api/bookmarks/"), token).originated("chrome-extension://abcdef"))
	assertStatus(t, r, http.StatusOK)
}

func TestLinkdingAnswersAreNeverCachedAndAreJSON(t *testing.T) {
	app := runningApp(t)
	token, _ := app.linkdingUser("henry")
	for _, path := range []string{"/api/bookmarks/", "/api/tags/"} {
		r := app.send(withToken(get(path), token))
		if r.headers.Get("Cache-Control") != "no-store" || r.headers.Get("Content-Type") != "application/json" {
			t.Errorf("%s: headers = %v", path, r.headers)
		}
	}
}

func TestOtherMethodsOfALinkdingRouteAreRefused(t *testing.T) {
	app := runningApp(t)
	token, _ := app.linkdingUser("henry")
	for _, tc := range []struct {
		request sent
		allow   string
	}{
		{del("/api/bookmarks/"), "GET,HEAD,POST"},
		{post("/api/bookmarks/1/", map[string]any{}), "GET,HEAD,PUT,PATCH,DELETE"},
		{post("/api/bookmarks/check/", map[string]any{}), "GET,HEAD"},
		{post("/api/tags/", map[string]any{}), "GET,HEAD"},
	} {
		r := app.send(withToken(tc.request, token))
		assertStatus(t, r, http.StatusMethodNotAllowed)
		if r.headers.Get("Allow") != tc.allow {
			t.Errorf("%s %s: Allow = %q, want %q", tc.request.method, tc.request.uri, r.headers.Get("Allow"), tc.allow)
		}
	}
}

func TestLinkdingBookmarkUsesLinkdingsFieldsWithBkdsMeaning(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	app.saveBookmark(user, bookmarks.Input{
		URL: "https://example.com/a", Title: "My title", Note: "my own words", Tags: []string{"go", "web"},
		Site: bookmarks.Site{Title: "Page title", Description: "What the page says"},
	}, 1_600_000_000)

	r := app.send(withToken(get("/api/bookmarks/"), token))
	assertStatus(t, r, http.StatusOK)
	body := r.json()
	if body["count"] != float64(1) || body["next"] != nil || body["previous"] != nil {
		t.Fatalf("envelope = %v", body)
	}
	got := r.results()[0]
	want := map[string]any{
		"url": "https://example.com/a", "title": "My title",
		"description": "What the page says", "notes": "my own words",
		"web_archive_snapshot_url": "", "favicon_url": nil, "preview_image_url": nil,
		"is_archived": false, "unread": false, "shared": false,
		"date_added": "2020-09-13T12:26:40.000000Z", "date_modified": "2020-09-13T12:26:40.000000Z",
		"website_title": "Page title", "website_description": "What the page says",
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %#v, want %#v", key, got[key], value)
		}
	}
	tags, _ := got["tag_names"].([]any)
	if len(tags) != 2 || tags[0] != "go" || tags[1] != "web" {
		t.Errorf("tag_names = %v", got["tag_names"])
	}
	if len(got) != len(want)+2 {
		t.Errorf("fields = %v", got)
	}
}

func TestLinkdingBookmarkWithoutSiteDetailsHasEmptyDescriptionAndNullWebsiteFields(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	app.saveBookmark(user, bookmarks.Input{URL: "https://example.com/a", Title: "t"}, 1)
	got := app.send(withToken(get("/api/bookmarks/"), token)).results()[0]
	if got["description"] != "" || got["notes"] != "" || got["website_title"] != nil || got["website_description"] != nil {
		t.Fatalf("bookmark = %v", got)
	}
	if tags, ok := got["tag_names"].([]any); !ok || len(tags) != 0 {
		t.Fatalf("tag_names = %#v", got["tag_names"])
	}
}

func TestLinkdingListIsNewestFirstAndOnlyTheOwners(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	_, other := app.linkdingUser("other")
	first := app.saveBookmark(user, bookmarks.Input{URL: "https://example.com/1", Title: "one"}, 100)
	second := app.saveBookmark(user, bookmarks.Input{URL: "https://example.com/2", Title: "two"}, 200)
	app.saveBookmark(other, bookmarks.Input{URL: "https://example.com/3", Title: "theirs"}, 300)

	r := app.send(withToken(get("/api/bookmarks/"), token))
	assertIDs(t, r, second.ID, first.ID)
	if r.json()["count"] != float64(2) {
		t.Fatalf("count = %v", r.json()["count"])
	}
}

func assertIDs(t *testing.T, r reply, want ...int64) {
	t.Helper()
	got := resultIDs(r)
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != float64(want[i]) {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}
}

func TestLinkdingEmptyListHasNoResultsAndNoLinks(t *testing.T) {
	app := runningApp(t)
	token, _ := app.linkdingUser("henry")
	r := app.send(withToken(get("/api/bookmarks/"), token))
	if r.text != `{"count":0,"next":null,"previous":null,"results":[]}` {
		t.Fatalf("body = %s", r.text)
	}
}

func fiveBookmarks(app *testApp, user int64) []bookmarks.Bookmark {
	saved := []bookmarks.Bookmark{}
	for i := range 5 {
		saved = append(saved, app.saveBookmark(user, bookmarks.Input{
			URL: "https://example.com/" + string(rune('a'+i)), Title: "b" + string(rune('a'+i)),
		}, int64(100+i)))
	}
	return saved // oldest first; the list is newest first
}

func TestLinkdingPagesLinkToEachOtherWithTheOtherParametersKept(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	saved := fiveBookmarks(app, user)

	first := app.send(withToken(get("/api/bookmarks/?limit=2&q=b"), token))
	assertIDs(t, first, saved[4].ID, saved[3].ID)
	body := first.json()
	if body["count"] != float64(5) || body["previous"] != nil ||
		body["next"] != "http://bkd.test/api/bookmarks/?limit=2&offset=2&q=b" {
		t.Fatalf("first page = %v", body)
	}

	middle := app.send(withToken(get("/api/bookmarks/?limit=2&offset=2&q=b"), token))
	assertIDs(t, middle, saved[2].ID, saved[1].ID)
	body = middle.json()
	if body["next"] != "http://bkd.test/api/bookmarks/?limit=2&offset=4&q=b" ||
		body["previous"] != "http://bkd.test/api/bookmarks/?limit=2&q=b" {
		t.Fatalf("middle page = %v", body)
	}

	last := app.send(withToken(get("/api/bookmarks/?limit=2&offset=4&q=b"), token))
	assertIDs(t, last, saved[0].ID)
	body = last.json()
	if body["next"] != nil || body["previous"] != "http://bkd.test/api/bookmarks/?limit=2&offset=2&q=b" {
		t.Fatalf("last page = %v", body)
	}
}

func TestLinkdingPageThatEndsExactlyAtTheCountHasNoNext(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	fiveBookmarks(app, user)
	body := app.send(withToken(get("/api/bookmarks/?limit=5"), token)).json()
	if body["next"] != nil {
		t.Fatalf("next = %v", body["next"])
	}
	body = app.send(withToken(get("/api/bookmarks/?limit=4"), token)).json()
	if body["next"] == nil {
		t.Fatal("no next although one bookmark is left")
	}
}

func TestLinkdingPastTheEndIsAnEmptyPageWithAWayBack(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	fiveBookmarks(app, user)
	body := app.send(withToken(get("/api/bookmarks/?limit=2&offset=40"), token)).json()
	results, _ := body["results"].([]any)
	if len(results) != 0 || body["count"] != float64(5) || body["next"] != nil ||
		body["previous"] != "http://bkd.test/api/bookmarks/?limit=2&offset=38" {
		t.Fatalf("body = %v", body)
	}
}

func TestLinkdingLinksAreHTTPSWhenTheSiteIsServedOverIt(t *testing.T) {
	app := newTestApp(t, Config{SecureCookies: true})
	token, user := app.linkdingUser("henry")
	fiveBookmarks(app, user)
	next, _ := app.send(withToken(get("/api/bookmarks/?limit=2"), token)).json()["next"].(string)
	if !strings.HasPrefix(next, "https://bkd.test/api/bookmarks/") {
		t.Fatalf("next = %q", next)
	}
}

func TestLinkdingPagingReadsLimitAndOffsetLikeDjangoRESTFramework(t *testing.T) {
	cases := []struct {
		query         string
		max           uint32
		limit, offset uint32
	}{
		{"", 1000, 100, 0},
		{"limit=7&offset=3", 1000, 7, 3},
		{"limit=abc&offset=abc", 1000, 100, 0},
		{"limit=0", 1000, 100, 0},
		{"limit=-5&offset=-5", 1000, 100, 0},
		{"limit=1000", 1000, 1000, 0},
		{"limit=1001", 1000, 1000, 0},
		{"limit=5000", 5000, 5000, 0},
		{"limit=5001", 5000, 5000, 0},
		{"limit=99999999999", 1000, 1000, 0},
		{"limit=99999999999999999999999", 1000, 1000, 0},
		{"offset=99999999999", 1000, 100, 4294967295},
		{"offset=99999999999999999999999", 1000, 100, 4294967295},
		{"offset=4294967295", 1000, 100, 4294967295},
		{"offset=4294967294", 1000, 100, 4294967294},
	}
	for _, c := range cases {
		request := httptest.NewRequest(http.MethodGet, "/api/bookmarks/?"+c.query, nil)
		got := linkdingPagingOf(request, c.max)
		if got.limit != c.limit || got.offset != c.offset {
			t.Errorf("%q: limit %d offset %d, want %d and %d", c.query, got.limit, got.offset, c.limit, c.offset)
		}
	}
}

func TestLinkdingTagsAllowTheLimitTheBrowserExtensionAsksFor(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	tags := make([]string, 0, 12)
	for _, name := range "abcdefghijkl" {
		tags = append(tags, string(name))
	}
	app.saveBookmark(user, bookmarks.Input{URL: "https://example.com/1", Title: "1", Tags: tags[:10]}, 1)
	app.saveBookmark(user, bookmarks.Input{URL: "https://example.com/2", Title: "2", Tags: tags[10:]}, 2)
	r := app.send(withToken(get("/api/tags/?limit=5000"), token))
	if len(r.results()) != 12 || r.json()["next"] != nil {
		t.Fatalf("tags = %s", r.text)
	}
	// Twelve tags do not reach the cap, so the link back shows which limit was
	// kept: 5000 for tags, where bookmarks would have cut it to 1000.
	r = app.send(withToken(get("/api/tags/?limit=5000&offset=1"), token))
	previous, _ := r.json()["previous"].(string)
	if !strings.Contains(previous, "limit=5000") {
		t.Fatalf("previous = %q", previous)
	}
}

func TestLinkdingLinksUseHTTPSWhenAProxySaysTheRequestWasSecure(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	fiveBookmarks(app, user)
	cases := map[string]string{"https": "https://", "HTTPS, http": "https://", "http": "http://", "": "http://", "gopher": "http://"}
	for forwarded, prefix := range cases {
		request := withToken(get("/api/bookmarks/?limit=2"), token)
		if forwarded != "" {
			request.headers["X-Forwarded-Proto"] = forwarded
		}
		next, _ := app.send(request).json()["next"].(string)
		if !strings.HasPrefix(next, prefix+"bkd.test/") {
			t.Errorf("X-Forwarded-Proto %q: next = %q, want %s…", forwarded, next, prefix)
		}
	}
}

func TestLinkdingNextLinkKeepsAQueryThatNeedsEncoding(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	for i := range 3 {
		app.saveBookmark(user, bookmarks.Input{URL: "https://example.com/" + strconv.Itoa(i), Title: "a&b#c+d " + strconv.Itoa(i)}, int64(i))
	}
	r := app.send(withToken(get("/api/bookmarks/?limit=1&q=a%26b%23c%2Bd"), token))
	if r.json()["count"] != float64(3) || r.json()["next"] != "http://bkd.test/api/bookmarks/?limit=1&offset=1&q=a%26b%23c%2Bd" {
		t.Fatalf("body = %s", r.text)
	}
}

func TestLinkdingListsBkdHasNothingForAreEmptyNotMissing(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	fiveBookmarks(app, user)
	for _, path := range []string{"/api/bookmarks/archived/", "/api/bookmarks/shared/"} {
		r := app.send(withToken(get(path), token))
		assertStatus(t, r, http.StatusOK)
		if r.text != `{"count":0,"next":null,"previous":null,"results":[]}` {
			t.Errorf("%s: body = %s", path, r.text)
		}
		assertStatus(t, app.send(get(path)), http.StatusUnauthorized)
	}
}

func TestLinkdingSearchUsesBkdsSyntax(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	app.saveBookmark(user, bookmarks.Input{URL: "https://example.com/go", Title: "Gopher", Tags: []string{"lang"}}, 1)
	other := app.saveBookmark(user, bookmarks.Input{URL: "https://example.com/rs", Title: "Crab", Tags: []string{"lang"}}, 2)
	r := app.send(withToken(get("/api/bookmarks/?q=crab"), token))
	assertIDs(t, r, other.ID)
	r = app.send(withToken(get("/api/bookmarks/?q=tag:lang+-gopher"), token))
	assertIDs(t, r, other.ID)
	if r.json()["count"] != float64(1) {
		t.Fatalf("count = %v", r.json()["count"])
	}
}

func TestLinkdingTimeBoundsAreStrictlyAfter(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	old := app.saveBookmark(user, bookmarks.Input{URL: "https://example.com/old", Title: "old"}, 1_600_000_000)
	fresh := app.saveBookmark(user, bookmarks.Input{URL: "https://example.com/new", Title: "new"}, 1_600_000_100)
	list := func(query string) reply {
		return app.send(withToken(get("/api/bookmarks/?"+query), token))
	}
	assertIDs(t, list("added_since=2020-09-13T12:26:39Z"), fresh.ID, old.ID)
	assertIDs(t, list("added_since=2020-09-13T12:26:40Z"), fresh.ID)
	assertIDs(t, list("added_since=2020-09-13T12:28:20Z"))
	assertIDs(t, list("modified_since=2020-09-13T12:26:40Z"), fresh.ID)
	assertIDs(t, list("modified_since=2020-09-13T12:26:40Z&added_since=2020-09-13T12:26:39Z"), fresh.ID)
}

func TestLinkdingTimeBoundsTakeTheSpellingsClientsSend(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	// One bookmark, saved at 2020-09-13T12:26:40Z. Each bound is one second
	// before it, so the bookmark is found only if the spelling was read right.
	saved := app.saveBookmark(user, bookmarks.Input{URL: "https://example.com/a", Title: "a"}, 1_600_000_000)
	for _, bound := range []string{
		"2020-09-13T12:26:39.999999Z", // what linkding writes
		"2020-09-13T12:26:39Z",
		"2020-09-13T14:26:39%2B02:00", // another zone
		"2020-09-13T12:26:39",         // no zone is UTC
		"2020-09-13%2012:26:39",       // a space in place of the T
		"2020-09-13T12:26:39.5Z",      // a fraction is rounded down
		"2020-09-13",                  // a date is its midnight in UTC
		"2020-09-13T12:26",            // no seconds
		"2020-09-13%2012:26",          // no seconds, a space for the T
		"2020-09-13T14:26:39%2B0200",  // an offset without a colon
		"2020-09-13T14:26:39+02:00",   // a "+" that was not encoded reads as a space
		"2020-09-13T14:26:39.5+02:00", // the same with a fraction
		"2020-09-13%2014:26:39+02:00", // the same with a space for the T
	} {
		r := app.send(withToken(get("/api/bookmarks/?added_since="+bound), token))
		assertStatus(t, r, http.StatusOK)
		assertIDs(t, r, saved.ID)
	}
	for _, bound := range []string{
		"2020-09-13T12:26:40Z", "2020-09-13T14:26:40%2B02:00", "2020-09-14", "2020-09-13T14:26:40+02:00",
	} {
		r := app.send(withToken(get("/api/bookmarks/?added_since="+bound), token))
		assertStatus(t, r, http.StatusOK)
		assertIDs(t, r)
	}
}

func TestLinkdingRefusesATimeBoundItCannotRead(t *testing.T) {
	app := runningApp(t)
	token, _ := app.linkdingUser("henry")
	for _, name := range []string{"added_since", "modified_since"} {
		r := app.send(withToken(get("/api/bookmarks/?"+name+"=yesterday"), token))
		assertStatus(t, r, http.StatusBadRequest)
		if r.json()["detail"] != name+": Enter a valid date/time." {
			t.Errorf("%s: body = %s", name, r.text)
		}
	}
	assertStatus(t, app.send(withToken(get("/api/bookmarks/?added_since=&modified_since="), token)), http.StatusOK)
}

func TestLinkdingGetsOneBookmark(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	_, other := app.linkdingUser("other")
	mine := app.saveBookmark(user, bookmarks.Input{URL: "https://example.com/a", Title: "mine", Tags: []string{"x"}}, 5)
	theirs := app.saveBookmark(other, bookmarks.Input{URL: "https://example.com/b", Title: "theirs"}, 5)

	r := app.send(withToken(get("/api/bookmarks/"+strconv.FormatInt(mine.ID, 10)+"/"), token))
	assertStatus(t, r, http.StatusOK)
	if got := r.json(); got["id"] != float64(mine.ID) || got["title"] != "mine" {
		t.Fatalf("bookmark = %v", got)
	}
	for _, path := range []string{
		"/api/bookmarks/" + strconv.FormatInt(theirs.ID, 10) + "/", "/api/bookmarks/99999/", "/api/bookmarks/abc/",
		"/api/bookmarks/0/", "/api/bookmarks/-1/", "/api/bookmarks/99999999999999999999/",
	} {
		r := app.send(withToken(get(path), token))
		assertStatus(t, r, http.StatusNotFound)
		if r.json()["detail"] != messageNotFound {
			t.Errorf("%s: body = %s", path, r.text)
		}
	}
}

func TestLinkdingListsTagsWithMadeUpIDsAndTheDateOfTheirOldestBookmark(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	_, other := app.linkdingUser("other")
	app.saveBookmark(user, bookmarks.Input{URL: "https://example.com/1", Title: "1", Tags: []string{"go", "web"}}, 1_600_000_100)
	app.saveBookmark(user, bookmarks.Input{URL: "https://example.com/2", Title: "2", Tags: []string{"go"}}, 1_600_000_000)
	app.saveBookmark(other, bookmarks.Input{URL: "https://example.com/3", Title: "3", Tags: []string{"private"}}, 1)

	r := app.send(withToken(get("/api/tags/"), token))
	assertStatus(t, r, http.StatusOK)
	if r.json()["count"] != float64(2) || r.json()["next"] != nil || r.json()["previous"] != nil {
		t.Fatalf("envelope = %v", r.json())
	}
	tags := r.results()
	if len(tags) != 2 || tags[0]["name"] != "go" || tags[1]["name"] != "web" {
		t.Fatalf("tags = %v", tags)
	}
	if tags[0]["date_added"] != "2020-09-13T12:26:40.000000Z" || tags[1]["date_added"] != "2020-09-13T12:28:20.000000Z" {
		t.Fatalf("dates = %v", tags)
	}
	if tags[0]["id"] == tags[1]["id"] || tags[0]["id"] == nil {
		t.Fatalf("ids = %v", tags)
	}
	if strings.Contains(r.text, "private") {
		t.Fatal("another user's tag is listed")
	}
}

func TestLinkdingTagsArePaged(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	app.saveBookmark(user, bookmarks.Input{URL: "https://example.com/1", Title: "1", Tags: []string{"a", "b", "c"}}, 1)
	r := app.send(withToken(get("/api/tags/?limit=2"), token))
	if r.json()["next"] != "http://bkd.test/api/tags/?limit=2&offset=2" || len(r.results()) != 2 {
		t.Fatalf("first page = %s", r.text)
	}
	r = app.send(withToken(get("/api/tags/?limit=2&offset=2"), token))
	if r.json()["next"] != nil || r.json()["previous"] != "http://bkd.test/api/tags/?limit=2" || len(r.results()) != 1 {
		t.Fatalf("second page = %s", r.text)
	}
}

func TestLinkdingCheckFindsABookmarkedAddress(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	saved := app.saveBookmark(user, bookmarks.Input{
		URL: "https://example.com/a?x=1", Title: "mine",
		Site: bookmarks.Site{Title: "Page title", Description: "About the page"},
	}, 5)

	r := app.send(withToken(get("/api/bookmarks/check/?url=https%3A%2F%2Fexample.com%2Fa%3Fx%3D1"), token))
	assertStatus(t, r, http.StatusOK)
	body := r.json()
	bookmark, _ := body["bookmark"].(map[string]any)
	metadata, _ := body["metadata"].(map[string]any)
	if bookmark["id"] != float64(saved.ID) {
		t.Fatalf("bookmark = %v", bookmark)
	}
	if metadata["url"] != "https://example.com/a?x=1" || metadata["title"] != "Page title" ||
		metadata["description"] != "About the page" || metadata["preview_image"] != nil {
		t.Fatalf("metadata = %v", metadata)
	}
	if tags, ok := body["auto_tags"].([]any); !ok || len(tags) != 0 {
		t.Fatalf("auto_tags = %#v", body["auto_tags"])
	}
}

// A browser shows https://example.com/ for a page saved as https://example.com.
func TestLinkdingCheckIgnoresATrailingSlash(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	saved := app.saveBookmark(user, bookmarks.Input{URL: "https://example.com", Title: "root"}, 5)
	for _, address := range []string{"https://example.com/", "https://example.com"} {
		r := app.send(withToken(get("/api/bookmarks/check/?url="+address), token))
		bookmark, _ := r.json()["bookmark"].(map[string]any)
		if bookmark["id"] != float64(saved.ID) {
			t.Errorf("%s: body = %s", address, r.text)
		}
	}
}

func TestLinkdingCheckOfAnUnknownAddressHasNoBookmarkAndNoMetadata(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	_, other := app.linkdingUser("other")
	app.saveBookmark(other, bookmarks.Input{URL: "https://example.com/theirs", Title: "theirs"}, 5)
	app.saveBookmark(user, bookmarks.Input{URL: "https://example.com/mine", Title: "mine"}, 5)

	for _, address := range []string{"https://example.com/theirs", "https://example.com/min", "https://example.com/mine/x"} {
		r := app.send(withToken(get("/api/bookmarks/check/?url="+address), token))
		assertStatus(t, r, http.StatusOK)
		want := `{"bookmark":null,"metadata":{"url":"` + address +
			`","title":null,"description":null,"preview_image":null},"auto_tags":[]}`
		if r.text != want {
			t.Errorf("%s: body = %s", address, r.text)
		}
	}
}

func TestLinkdingCheckNeedsAnAddress(t *testing.T) {
	app := runningApp(t)
	token, _ := app.linkdingUser("henry")
	for _, path := range []string{"/api/bookmarks/check/", "/api/bookmarks/check/?url=", "/api/bookmarks/check/?url=%20"} {
		r := app.send(withToken(get(path), token))
		assertStatus(t, r, http.StatusBadRequest)
		if r.json()["detail"] != "url: This field is required." {
			t.Errorf("%s: body = %s", path, r.text)
		}
	}
}

func TestLinkdingRoutesAnswer500WithoutTheCauseWhenTheDatabaseFails(t *testing.T) {
	app := runningApp(t)
	token, _ := app.linkdingUser("henry")
	app.breakTable("bookmark_tags")
	app.breakTable("bookmarks")
	for _, path := range []string{"/api/bookmarks/", "/api/bookmarks/1/", "/api/bookmarks/check/?url=https://a.b", "/api/tags/"} {
		r := app.send(withToken(get(path), token))
		if r.status != http.StatusInternalServerError || r.json()["detail"] != "internal error" {
			t.Errorf("%s: %d %s", path, r.status, r.text)
		}
	}
}

func TestLinkdingTokenStopsWorkingWhenItIsRevoked(t *testing.T) {
	app := runningApp(t)
	token, user := app.linkdingUser("henry")
	assertStatus(t, app.send(withToken(get("/api/tags/"), token)), http.StatusOK)
	app.exec("DELETE FROM api_tokens WHERE user_id = ?1", user)
	assertStatus(t, app.send(withToken(get("/api/tags/"), token)), http.StatusUnauthorized)
}

func TestTheOtherAPIRoutesStillAnswerTheCookieSession(t *testing.T) {
	app := runningApp(t)
	session := app.sessionFor("henry")
	assertStatus(t, app.send(get("/api/bookmarks").as(session)), http.StatusOK)
	assertStatus(t, app.send(get("/api/tags").as(session)), http.StatusOK)
	assertStatus(t, app.send(get("/api/bookmarks/random").as(session)), http.StatusNotFound)
	assertStatus(t, app.send(post("/api/bookmarks/check", map[string]any{"ids": []int{}}).as(session)), http.StatusBadRequest)
}

func TestLinkdingLogsTheCauseOfAFaultButOnlyThen(t *testing.T) {
	app, log := loggedApp(t)
	app.app.writeDetailError(httptest.NewRecorder(), unauthenticated(messageBadToken))
	app.app.writeDetailError(httptest.NewRecorder(), detailNotFound())
	if log.Len() != 0 {
		t.Fatalf("a client error was logged as a fault: %s", log)
	}

	recorder := httptest.NewRecorder()
	app.app.writeDetailError(recorder, internal(errors.New("disk on fire")))
	if !strings.Contains(log.String(), "disk on fire") {
		t.Errorf("the cause was not logged: %s", log)
	}
	if strings.Contains(recorder.Body.String(), "disk on fire") {
		t.Errorf("the cause reached the client: %s", recorder.Body)
	}
}
