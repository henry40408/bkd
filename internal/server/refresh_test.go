package server

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/henry40408/bkd/internal/linkcheck"
)

const describedPage = `<html><head>
	<title>Plain title</title>
	<meta property="og:title" content="Og title">
	<meta property="og:description" content="Og description">
</head><body></body></html>`

func assertEqual[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func pageSite(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))
}

func refreshAs(token string, id int64) sent {
	return post("/api/bookmarks/"+strconv.FormatInt(id, 10)+"/refresh", nil).as(token)
}

func TestRefreshStoresWhatThePageSaysAndShowsItInTheList(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	site := pageSite(describedPage)
	defer site.Close()
	id := app.addBookmarkAt(token, site.URL+"/article")

	r := app.send(refreshAs(token, id))
	assertStatus(t, r, http.StatusOK)
	got := r.json()["site"].(map[string]any)
	if got["title"] != "Og title" || got["description"] != "Og description" || got["fetched_at"].(float64) <= 0 {
		t.Fatalf("site = %v", got)
	}
	if r.json()["title"] != "Page" {
		t.Fatalf("the user's own title changed: %v", r.json()["title"])
	}
	mustContain(t, app.send(get("/api/bookmarks").as(token)), `"site":{"title":"Og title","description":"Og description","fetched_at":`)
}

func TestABookmarkThatWasNeverRefreshedHasNoSite(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	app.createBookmark(token, "Plain")
	mustContain(t, app.send(get("/api/bookmarks").as(token)), `"site":null`)
}

func TestRefreshAnswers404ForAMissingOrForeignBookmark(t *testing.T) {
	app := runningApp(t)
	owner := app.signIn("owner", false)
	other := app.signIn("other", false)
	var hits atomic.Int32
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer site.Close()
	id := app.addBookmarkAt(owner, site.URL+"/mine")

	assertStatus(t, app.send(refreshAs(other, id)), http.StatusNotFound)
	assertStatus(t, app.send(refreshAs(owner, 999)), http.StatusNotFound)
	if hits.Load() != 0 {
		t.Fatal("the site was contacted for a bookmark the caller does not own")
	}
}

func TestRefreshNeedsASession(t *testing.T) {
	app := runningApp(t)
	assertStatus(t, app.send(post("/api/bookmarks/1/refresh", nil)), http.StatusUnauthorized)
}

func TestRefreshRefusesACrossSiteRequestAndTheWrongMethod(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	id := app.addBookmarkAt(token, "https://example.com/a")
	assertStatus(t, app.send(refreshAs(token, id).originated("http://evil.test")), http.StatusForbidden)
	assertStatus(t, app.send(get("/api/bookmarks/1/refresh").as(token)), http.StatusMethodNotAllowed)
}

func TestRefreshRejectsAnIdThatIsNotANumber(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	assertStatus(t, app.send(post("/api/bookmarks/abc/refresh", nil).as(token)), http.StatusBadRequest)
}

func TestRefreshExplainsWhyAPageCouldNotBeRead(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	missing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer missing.Close()
	id := app.addBookmarkAt(token, missing.URL+"/gone")

	r := app.send(refreshAs(token, id))
	assertStatus(t, r, http.StatusBadGateway)
	assertEqual(t, r.json()["error"].(string), "could not read the page: HTTP 404")
	mustContain(t, app.send(get("/api/bookmarks").as(token)), `"site":null`)
}

func TestRefreshDoesNotContactAPrivateAddress(t *testing.T) {
	// No Allow hook, as in production: loopback is not public.
	app := newTestApp(t, Config{LinkChecker: linkcheck.New(linkcheck.Config{})})
	token := app.signIn("henry", false)
	var hits atomic.Int32
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer site.Close()
	id := app.addBookmarkAt(token, site.URL+"/inside")

	r := app.send(refreshAs(token, id))
	assertStatus(t, r, http.StatusBadGateway)
	assertEqual(t, r.json()["error"].(string), "could not read the page: Address is not public")
	if hits.Load() != 0 {
		t.Fatal("a private address was contacted")
	}
}

func TestRefreshSaysWhenThePageHasNothingToRead(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	site := pageSite("<html><head></head><body>hello</body></html>")
	defer site.Close()
	id := app.addBookmarkAt(token, site.URL+"/bare")

	r := app.send(refreshAs(token, id))
	assertStatus(t, r, http.StatusBadGateway)
	assertEqual(t, r.json()["error"].(string), "the page has no title or description")
}

func TestAFailedRefreshKeepsWhatWasStoredBefore(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	var serving atomic.Bool
	serving.Store(true)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !serving.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(describedPage))
	}))
	defer site.Close()
	id := app.addBookmarkAt(token, site.URL+"/flaky")
	assertStatus(t, app.send(refreshAs(token, id)), http.StatusOK)

	serving.Store(false)
	assertStatus(t, app.send(refreshAs(token, id)), http.StatusBadGateway)
	mustContain(t, app.send(get("/api/bookmarks").as(token)), `"title":"Og title"`)
}

func TestRefreshDoesNotStoreAPageReadFromAnAddressThatWasEditedMeanwhile(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	var id int64
	var edited atomic.Bool
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if edited.CompareAndSwap(false, true) {
			moved := app.send(put("/api/bookmarks/"+strconv.FormatInt(id, 10), bookmarkBody("https://example.com/elsewhere", "Page")).as(token))
			assertStatus(t, moved, http.StatusOK)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(describedPage))
	}))
	defer site.Close()
	id = app.addBookmarkAt(token, site.URL+"/before")

	r := app.send(refreshAs(token, id))
	assertStatus(t, r, http.StatusConflict)
	assertEqual(t, r.json()["error"].(string), "the bookmark changed while its page was read")
	mustContain(t, app.send(get("/api/bookmarks").as(token)), `"site":null`)
}

func TestRefreshWaitsForTheUsersRunningCheck(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	var hits atomic.Int32
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer site.Close()
	id := app.addBookmarkAt(token, site.URL+"/busy")
	release, ok := app.app.activeChecks.Start(1)
	if !ok {
		t.Fatal("could not start a check")
	}
	defer release()

	r := app.send(refreshAs(token, id))
	assertStatus(t, r, http.StatusTooManyRequests)
	assertEqual(t, r.headers.Get("Retry-After"), "10")
	assertEqual(t, hits.Load(), int32(0))
}

func TestRefreshGivesBackItsSlot(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	site := pageSite(describedPage)
	defer site.Close()
	id := app.addBookmarkAt(token, site.URL+"/twice")
	for range 2 {
		assertStatus(t, app.send(refreshAs(token, id)), http.StatusOK)
	}
	release, ok := app.app.activeChecks.Start(1)
	if !ok {
		t.Fatal("the slot was not released")
	}
	release()
}

func TestAnEditedAddressForgetsTheSiteUntilTheNextRefresh(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	site := pageSite(describedPage)
	defer site.Close()
	id := app.addBookmarkAt(token, site.URL+"/before")
	assertStatus(t, app.send(refreshAs(token, id)), http.StatusOK)

	moved := app.send(put("/api/bookmarks/"+strconv.FormatInt(id, 10), bookmarkBody(site.URL+"/after", "Page")).as(token))
	assertStatus(t, moved, http.StatusOK)
	if moved.json()["site"] != nil {
		t.Fatalf("site = %v after the address changed", moved.json()["site"])
	}
}

// ---- the page route ----

func refreshForm(token string, id int64, fields string) sent {
	return formPost("/bookmarks/"+strconv.FormatInt(id, 10)+"/refresh", fields).as(token)
}

func TestRefreshFormReturnsToWhereThePersonWasWithANotice(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	site := pageSite(describedPage)
	defer site.Close()
	id := app.addBookmarkAt(token, site.URL+"/form")

	r := app.send(refreshForm(token, id, "back=%2F%3Fq%3Dgo"))
	assertStatus(t, r, http.StatusSeeOther)
	assertEqual(t, locationOf(r), "/?q=go&notice=site-refreshed")
	mustContain(t, app.send(get("/").as(token)), `<p class="site-desc">Og description</p>`)
}

func TestEditPageOffersTheRefreshedPageTitle(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	site := pageSite(describedPage)
	defer site.Close()
	id := app.addBookmarkAt(token, site.URL+"/edit")
	editPath := "/bookmarks/" + strconv.FormatInt(id, 10) + "/edit"

	if strings.Contains(app.send(get(editPath).as(token)).text, "data-site-title") {
		t.Fatal("a page title is offered before any refresh")
	}
	assertStatus(t, app.send(refreshAs(token, id)), http.StatusOK)
	mustContain(t, app.send(get(editPath).as(token)), `data-site-title="Og title"`)
}

func TestRefreshFormNeverLeavesTheSite(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	site := pageSite(describedPage)
	defer site.Close()
	id := app.addBookmarkAt(token, site.URL+"/form")
	for _, back := range []string{"", "https%3A%2F%2Fevil.test%2F", "%2F%2Fevil.test"} {
		r := app.send(refreshForm(token, id, "back="+back))
		assertEqual(t, locationOf(r), "/?notice=site-refreshed")
	}
}

func TestRefreshFormShowsAnErrorPageThatSaysWhy(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	missing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer missing.Close()
	id := app.addBookmarkAt(token, missing.URL+"/gone")

	r := app.send(refreshForm(token, id, "back=%2F"))
	assertStatus(t, r, http.StatusBadGateway)
	mustContain(t, r, "could not read the page: HTTP 404")
}

func TestRefreshFormAnswers404ForAMissingOrForeignBookmark(t *testing.T) {
	app := runningApp(t)
	owner := app.signIn("owner", false)
	other := app.signIn("other", false)
	id := app.addBookmarkAt(owner, "https://example.com/mine")
	assertStatus(t, app.send(refreshForm(other, id, "back=%2F")), http.StatusNotFound)
	assertStatus(t, app.send(refreshForm(owner, 999, "back=%2F")), http.StatusNotFound)
}

func TestRefreshFormNeedsASessionAndTheSameOrigin(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	id := app.addBookmarkAt(token, "https://example.com/a")
	uri := "/bookmarks/" + strconv.FormatInt(id, 10) + "/refresh"

	anonymous := app.send(formPost(uri, "back=%2F"))
	mustRedirect(t, anonymous, "/login")
	foreign := app.send(formPost(uri, "back=%2F").as(token).originated("http://evil.test"))
	assertStatus(t, foreign, http.StatusForbidden)
	if strings.Contains(foreign.text, "Og title") {
		t.Fatal("a cross-site form refreshed the bookmark")
	}
}

func TestRefreshFormSaysWhenACheckIsRunning(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	id := app.addBookmarkAt(token, "https://example.com/a")
	release, ok := app.app.activeChecks.Start(1)
	if !ok {
		t.Fatal("could not start a check")
	}
	defer release()
	r := app.send(refreshForm(token, id, "back=%2F"))
	assertStatus(t, r, http.StatusTooManyRequests)
	assertEqual(t, r.headers.Get("Retry-After"), "10")
}
