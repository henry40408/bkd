package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/bookmarks"
	"github.com/henry40408/bkd/internal/linkcheck"
	"github.com/henry40408/bkd/internal/sitemeta"
)

const ogResult = `{"title":"Og title","description":"Og description"}`

func readAs(token string, ids ...int) sent {
	if ids == nil {
		ids = []int{}
	}
	return post("/api/bookmarks/read", map[string]any{"ids": ids}).as(token)
}

// streamEvent is one event as the stream writes it.
func streamEvent(name, data string) string {
	return "event: " + name + "\ndata: " + data + "\n\n"
}

func assertEvents(t *testing.T, r reply, want string) {
	t.Helper()
	if got := strings.Join(eventsOf(r.text), ","); got != want {
		t.Fatalf("events %s, want %s, in %q", got, want, r.text)
	}
}

func assertSiteOf(t *testing.T, saved map[string]any, title, description string) {
	t.Helper()
	got, ok := saved["site"].(map[string]any)
	if !ok || got["title"] != title || got["description"] != description {
		t.Fatalf("site = %v, want %q and %q", saved["site"], title, description)
	}
}

func assertSlotIsFree(t *testing.T, app *testApp) {
	t.Helper()
	release, ok := app.app.activeChecks.Start(1)
	if !ok {
		t.Fatal("the user's slot is still taken")
	}
	release()
}

func TestReadStreamsAResultPerPageThenDone(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	site := serveHTML(ogPage)
	defer site.Close()
	app.addBookmarkAt(token, site.URL+"/a")
	app.addBookmarkAt(token, site.URL+"/b")

	r := app.send(readAs(token, 1, 2))
	assertStatus(t, r, http.StatusOK)
	assertEvents(t, r, "result,result,done")
	mustContain(t, r,
		streamEvent("result", `{"id":1,"site":`+ogResult+`,"note_filled":true}`),
		streamEvent("result", `{"id":2,"site":`+ogResult+`,"note_filled":true}`),
		streamEvent("done", "{}"))
	if got := r.headers.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("content type %q", got)
	}
	assertSlotIsFree(t, app)
}

func TestReadStoresTheDetailsAndFillsABlankNoteOnly(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	site := serveHTML(ogPage)
	defer site.Close()
	app.addBookmarkAt(token, site.URL+"/")
	app.exec("UPDATE bookmarks SET updated_at = 1000")

	assertStatus(t, app.send(readAs(token, 1)), http.StatusOK)
	saved := storedBookmark(t, app, token, 1)
	assertSiteOf(t, saved, "Og title", "Og description")
	if saved["title"] != "Page" || saved["note"] != "Og description" {
		t.Fatalf("title = %v, note = %v", saved["title"], saved["note"])
	}
	// The read changed the details and the note, so the bookmark counts as modified.
	if updated, _ := saved["updated_at"].(float64); updated <= 1000 {
		t.Fatalf("updated_at = %v, want it moved past 1000", saved["updated_at"])
	}
}

func TestReadKeepsANoteThatHasText(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	site := serveHTML(ogPage)
	defer site.Close()
	app.addBookmarkAt(token, site.URL+"/")
	app.exec("UPDATE bookmarks SET note = 'Mine'")

	r := app.send(readAs(token, 1))
	mustContain(t, r, streamEvent("result", `{"id":1,"site":`+ogResult+`,"note_filled":false}`))
	saved := storedBookmark(t, app, token, 1)
	assertSiteOf(t, saved, "Og title", "Og description")
	if saved["note"] != "Mine" {
		t.Fatalf("note = %v", saved["note"])
	}
}

func TestReadOfAPageWithOnlyATitleLeavesTheNoteBlank(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	site := serveHTML("<html><head><title>Just a title</title></head></html>")
	defer site.Close()
	app.addBookmarkAt(token, site.URL+"/")

	r := app.send(readAs(token, 1))
	mustContain(t, r, streamEvent("result", `{"id":1,"site":{"title":"Just a title","description":""},"note_filled":false}`))
	saved := storedBookmark(t, app, token, 1)
	assertSiteOf(t, saved, "Just a title", "")
	if saved["note"] != "" {
		t.Fatalf("note = %v", saved["note"])
	}
}

func TestReadOfAPageThatSaysNothingSendsNullAndStoresNothing(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	site := serveHTML("<html><head></head><body>hello</body></html>")
	defer site.Close()
	app.addBookmarkAt(token, site.URL+"/")
	app.exec("UPDATE bookmarks SET site_title = 'Old title', site_description = 'Old description'")

	r := app.send(readAs(token, 1))
	assertEvents(t, r, "result,done")
	mustContain(t, r, streamEvent("result", `{"id":1,"site":null,"note_filled":false}`))
	saved := storedBookmark(t, app, token, 1)
	assertSiteOf(t, saved, "Old title", "Old description")
	if saved["note"] != "" {
		t.Fatalf("note = %v", saved["note"])
	}
}

func TestReadCutsTheDetailsToWhatABookmarkKeeps(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	site := serveHTML(`<html><head><title>` + strings.Repeat("語", 301) + `</title>
		<meta name="description" content="` + strings.Repeat("d", 501) + `"></head></html>`)
	defer site.Close()
	app.addBookmarkAt(token, site.URL+"/")

	assertStatus(t, app.send(readAs(token, 1)), http.StatusOK)
	saved := storedBookmark(t, app, token, 1)
	assertSiteOf(t, saved, strings.Repeat("語", 300), strings.Repeat("d", 500))
	if saved["note"] != strings.Repeat("d", 500) {
		t.Errorf("note has %d characters", len([]rune(saved["note"].(string))))
	}
}

func TestReadDoesNotSendAResultForABookmarkEditedDuringTheRead(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	edited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		app.exec("UPDATE bookmarks SET url = 'https://elsewhere.test/'")
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(ogPage))
	}))
	defer edited.Close()
	app.addBookmarkAt(token, edited.URL+"/")

	r := app.send(readAs(token, 1))
	assertEvents(t, r, "done")
	saved := storedBookmark(t, app, token, 1)
	if saved["site"] != nil || saved["note"] != "" {
		t.Fatalf("site = %v, note = %v", saved["site"], saved["note"])
	}
}

func TestReadSaysWhyAPageCouldNotBeReadAndStoresNothing(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	mux := http.NewServeMux()
	mux.HandleFunc("/gone", http.NotFound)
	mux.HandleFunc("/file", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF"))
	})
	site := httptest.NewServer(mux)
	defer site.Close()
	app.addBookmarkAt(token, site.URL+"/gone")
	app.addBookmarkAt(token, site.URL+"/file")

	r := app.send(readAs(token, 1, 2))
	assertEvents(t, r, "failed,failed,done")
	mustContain(t, r,
		streamEvent("failed", `{"id":1,"detail":"HTTP 404"}`),
		streamEvent("failed", `{"id":2,"detail":"Not an HTML page"}`))
	for _, id := range []int64{1, 2} {
		if saved := storedBookmark(t, app, token, id); saved["site"] != nil || saved["note"] != "" {
			t.Errorf("bookmark %d: site = %v, note = %v", id, saved["site"], saved["note"])
		}
	}
}

func TestReadSkipsTheRestOfASiteThatAsksToWait(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	busy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer busy.Close()
	app.addBookmarkAt(token, busy.URL+"/a")
	app.addBookmarkAt(token, busy.URL+"/b")

	r := app.send(readAs(token, 1, 2))
	assertEvents(t, r, "failed,skipped,done")
	mustContain(t, r,
		streamEvent("failed", `{"id":1,"detail":"HTTP 429"}`),
		streamEvent("skipped", `{"id":2,"reason":"slow_down","host":"127.0.0.1"}`))
}

func TestReadSendsTheUsersUserAgent(t *testing.T) {
	app := runningApp(t)
	henry := app.signIn("henry", false)
	mei := app.signIn("mei", false)
	site, seen := userAgentSite()
	defer site.Close()
	assertStatus(t, putSettings(app, henry, map[string]any{"link_check_enabled": false, "link_check_user_agent": "Henry/1"}), http.StatusOK)
	henrysPage := app.addBookmarkAt(henry, site.URL+"/henry")
	meisPage := app.addBookmarkAt(mei, site.URL+"/mei")

	assertStatus(t, app.send(readAs(henry, int(henrysPage))), http.StatusOK)
	assertStatus(t, app.send(readAs(mei, int(meisPage))), http.StatusOK)
	if got := seen("/henry"); got != "Henry/1" {
		t.Errorf("henry's page was asked with %q", got)
	}
	if got := seen("/mei"); got != linkcheck.DefaultUserAgent() {
		t.Errorf("mei's page was asked with %q, want the default", got)
	}
}

func TestReadThatCannotSaveSendsErrorAndStops(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	site := serveHTML(ogPage)
	defer site.Close()
	app.addBookmarkAt(token, site.URL+"/a")
	app.addBookmarkAt(token, site.URL+"/b")
	app.exec(`CREATE TRIGGER no_site_update BEFORE UPDATE OF site_title ON bookmarks
		BEGIN SELECT RAISE(ABORT, 'read only'); END`)

	r := app.send(readAs(token, 1, 2))
	assertEvents(t, r, "error")
	mustContain(t, r, streamEvent("error", "{}"))
	assertSlotIsFree(t, app)
}

func TestAReadIsSavedEvenWhenTheClientHasLeft(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	id := app.addBookmarkAt(token, "https://read.example/a")
	gone, leave := context.WithCancel(context.Background())
	leave()
	checked := linkcheck.Checked{
		Target:  linkcheck.Target{ID: id, URL: "https://read.example/a"},
		Verdict: linkcheck.Verdict{Status: linkcheck.StatusOK},
		Meta:    &sitemeta.Meta{Title: "Left", Description: "Behind"},
	}

	stored, err := app.app.storeRead(gone, auth.SessionUser{UserID: 1}, checked)

	want := storedRead{found: true, site: &bookmarks.Site{Title: "Left", Description: "Behind"}, noteFilled: true}
	if err != nil || !reflect.DeepEqual(stored, want) {
		t.Fatalf("stored %+v, error %v", stored, err)
	}
	assertSiteOf(t, storedBookmark(t, app, token, id), "Left", "Behind")
}

func TestReadWithoutIDsIsUnprocessableAndNeedsASession(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)

	missing := app.send(post("/api/bookmarks/read", map[string]any{}).as(token))
	assertStatus(t, missing, http.StatusUnprocessableEntity)
	mustContain(t, missing, "missing field `ids`")
	assertStatus(t, app.send(post("/api/bookmarks/read", map[string]any{"ids": []int{1}})), http.StatusUnauthorized)
	assertStatus(t, app.send(get("/api/bookmarks/read").as(token)), http.StatusMethodNotAllowed)
}

func TestReadOfAnotherUsersBookmarkIsNotFoundAndFreesTheSlot(t *testing.T) {
	app := runningApp(t)
	alice := app.signIn("alice", false)
	bob := app.signIn("bob", false)
	app.addBookmarkAt(bob, "https://bob.example/")

	r := app.send(readAs(alice, 1))
	assertStatus(t, r, http.StatusNotFound)
	if r.text != `{"error":"not found"}` {
		t.Errorf("body %q", r.text)
	}
	assertSlotIsFree(t, app)
}

func TestReadWaitsForTheUsersRunningCheck(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	var hits atomic.Int32
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer site.Close()
	app.addBookmarkAt(token, site.URL+"/")
	release, ok := app.app.activeChecks.Start(1)
	if !ok {
		t.Fatal("slot not free")
	}

	r := app.send(readAs(token, 1))
	assertStatus(t, r, http.StatusTooManyRequests)
	assertEqualText(t, r.headers.Get("Retry-After"), "10")
	if hits.Load() != 0 {
		t.Fatal("the site was contacted while the user's slot was taken")
	}

	release()
	assertStatus(t, app.send(readAs(token, 1)), http.StatusOK)
}
