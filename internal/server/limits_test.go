package server

import (
	"fmt"
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

func TestTheListOffersOlderBookmarksOnlyBeyondOnePage(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	for index := range views.PageSize {
		app.createBookmark(token, fmt.Sprintf("bookmark %d", index))
	}
	if r := app.send(get("/").as(token)); strings.Contains(r.text, "Older") {
		t.Fatal("a full page and nothing more offers older bookmarks")
	}

	app.createBookmark(token, "one more")

	if r := app.send(get("/").as(token)); !strings.Contains(r.text, "Older") {
		t.Fatal("a bookmark beyond the page is not reachable")
	}
}
