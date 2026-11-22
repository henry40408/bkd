package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/henry40408/bkd/internal/linkcheck"
)

const ogPage = `<html><head>
	<title>Plain title</title>
	<meta property="og:title" content="Og title">
	<meta property="og:description" content="Og description">
</head><body></body></html>`

func serveHTML(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))
}

func previewAs(token, address string) sent {
	return post("/api/site-preview", map[string]any{"url": address}).as(token)
}

// fieldsOf encodes name, value pairs as a form body.
func fieldsOf(pairs ...string) string {
	values := url.Values{}
	for i := 0; i < len(pairs); i += 2 {
		values.Set(pairs[i], pairs[i+1])
	}
	return values.Encode()
}

// siteFields are the hidden fields a form sends after its page was read.
func siteFields(address string) []string {
	return []string{"site_url", address, "site_title", "Og title", "site_description", "Og description"}
}

func storedBookmark(t *testing.T, app *testApp, token string, id int64) map[string]any {
	t.Helper()
	for _, item := range app.send(get("/api/bookmarks").as(token)).json()["bookmarks"].([]any) {
		if bookmark := item.(map[string]any); int64(bookmark["id"].(float64)) == id {
			return bookmark
		}
	}
	t.Fatalf("no bookmark %d", id)
	return nil
}

// ---- POST /api/site-preview ----

func TestSitePreviewSaysWhatThePageSaysAboutItself(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	site := serveHTML(ogPage)
	defer site.Close()

	r := app.send(previewAs(token, " "+site.URL+"/article "))
	assertStatus(t, r, http.StatusOK)
	if got := r.json(); got["title"] != "Og title" || got["description"] != "Og description" {
		t.Fatalf("preview = %v", got)
	}
}

func TestSitePreviewOfAPageThatSaysNothingIsAnEmptyAnswerNotAnError(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	site := serveHTML("<html><head></head><body>hello</body></html>")
	defer site.Close()

	r := app.send(previewAs(token, site.URL+"/bare"))
	assertStatus(t, r, http.StatusOK)
	if got := r.json(); got["title"] != "" || got["description"] != "" {
		t.Fatalf("preview = %v", got)
	}
}

func TestSitePreviewSendsTheUsersUserAgent(t *testing.T) {
	app := runningApp(t)
	henry := app.signIn("henry", false)
	mei := app.signIn("mei", false)
	site, seen := userAgentSite()
	defer site.Close()
	assertStatus(t, putSettings(app, henry, map[string]any{"link_check_enabled": false, "link_check_user_agent": "Henry/1"}), http.StatusOK)

	assertStatus(t, app.send(previewAs(henry, site.URL+"/henry")), http.StatusOK)
	assertStatus(t, app.send(previewAs(mei, site.URL+"/mei")), http.StatusOK)
	if got := seen("/henry"); got != "Henry/1" {
		t.Errorf("henry's page was asked with %q", got)
	}
	if got := seen("/mei"); got != linkcheck.DefaultUserAgent() {
		t.Errorf("mei's page was asked with %q", got)
	}
}

func TestSitePreviewExplainsWhyAPageCouldNotBeRead(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	missing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer missing.Close()

	r := app.send(previewAs(token, missing.URL+"/gone"))
	assertStatus(t, r, http.StatusBadGateway)
	if got := r.json()["error"]; got != "could not read the page: HTTP 404" {
		t.Fatalf("error = %v", got)
	}
}

func TestSitePreviewDoesNotContactAPrivateAddress(t *testing.T) {
	// No Allow hook, as in production: loopback is not public.
	app := newTestApp(t, Config{LinkChecker: linkcheck.New(linkcheck.Config{})})
	token := app.signIn("henry", false)
	var hits atomic.Int32
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer site.Close()

	r := app.send(previewAs(token, site.URL+"/inside"))
	assertStatus(t, r, http.StatusBadGateway)
	if got := r.json()["error"]; got != "could not read the page: Address is not public" {
		t.Fatalf("error = %v", got)
	}
	if hits.Load() != 0 {
		t.Fatal("a private address was contacted")
	}
}

func TestSitePreviewRefusesAnAddressThatIsNotAWebAddress(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	for _, address := range []string{"", "not a url", "ftp://example.com/", "javascript:alert(1)"} {
		assertStatus(t, app.send(previewAs(token, address)), http.StatusBadGateway)
	}
}

func TestSitePreviewWaitsForTheUsersRunningCheck(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	var hits atomic.Int32
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer site.Close()
	release, ok := app.app.activeChecks.Start(1)
	if !ok {
		t.Fatal("could not start a check")
	}

	r := app.send(previewAs(token, site.URL+"/busy"))
	assertStatus(t, r, http.StatusTooManyRequests)
	assertEqualText(t, r.headers.Get("Retry-After"), "10")
	if hits.Load() != 0 {
		t.Fatal("the site was contacted while the user's slot was taken")
	}

	release()
	assertStatus(t, app.send(previewAs(token, site.URL+"/free")), http.StatusOK)
}

func TestSitePreviewGivesBackItsSlot(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	site := serveHTML(ogPage)
	defer site.Close()
	for range 2 {
		assertStatus(t, app.send(previewAs(token, site.URL+"/twice")), http.StatusOK)
	}
}

func TestSitePreviewNeedsASessionTheSameOriginAndTheRightRequest(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	site := serveHTML(ogPage)
	defer site.Close()

	assertStatus(t, app.send(post("/api/site-preview", map[string]any{"url": site.URL})), http.StatusUnauthorized)
	assertStatus(t, app.send(previewAs(token, site.URL).originated("http://evil.test")), http.StatusForbidden)
	assertStatus(t, app.send(get("/api/site-preview").as(token)), http.StatusMethodNotAllowed)
	assertStatus(t, app.send(post("/api/site-preview", map[string]any{}).as(token)), http.StatusUnprocessableEntity)
}

func assertEqualText(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// ---- the add form ----

func TestAddFormSavesTheSiteTitleAndDescriptionWhenTheFieldsAreBlank(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	address := "https://example.com/a"

	app.saveThroughForm(token, fieldsOf(append([]string{"url", address, "title", "", "note", "", "tags", ""}, siteFields(address)...)...))
	saved := storedBookmark(t, app, token, 1)
	if saved["title"] != "Og title" || saved["note"] != "Og description" {
		t.Fatalf("title = %v, note = %v", saved["title"], saved["note"])
	}
	if got := saved["site"].(map[string]any); got["title"] != "Og title" || got["description"] != "Og description" {
		t.Fatalf("site = %v", got)
	}
}

func TestAddFormSavesWhatThePersonTypedAndStillKeepsTheSite(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	address := "https://example.com/a"

	app.saveThroughForm(token, fieldsOf(append([]string{"url", address, "title", "  Mine ", "note", "My note", "tags", ""}, siteFields(address)...)...))
	saved := storedBookmark(t, app, token, 1)
	if saved["title"] != "Mine" || saved["note"] != "My note" {
		t.Fatalf("title = %v, note = %v", saved["title"], saved["note"])
	}
	if got := saved["site"].(map[string]any); got["title"] != "Og title" {
		t.Fatalf("site = %v", got)
	}
}

func TestAddFormFallsBackToTheDomainWhenThePageSaidNothing(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)

	app.saveThroughForm(token, fieldsOf("url", "https://example.com/a", "title", "", "note", "", "tags", ""))
	saved := storedBookmark(t, app, token, 1)
	if saved["title"] != "example.com" || saved["note"] != "" || saved["site"] != nil {
		t.Fatalf("saved = %v", saved)
	}
}

func TestAddFormDropsDetailsThatWereReadFromAnotherAddress(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)

	// What a browser without the script sends after the address was edited.
	app.saveThroughForm(token, fieldsOf(append([]string{"url", "https://example.com/new", "title", "", "note", "", "tags", ""},
		siteFields("https://example.com/old")...)...))
	saved := storedBookmark(t, app, token, 1)
	if saved["title"] != "example.com" || saved["note"] != "" || saved["site"] != nil {
		t.Fatalf("saved = %v", saved)
	}
}

func TestAddFormRefusedShowsTheDetailsAgain(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	address := "https://example.com/a"
	tooLongTag := strings.Repeat("t", 51)

	r := app.send(formPost("/bookmarks", fieldsOf(append([]string{"url", address, "title", "", "note", "", "tags", tooLongTag}, siteFields(address)...)...)).as(token))
	assertStatus(t, r, http.StatusBadRequest)
	mustContain(t, r,
		`name="site_url" value="https://example.com/a"`, `name="site_title" value="Og title"`,
		`name="site_description" value="Og description"`,
		`placeholder="Og title"`, `placeholder="Og description"`)
}

// ---- the edit form ----

func TestEditFormShowsTheStoredDetailsAsPlaceholders(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	address := "https://example.com/a"
	app.saveThroughForm(token, fieldsOf(append([]string{"url", address, "title", "Mine", "note", "", "tags", ""}, siteFields(address)...)...))

	r := app.send(get("/bookmarks/1/edit").as(token))
	mustContain(t, r,
		`name="site_url" value="https://example.com/a"`, `name="site_title" value="Og title"`,
		`name="site_description" value="Og description"`,
		`placeholder="Og title"`, `placeholder="Og description"`, `name="title" value="Mine"`)
}

func TestEditFormOfABookmarkWithNoDetailsKeepsTheUsualPlaceholder(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	app.createBookmark(token, "Plain")

	r := app.send(get("/bookmarks/1/edit").as(token))
	mustContain(t, r, `name="site_title" value=""`, `placeholder="Leave blank to use the domain"`)
}

func TestEditFormSavesTheStoredDetailsWhenTheFieldsAreCleared(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	address := "https://example.com/a"
	app.saveThroughForm(token, fieldsOf(append([]string{"url", address, "title", "Mine", "note", "My note", "tags", ""}, siteFields(address)...)...))

	r := app.send(formPost("/bookmarks/1/edit", fieldsOf(append([]string{"url", address, "title", "", "note", "", "tags", "", "back", "/"}, siteFields(address)...)...)).as(token))
	assertStatus(t, r, http.StatusSeeOther)
	saved := storedBookmark(t, app, token, 1)
	if saved["title"] != "Og title" || saved["note"] != "Og description" {
		t.Fatalf("title = %v, note = %v", saved["title"], saved["note"])
	}
}

func TestEditFormKeepsTheStoredDetailsWhileTheAddressStays(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	address := "https://example.com/a"
	app.saveThroughForm(token, fieldsOf(append([]string{"url", address, "title", "Mine", "note", "", "tags", ""}, siteFields(address)...)...))

	// A form sent without any details, as by a client that does not know them.
	r := app.send(formPost("/bookmarks/1/edit", fieldsOf("url", address, "title", "Renamed", "note", "", "tags", "", "back", "/")).as(token))
	assertStatus(t, r, http.StatusSeeOther)
	saved := storedBookmark(t, app, token, 1)
	if got := saved["site"].(map[string]any); got["title"] != "Og title" || got["description"] != "Og description" {
		t.Fatalf("site = %v", got)
	}
}

func TestEditFormReplacesTheDetailsWhenTheAddressChanges(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	app.saveThroughForm(token, fieldsOf(append([]string{"url", "https://example.com/a", "title", "Mine", "note", "", "tags", ""}, siteFields("https://example.com/a")...)...))

	moved := "https://example.com/b"
	r := app.send(formPost("/bookmarks/1/edit", fieldsOf("url", moved, "title", "", "note", "", "tags", "", "back", "/",
		"site_url", moved, "site_title", "Moved title", "site_description", "")).as(token))
	assertStatus(t, r, http.StatusSeeOther)
	saved := storedBookmark(t, app, token, 1)
	if got := saved["site"].(map[string]any); got["title"] != "Moved title" || got["description"] != "" {
		t.Fatalf("site = %v", got)
	}
	if saved["title"] != "Moved title" {
		t.Fatalf("title = %v", saved["title"])
	}
}

func TestEditFormForgetsTheDetailsWhenTheAddressChangesWithoutNewOnes(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	app.saveThroughForm(token, fieldsOf(append([]string{"url", "https://example.com/a", "title", "Mine", "note", "", "tags", ""}, siteFields("https://example.com/a")...)...))

	// What a browser without the script sends: the details of the old address.
	r := app.send(formPost("/bookmarks/1/edit", fieldsOf(append([]string{"url", "https://example.com/b", "title", "", "note", "", "tags", "", "back", "/"},
		siteFields("https://example.com/a")...)...)).as(token))
	assertStatus(t, r, http.StatusSeeOther)
	saved := storedBookmark(t, app, token, 1)
	if saved["title"] != "example.com" || saved["site"] != nil {
		t.Fatalf("saved = %v", saved)
	}
}

func TestEditFormRefusedShowsTheDetailsAgain(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	address := "https://example.com/a"
	app.saveThroughForm(token, fieldsOf(append([]string{"url", address, "title", "Mine", "note", "", "tags", ""}, siteFields(address)...)...))
	tooLongTag := strings.Repeat("t", 51)

	r := app.send(formPost("/bookmarks/1/edit", fieldsOf(append([]string{"url", address, "title", "", "note", "", "tags", tooLongTag, "back", "/"}, siteFields(address)...)...)).as(token))
	assertStatus(t, r, http.StatusBadRequest)
	mustContain(t, r, `name="site_title" value="Og title"`, `placeholder="Og title"`, `placeholder="Og description"`)
}

// ---- the JSON API ----

func TestTheJSONAPIKeepsTheSiteWhileTheAddressStaysAndDropsItWhenItChanges(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	address := "https://example.com/a"
	app.saveThroughForm(token, fieldsOf(append([]string{"url", address, "title", "Mine", "note", "", "tags", ""}, siteFields(address)...)...))
	uri := "/api/bookmarks/" + strconv.Itoa(1)

	kept := app.send(put(uri, bookmarkBody(address, "Renamed")).as(token))
	assertStatus(t, kept, http.StatusOK)
	if got := kept.json()["site"].(map[string]any); got["title"] != "Og title" {
		t.Fatalf("site = %v", got)
	}
	moved := app.send(put(uri, bookmarkBody("https://example.com/b", "Renamed")).as(token))
	assertStatus(t, moved, http.StatusOK)
	if moved.json()["site"] != nil {
		t.Fatalf("site = %v after the address changed", moved.json()["site"])
	}
}

func TestABookmarkMadeThroughTheJSONAPIHasNoSite(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	created := app.createBookmark(token, "Plain")
	if created["site"] != nil {
		t.Fatalf("site = %v", created["site"])
	}
}

// ---- text longer than a bookmark keeps ----

func TestSitePreviewCutsTextToWhatABookmarkKeeps(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	site := serveHTML(`<html><head><title>` + strings.Repeat("語", 301) + `</title>
		<meta name="description" content="` + strings.Repeat("d", 501) + `"></head></html>`)
	defer site.Close()

	got := app.send(previewAs(token, site.URL+"/long")).json()
	if n := len([]rune(got["title"].(string))); n != 300 {
		t.Errorf("title has %d characters", n)
	}
	if n := len([]rune(got["description"].(string))); n != 500 {
		t.Errorf("description has %d characters", n)
	}
}

func TestABlankTitleAndNoteAreSavedEvenWhenAPostedPageTextIsTooLong(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	address := "https://example.com/a"

	// A client can post more than the preview ever answers.
	r := app.send(formPost("/bookmarks", fieldsOf("url", address, "title", "", "note", "", "tags", "",
		"site_url", address, "site_title", strings.Repeat("t", 301), "site_description", strings.Repeat("d", 6000))).as(token))
	assertStatus(t, r, http.StatusSeeOther)
	saved := storedBookmark(t, app, token, 1)
	if n := len([]rune(saved["title"].(string))); n != 300 {
		t.Errorf("title has %d characters", n)
	}
	if n := len([]rune(saved["note"].(string))); n != 500 {
		t.Errorf("note has %d characters", n)
	}
}

func TestPostedDetailsMatchTheAddressWhateverSpacesSurroundIt(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)

	app.saveThroughForm(token, fieldsOf("url", "  https://example.com/a ", "title", "", "note", "", "tags", "",
		"site_url", "https://example.com/a  ", "site_title", "Og title", "site_description", ""))
	saved := storedBookmark(t, app, token, 1)
	if saved["title"] != "Og title" {
		t.Fatalf("title = %v", saved["title"])
	}
}
