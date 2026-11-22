package server

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

const readNotice = "Some pages were not read: a site asked to be left alone, did not answer, " +
	"or the time ran out. Read them again later."

// readSelection is what the selection form posts when "Read pages" is chosen.
func readSelection(ids ...int) string {
	body := ""
	for _, id := range ids {
		body += "id-" + strconv.Itoa(id) + "=1&"
	}
	return body + "action=read&" + bulkContext
}

func TestTheNoScriptReadStoresTheDetailsAndSaysDone(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	site := serveHTML(ogPage)
	defer site.Close()
	app.addBookmarkAt(token, site.URL+"/")

	r := app.send(formPost("/bookmarks/bulk", readSelection(1)).as(token))
	mustRedirect(t, r, "/?notice=bulk-done")
	saved := storedBookmark(t, app, token, 1)
	assertSiteOf(t, saved, "Og title", "Og description")
	if saved["note"] != "Og description" {
		t.Errorf("note = %v", saved["note"])
	}
	mustContain(t, app.send(get(locationOf(r)).as(token)), "Done.")
	assertSlotIsFree(t, app)
}

func TestTheNoScriptReadOfAPageThatCannotBeReadStoresNothing(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	missing := httptest.NewServer(http.HandlerFunc(http.NotFound))
	defer missing.Close()
	app.addBookmarkAt(token, missing.URL+"/")

	mustRedirect(t, app.send(formPost("/bookmarks/bulk", readSelection(1)).as(token)), "/?notice=bulk-done")
	if saved := storedBookmark(t, app, token, 1); saved["site"] != nil || saved["note"] != "" {
		t.Fatalf("site = %v, note = %v", saved["site"], saved["note"])
	}
}

func TestTheNoScriptReadOfASiteThatAsksToWaitSaysSomePagesWereNotRead(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	busy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer busy.Close()
	app.addBookmarkAt(token, busy.URL+"/a")
	app.addBookmarkAt(token, busy.URL+"/b")

	r := app.send(formPost("/bookmarks/bulk", readSelection(1, 2)).as(token))
	mustRedirect(t, r, "/?notice=read-incomplete")
	mustContain(t, app.send(get(locationOf(r)).as(token)), readNotice)
}

func TestTheNoScriptReadThatRunsOutOfTimeSaysSomePagesWereNotRead(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer slow.Close()
	app.addBookmarkAt(token, slow.URL+"/")
	app.app.checkLimit = 50 * time.Millisecond

	mustRedirect(t, app.send(formPost("/bookmarks/bulk", readSelection(1)).as(token)), "/?notice=read-incomplete")
	assertSlotIsFree(t, app)
}

func TestTheNoScriptReadWithNothingTickedSaysSo(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)

	mustRedirect(t, app.send(formPost("/bookmarks/bulk", readSelection()).as(token)), "/?select=1&notice=nothing-selected")
}
