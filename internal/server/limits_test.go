package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/henry40408/bkd/internal/views"
)

func TestAUserAgentIsCutToTwoHundredCharacters(t *testing.T) {
	cases := map[int]int{199: 199, 200: 200, 201: 200}
	for given, want := range cases {
		start := caller{userAgent: strings.Repeat("é", given)}.sessionStart(1)
		if got := len([]rune(start.UserAgent)); got != want {
			t.Errorf("%d characters kept as %d, want %d", given, got, want)
		}
	}
}

func seedBookmarks(app *testApp, token string, count int) {
	for index := range count {
		app.createBookmark(token, fmt.Sprintf("bookmark %d", index))
	}
}

func rowsIn(r reply) int { return strings.Count(r.text, `<li class="item"`) }

func TestTheListOffersLoadMoreOnlyBeyondTheRowsShown(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	seedBookmarks(app, token, views.PageSize)
	full := app.send(get("/").as(token))
	mustContain(t, full, "All 50 bookmarks shown")
	mustLack(t, full, "load-more")

	app.createBookmark(token, "one more")

	first := app.send(get("/").as(token))
	mustContain(t, first, "Showing 50 of 51", `id="load-more"`, `href="/?page=2#bookmark-`)
	if got := rowsIn(first); got != 50 {
		t.Errorf("first page lists %d rows, want 50", got)
	}
	second := app.send(get("/?page=2").as(token))
	mustContain(t, second, "All 51 bookmarks shown")
	mustLack(t, second, "load-more")
	if got := rowsIn(second); got != 51 {
		t.Errorf("second page lists %d rows, want 51", got)
	}
}

func TestASecondPageHoldsHundredRowsAndOffersMoreFromHundredAndOne(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	seedBookmarks(app, token, 2*views.PageSize)
	exact := app.send(get("/?page=2").as(token))
	mustContain(t, exact, "All 100 bookmarks shown")
	mustLack(t, exact, "load-more")

	app.createBookmark(token, "one more")

	more := app.send(get("/?page=2").as(token))
	mustContain(t, more, "Showing 100 of 101", `href="/?page=3#bookmark-`)
	if got := rowsIn(more); got != 100 {
		t.Errorf("second page lists %d rows, want 100", got)
	}
}

func TestAPageNumberIsCappedAtTheLimit(t *testing.T) {
	for page, want := range map[string]uint32{"999999": 999_999, "1000000": 1_000_000, "1000001": 1_000_000, "0": 1} {
		request := httptest.NewRequest(http.MethodGet, "/?page="+page, nil)
		view, failure := readListView(request)
		if failure != nil {
			t.Fatalf("page %s: %v", page, failure)
		}
		if view.Page != want {
			t.Errorf("page %s read as %d, want %d", page, view.Page, want)
		}
	}
}

func TestTheCappedPageStillAnswers(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	app.createBookmark(token, "only")
	r := app.send(get("/?page=1000001").as(token))
	assertStatus(t, r, http.StatusOK)
	mustContain(t, r, "1 bookmark shown")
}
