package server

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strconv"
	"testing"
	"time"
)

var rowBox = regexp.MustCompile(`name="id-(\d+)" value="1"( checked)?`)

// tickedRows lists the ids of the rows that the page shows ticked.
func tickedRows(r reply) []string {
	ticked := []string{}
	for _, box := range rowBox.FindAllStringSubmatch(r.text, -1) {
		if box[2] != "" {
			ticked = append(ticked, box[1])
		}
	}
	return ticked
}

func assertTicked(t *testing.T, r reply, want ...string) {
	t.Helper()
	if got := tickedRows(r); !reflect.DeepEqual(got, want) {
		t.Errorf("ticked rows = %v, want %v", got, want)
	}
}

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

func TestTheNoScriptReadOfAPageThatCannotBeReadStoresNothingAndKeepsItTicked(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	missing := httptest.NewServer(http.HandlerFunc(http.NotFound))
	defer missing.Close()
	app.addBookmarkAt(token, missing.URL+"/")

	r := app.send(formPost("/bookmarks/bulk", readSelection(1)).as(token))
	assertStatus(t, r, http.StatusOK)
	mustContain(t, r, "1 page was not read: ")
	mustLack(t, r, "Done.")
	assertTicked(t, r, "1")
	if saved := storedBookmark(t, app, token, 1); saved["site"] != nil || saved["note"] != "" {
		t.Fatalf("site = %v, note = %v", saved["site"], saved["note"])
	}
	assertSlotIsFree(t, app)
}

func TestTheNoScriptReadTicksOnlyThePagesItDidNotRead(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	site := serveHTML(ogPage)
	defer site.Close()
	missing := httptest.NewServer(http.HandlerFunc(http.NotFound))
	defer missing.Close()
	app.addBookmarkAt(token, site.URL+"/")
	app.addBookmarkAt(token, missing.URL+"/")
	app.addBookmarkAt(token, site.URL+"/again")

	r := app.send(formPost("/bookmarks/bulk", readSelection(1, 2, 3)+"&tags=keep").as(token))
	assertStatus(t, r, http.StatusOK)
	mustContain(t, r, "1 page was not read: ", `value="keep"`)
	assertTicked(t, r, "2")
	assertSiteOf(t, storedBookmark(t, app, token, 1), "Og title", "Og description")
	assertSiteOf(t, storedBookmark(t, app, token, 3), "Og title", "Og description")
}

func TestTheNoScriptReadOfAPageThatSaysNothingIsRead(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	silent := serveHTML("<html><head></head><body>hello</body></html>")
	defer silent.Close()
	app.addBookmarkAt(token, silent.URL+"/")

	mustRedirect(t, app.send(formPost("/bookmarks/bulk", readSelection(1)).as(token)), "/?notice=bulk-done")
}

func TestTheNoScriptReadOfASiteThatAsksToWaitTicksTheTwoPagesNotRead(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	busy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer busy.Close()
	app.addBookmarkAt(token, busy.URL+"/a")
	app.addBookmarkAt(token, busy.URL+"/b")

	r := app.send(formPost("/bookmarks/bulk", readSelection(1, 2)).as(token))
	assertStatus(t, r, http.StatusOK)
	mustContain(t, r, "2 pages were not read: ")
	assertTicked(t, r, "2", "1")
}

func TestTheNoScriptReadThatRunsOutOfTimeTicksThePageNotRead(t *testing.T) {
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

	r := app.send(formPost("/bookmarks/bulk", readSelection(1)).as(token))
	assertStatus(t, r, http.StatusOK)
	mustContain(t, r, "1 page was not read: ")
	assertTicked(t, r, "1")
	assertSlotIsFree(t, app)
}

func TestTheNoScriptReadWithNothingTickedSaysSo(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)

	mustRedirect(t, app.send(formPost("/bookmarks/bulk", readSelection()).as(token)), "/?select=1&notice=nothing-selected")
}
