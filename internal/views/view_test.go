package views

import (
	"strings"
	"testing"
	"time"
)

func TestHighlightWrapsMatchesAndEscapesEverything(t *testing.T) {
	got := highlight("Rust <b> & Rust", []string{"rust"})
	want := "<mark>Rust</mark> &lt;b&gt; &amp; <mark>Rust</mark>"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestHighlightWithoutTermsStillEscapes(t *testing.T) {
	if got := highlight("<script>", nil); string(got) != "&lt;script&gt;" {
		t.Fatalf("got %q", got)
	}
}

func TestHighlightHandlesNonASCIIText(t *testing.T) {
	got := highlight("學習 Rust 語言", []string{"rust", "語言"})
	want := "學習 <mark>Rust</mark> <mark>語言</mark>"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestHighlightPrefersTheLongestTermAtAPosition(t *testing.T) {
	got := highlight("abcd", []string{"ab", "abc", ""})
	if string(got) != "<mark>abc</mark>d" {
		t.Fatalf("got %q", got)
	}
}

func TestDomainsLoseSchemePortUserinfoAndWWW(t *testing.T) {
	tests := map[string]string{
		"https://www.Example.com:8080/a?b#c": "example.com",
		"http://me@host.test/":               "host.test",
		"not a url":                          "not a url",
		"https://host.test:/x":               "host.test",
		"https://host.test:80a/x":            "host.test:80a",
		"https:///x":                         "https:///x",
	}
	for url, want := range tests {
		if got := DomainOf(url); got != want {
			t.Errorf("DomainOf(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestTimesAreFormattedInUTC(t *testing.T) {
	got := NewSavedTime(1_700_000_000+3*86_400, 1_700_000_000)
	want := SavedTime{ISO: "2023-11-14T22:13:20Z", Exact: "2023-11-14 22:13 UTC", Ago: "3 days ago"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestCivilMatchesTheStandardLibrary(t *testing.T) {
	// Leap days, century rules, the epoch and negative times.
	seconds := []int64{
		0, 1, 86_399, 86_400, 951_782_400, 951_868_799, 4_107_542_400, 1_709_251_199, 1_709_251_200,
		-1, -86_400, -2_208_988_800, 253_402_300_799, 1_794_900_000,
	}
	for _, s := range seconds {
		want := time.Unix(s, 0).UTC()
		got := civil(s)
		if got.year != int64(want.Year()) || got.month != int64(want.Month()) || got.day != int64(want.Day()) ||
			got.hour != int64(want.Hour()) || got.minute != int64(want.Minute()) || got.second != int64(want.Second()) {
			t.Errorf("civil(%d) = %+v, want %s", s, got, want)
		}
	}
}

func TestRelativeTimesUseTheLargestUnit(t *testing.T) {
	tests := []struct {
		now, then int64
		want      string
	}{
		{100, 90, "just now"},
		{2 * 3_600, 0, "2 hours ago"},
		{3_600, 0, "1 hour ago"},
		{86_400 + 7_200, 0, "yesterday"},
		{14 * 86_400, 0, "2 weeks ago"},
		{60 * 86_400, 0, "2 months ago"},
		{400 * 86_400, 0, "1 year ago"},
		{0, 100, "just now"},
	}
	for _, test := range tests {
		if got := timeAgo(test.now, test.then); got != test.want {
			t.Errorf("timeAgo(%d, %d) = %q, want %q", test.now, test.then, got, test.want)
		}
	}
}

func TestListLinksKeepTheFilterAndEncodeIt(t *testing.T) {
	view := ListView{Q: "a b&c", Tag: "to read", Page: 3}
	if got, want := view.Href(), "/?q=a%20b%26c&tag=to%20read&page=3"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := (ListView{}).Href(); got != "/" {
		t.Fatalf("got %q", got)
	}
}

func TestListLinksKeepSelectModeAndPath(t *testing.T) {
	view := ListView{Status: "ok", Page: 2, Select: true, All: true}
	if got, want := view.HrefTo("/random"), "/random?status=ok&page=2&select=1&all=1"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got, want := view.WithoutSelect().Href(), "/?status=ok&page=2"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got, want := view.HrefForPage(1), "/?status=ok&select=1&all=1"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got, want := (ListView{Q: "  "}).WithAll(false).Href(), "/?select=1"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestWithQueryAppendsAfterAnExistingQuery(t *testing.T) {
	got := WithQuery("/a?x=1", [][2]string{{"back", "/?q=é~"}})
	if want := "/a?x=1&back=%2F%3Fq%3D%C3%A9~"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSelectModeTicksTheChosenRows(t *testing.T) {
	view := BookmarksView{
		Shell: emptyShell("alice", false), List: ListView{Page: 1, Select: true}, Now: goldenNow,
		Rows: pageRows(2, 1),
		Bulk: BulkState{Selected: []int64{5}, Tags: "a, b", Error: "Choose an action."},
	}
	page := BookmarksPage(view)
	for _, want := range []string{
		`<form class="bulk-form" method="post" action="/bookmarks/bulk">`,
		`<ul class="list selecting">`,
		`<input type="checkbox" name="id-5" value="1" checked aria-label="Select Page 1">`,
		`<input type="checkbox" name="id-6" value="1" aria-label="Select Page 2">`,
		`<input type="text" name="tags" value="a, b"`,
		`<p class="err" role="alert">Choose an action.</p>`,
		`<a class="button quiet-button on" href="/">Done</a>`,
		`Select all on this page`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	view.List.All = true
	page = BookmarksPage(view)
	if strings.Count(page, " checked ") != 2 || !strings.Contains(page, "Clear selection") {
		t.Errorf("select-all page does not tick every row")
	}
}

func TestAddFormOpensWithItsError(t *testing.T) {
	view := BookmarksView{
		Shell: emptyShell("alice", false), List: listView(1),
		Add: AddForm{Open: true, URL: "bad", Note: "<n>", Error: "url is not valid"},
	}
	page := BookmarksPage(view)
	for _, want := range []string{
		`<details class="add" id="add" open>`,
		`value="bad"`, `<textarea name="note" rows="2">&lt;n&gt;</textarea>`,
		`<p class="err" role="alert">url is not valid</p>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %s", want)
		}
	}
}

func TestPickShowsTheBookmarkAndWaysOut(t *testing.T) {
	pick := realRows()[0]
	view := BookmarksView{
		Shell: emptyShell("alice", false), List: ListView{Q: "x", Page: 1}, Now: goldenNow, Pick: &pick,
	}
	page := BookmarksPage(view)
	for _, want := range []string{
		`<section class="pick" aria-label="Random bookmark">`,
		`<a class="quiet" href="/random?q=x">Another</a>`,
		`<a class="quiet" href="/bookmarks/3/edit?back=%2F%3Fq%3Dx">Edit</a>`,
		`<a class="quiet" href="/?q=x">Close</a>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %s", want)
		}
	}
}

func TestAnUnknownLinkStatusShowsNoNote(t *testing.T) {
	row := BookmarkRow{ID: 1, URL: "https://a.test/", Title: "A", Link: &LinkCheck{Status: "unchecked"}}
	item := string(bookmarkItem(row, goldenNow, "/", nil))
	if strings.Contains(item, "link-note") || strings.Contains(item, "link-unchecked") {
		t.Errorf("unexpected note in %s", item)
	}
}

func TestPagerLinksNeighbours(t *testing.T) {
	view := BookmarksView{Shell: emptyShell("alice", false), List: ListView{Q: "x", Page: 2}, Rows: pageRows(1, 1), HasNext: true}
	page := BookmarksPage(view)
	for _, want := range []string{
		`<a class="button quiet-button" href="/?q=x" rel="prev">Newer</a>`,
		`<a class="button quiet-button" href="/?q=x&amp;page=3" rel="next">Older</a>`,
		`Showing 51–51`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %s", want)
		}
	}
}

func TestAccountPageListsSessions(t *testing.T) {
	view := AccountView{
		Shell: alice(SectionAccount), Now: goldenNow, Error: "current password is wrong",
		Sessions: []SessionRow{
			{ID: "abc", Agent: "Firefox <1>", IP: "10.0.0.1", LastUsedAt: 1577836800, IsCurrent: true},
			{ID: "def", Agent: "Unknown browser", IP: "::1", LastUsedAt: 1577836800},
		},
	}
	page := AccountPage(view)
	for _, want := range []string{
		`<span class="name">Firefox &lt;1&gt;<span class="here">This browser</span></span>`,
		`<span class="meta">10.0.0.1 · last used <time datetime="2020-01-01T00:00:00Z" title="2020-01-01 00:00 UTC">6 years ago</time></span>`,
		`<form method="post" action="/account/sessions/def/revoke"><button class="quiet danger" type="submit" aria-label="Sign out Unknown browser">Sign out</button></form>`,
		`action="/account/sessions/revoke-others"`,
		`<input type="password" name="new" autocomplete="new-password" minlength="12" required>`,
		`<input type="password" name="again" autocomplete="new-password" required>`,
		`<p class="err" role="alert">current password is wrong</p>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	if strings.Contains(page, "/account/sessions/abc/revoke") {
		t.Error("the current session must not offer a sign-out button")
	}
	view.Sessions = view.Sessions[:1]
	if strings.Contains(AccountPage(view), "revoke-others") {
		t.Error("a single session must not offer to sign out the others")
	}
}

func TestForwardedUsersSeeNoAccountLinkOrSignOut(t *testing.T) {
	shell := alice(SectionSettings)
	shell.Forwarded = true
	page := SettingsPage(SettingsView{Shell: shell, LinkCheckEnabled: true, ServerChecksLinks: true})
	for _, unwanted := range []string{`href="/account"`, `action="/logout"`, "Background checks are turned off"} {
		if strings.Contains(page, unwanted) {
			t.Errorf("page contains %s", unwanted)
		}
	}
	if !strings.Contains(page, `name="link_check_enabled" value="on" checked>`) {
		t.Error("the checkbox must start ticked")
	}
}

func TestInactiveUserRowsHideTheirActionsForYourself(t *testing.T) {
	view := UsersView{Shell: alice(SectionUsers), Now: goldenNow, NewIsAdmin: true,
		Users: []UserRow{{ID: 7, Username: "me", IsAdmin: true, IsDisabled: true}}}
	page := UsersPage(view)
	if strings.Contains(page, "user-actions") {
		t.Error("your own row must have no buttons")
	}
	if !strings.Contains(page, `<div class="user-row off">`) || !strings.Contains(page, `name="is_admin" checked>`) {
		t.Error("a disabled row needs the off class and the new-user admin box must be ticked")
	}
}
