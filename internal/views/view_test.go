package views

import (
	"strings"
	"testing"
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
		"https://www.Example.com:8080/a?b#c":     "example.com",
		"http://me@host.test/":                   "host.test",
		"not a url":                              "not a url",
		"https://host.test:/x":                   "host.test",
		"https://host.test:80a/x":                "host.test:80a",
		"https:///x":                             "https:///x",
		"https://user:secret@example.com/50%off": "example.com",
		"https://example.com/50%off":             "example.com",
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
		Rows: pageRows(2, 1), Matching: 2,
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
		`<a class="btn ico quiet" href="/" title="Done">`,
		`<a class="btn ico on" href="/" title="Select">`,
		`Select all`,
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

func TestBulkBarFollowsTheListAndHasNoTagsToggle(t *testing.T) {
	view := BookmarksView{
		Shell: emptyShell("alice", false), List: ListView{Page: 1, Select: true}, Now: goldenNow, Rows: pageRows(1, 1),
	}
	page := BookmarksPage(view)
	list, bar := strings.Index(page, `<ul class="list selecting">`), strings.Index(page, `class="bulk"`)
	if list < 0 || bar < list {
		t.Errorf("the bulk bar must come after the list (list %d, bar %d)", list, bar)
	}
	requireAbsent(t, page, "bulk-tags-toggle")
	requireContains(t, page, `name="action" value="add_tags"`, `name="action" value="remove_tags"`,
		`name="action" value="check"`, `name="action" value="read" title="Read pages"`,
		`name="action" value="delete"`, `id="bulk-count"`)
}

func TestSelectLinkIsOnlyPressedInSelectMode(t *testing.T) {
	view := BookmarksView{Shell: emptyShell("alice", false), List: ListView{Page: 1}, Rows: pageRows(1, 1)}
	page := BookmarksPage(view)
	requireContains(t, page, `<a class="btn ico" href="/?select=1" title="Select">`)
	requireAbsent(t, page, `class="bulk"`, `ico on`)
}

func TestSearchTipsExplainTheSyntax(t *testing.T) {
	page := BookmarksPage(BookmarksView{Shell: emptyShell("alice", false), List: ListView{Page: 1}})
	requireContains(t, page, `<details class="tips" id="search-hint">`, `aria-describedby="search-hint"`,
		"<code>-word</code>", "<code>url:github.com</code>", "<code>status:broken</code>")
}

func TestRowShowsItsUrlWithHighlight(t *testing.T) {
	row := BookmarkRow{ID: 1, URL: "https://a.test/x", Title: "A", DomainTerms: []string{"a.test"}}
	requireContains(t, string(bookmarkItem(row, goldenNow, "/", nil)), `<p class="url" title="https://a.test/x">https://<mark>a.test</mark>/x</p>`)
}

func TestIconsAllHaveAPathAndRender(t *testing.T) {
	for name := range iconPaths {
		if iconPaths[name] == "" {
			t.Errorf("icon %q has no path", name)
		}
	}
	for _, name := range []iconName{iconOK, iconBroken, iconRedirected, iconUnknown, iconEdit, iconTrash, iconPlus,
		iconShuffle, iconSelect, iconAll, iconTagAdd, iconTagRemove, iconLink, iconSearch, iconMenu,
		iconUpload, iconDownload, iconSignOut, iconSignIn, iconKey, iconUserAdd, iconBan} {
		if _, ok := iconPaths[name]; !ok {
			t.Errorf("icon %q is not drawn", name)
		}
	}
	want := `<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8 3v10M3 8h10"></path></svg><span class="lbl">Add</span>`
	if got := string(iconLabel(iconPlus, "Add")); got != want {
		t.Errorf("got %s, want %s", got, want)
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
		`<section class="pick panel" aria-label="Random bookmark">`,
		`<a class="btn ico primary" href="/random?q=x" title="Another">`,
		`<a class="btn ico quiet" href="/bookmarks/3/edit?back=%2F%3Fq%3Dx" title="Edit">`,
		`<a class="btn ico quiet danger-text" href="/bookmarks/3/delete?back=%2F%3Fq%3Dx" title="Delete">`,
		`<a class="btn ico" href="/?q=x" title="Close">`,
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

func TestLoadMoreLinksToTheLongerPageAndReturnsToTheLastRow(t *testing.T) {
	view := BookmarksView{Shell: emptyShell("alice", false), List: ListView{Q: "x", Page: 2}, Rows: pageRows(3, 1), Matching: 101}
	page := BookmarksPage(view)
	requireContains(t, page,
		`<div class="foot"><p>Showing 3 of 101</p><a class="btn" id="load-more" href="/?q=x&amp;page=3#bookmark-5">Load more</a></div>`,
		`<li class="item" id="bookmark-7" data-id="7">`)
	requireAbsent(t, page, "rel=\"next\"", "Newer", "Older")
}

func TestLoadMoreKeepsSelectModeAndTicks(t *testing.T) {
	view := BookmarksView{Shell: emptyShell("alice", false), List: ListView{Page: 1, Select: true}, Rows: pageRows(1, 1), Matching: 2}
	requireContains(t, BookmarksPage(view), `href="/?page=2&amp;select=1#bookmark-5"`, "Select the 1 shown")
	view.List.All = true
	requireContains(t, BookmarksPage(view), `href="/?page=2&amp;select=1&amp;all=1#bookmark-5"`, "Clear selection")
}

func TestTheFootHasNoLinkWhenEverythingIsShown(t *testing.T) {
	for _, c := range []struct {
		name     string
		rows     []BookmarkRow
		matching int64
		want     string
	}{
		{"none", nil, 0, ""},
		{"one", pageRows(1, 1), 1, "<p>1 bookmark shown</p>"},
		{"two", pageRows(2, 1), 2, "<p>All 2 bookmarks shown</p>"},
	} {
		page := BookmarksPage(BookmarksView{Shell: emptyShell("alice", false), List: listView(1), Rows: c.rows, Matching: c.matching})
		requireAbsent(t, page, "load-more")
		if c.want == "" {
			requireAbsent(t, page, `class="foot"`)
			continue
		}
		requireContains(t, page, c.want)
	}
}

func TestTheFootOffersNothingWhenNoRowIsShownYet(t *testing.T) {
	page := BookmarksPage(BookmarksView{Shell: emptyShell("alice", false), List: listView(1), Matching: 3})
	requireContains(t, page, "<p>Showing 0 of 3</p>")
	requireAbsent(t, page, "load-more")
}

func TestListHeadingAndCounts(t *testing.T) {
	shell := emptyShell("alice", false)
	shell.Total = 7
	page := BookmarksPage(BookmarksView{Shell: shell, List: ListView{Tag: "go", Page: 1}, Matching: 3})
	requireContains(t, page, `<div class="head"><h1>Tagged “go”</h1><span class="count" role="status">3 of 7</span></div>`,
		`<span class="count">7</span></a>`)
	page = BookmarksPage(BookmarksView{Shell: shell, List: listView(1), Matching: 7})
	requireContains(t, page, `<h1>All bookmarks</h1><span class="count" role="status">7 bookmarks</span>`)
}

func TestActiveFilterChipsRemoveOneFilterEach(t *testing.T) {
	cases := []struct {
		name string
		list ListView
		want []string
	}{
		{"tag", ListView{Q: "x", Tag: "go", Page: 3, Select: true}, []string{
			`<div class="active"><a class="chip" href="/?q=x&amp;select=1">Tag: go<span class="sr-only"> Remove filter</span><span aria-hidden="true">×</span></a></div>`}},
		{"status", ListView{Status: "redirected", Page: 2}, []string{
			`<div class="active"><a class="chip" href="/">Status: Moved<span class="sr-only"> Remove filter</span>`}},
		{"both", ListView{Tag: "go", Status: "ok"}, []string{
			`<a class="chip" href="/?status=ok">Tag: go`, `<a class="chip" href="/?tag=go">Status: OK`}},
		{"unknown status", ListView{Status: "odd"}, []string{`Status: odd`}},
	}
	for _, c := range cases {
		page := string(activeFilters(c.list))
		requireContains(t, page, c.want...)
	}
	if got := activeFilters(ListView{Q: "x", Page: 2}); got != "" {
		t.Errorf("no filter, got %q", got)
	}
}

func TestListHeadingNamesWhatTheListShows(t *testing.T) {
	cases := []struct {
		list ListView
		want string
	}{
		{ListView{}, "All bookmarks"},
		{ListView{Tag: "go"}, "Tagged “go”"},
		{ListView{Status: "broken"}, "Broken links"},
		{ListView{Status: "redirected"}, "Moved links"},
		{ListView{Status: "unknown"}, "Links not confirmed"},
		{ListView{Status: "ok"}, "Links that are OK"},
		{ListView{Status: "unchecked"}, "Links not checked"},
		{ListView{Status: "odd"}, "Search results"},
		{ListView{Q: "x", Tag: "go", Status: "ok"}, "Search results"},
		{ListView{Q: "  ", Tag: "go"}, "Tagged “go”"},
	}
	for _, c := range cases {
		if got := listHeading(c.list); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.list, got, c.want)
		}
	}
}

func TestCountWording(t *testing.T) {
	cases := []struct {
		filtered        bool
		matching, total int64
		want            string
	}{
		{false, 0, 0, "0 bookmarks"},
		{false, 1, 1, "1 bookmark"},
		{false, 2, 2, "2 bookmarks"},
		{true, 0, 5, "0 of 5"},
		{true, 1, 2, "1 of 2"},
	}
	for _, c := range cases {
		if got := countLabel(c.filtered, c.matching, c.total); got != c.want {
			t.Errorf("%+v: got %q, want %q", c, got, c.want)
		}
	}
}

func TestFootAndSelectAllWording(t *testing.T) {
	cases := []struct {
		shown, matching      int64
		wantFoot, wantSelect string
	}{
		{0, 0, "", "Select all"},
		{1, 1, "1 bookmark shown", "Select all"},
		{2, 2, "All 2 bookmarks shown", "Select all"},
		{50, 51, "Showing 50 of 51", "Select the 50 shown"},
		{100, 100, "All 100 bookmarks shown", "Select all"},
		{100, 101, "Showing 100 of 101", "Select the 100 shown"},
	}
	for _, c := range cases {
		if got := footText(c.shown, c.matching); got != c.wantFoot {
			t.Errorf("foot %d of %d: got %q, want %q", c.shown, c.matching, got, c.wantFoot)
		}
		if got := selectAllLabel(c.shown, c.matching); got != c.wantSelect {
			t.Errorf("select %d of %d: got %q, want %q", c.shown, c.matching, got, c.wantSelect)
		}
	}
}

func TestFiltersDropOneFilterAndTheirPage(t *testing.T) {
	view := ListView{Q: "x", Tag: "go", Status: "ok", Page: 4, Select: true}
	if got, want := view.WithoutTag().Href(), "/?q=x&status=ok&select=1"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := view.WithoutStatus().Href(), "/?q=x&tag=go&select=1"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	for list, want := range map[string]bool{"": false, " ": false, "x": true} {
		if got := (ListView{Q: list}).Filtered(); got != want {
			t.Errorf("Q %q: Filtered = %v", list, got)
		}
	}
	if !(ListView{Tag: "a"}).Filtered() || !(ListView{Status: "ok"}).Filtered() {
		t.Error("a tag or a status is a filter")
	}
}

func TestEditPageOffersTheMovedAddressOnlyForAMovedLink(t *testing.T) {
	view := EditView{Shell: alice(SectionBookmarks), ID: 7, URL: "https://old.test/", Back: "/"}
	requireAbsent(t, EditPage(view), "moved", "data-moved-to")
	view.MovedTo = `https://new.test/?a=1&b="2"`
	requireContains(t, EditPage(view),
		`<p class="hint moved">This link moved to https://new.test/?a=1&amp;b=&quot;2&quot;</p>`,
		`<button class="btn" type="button" data-moved-to="https://new.test/?a=1&amp;b=&quot;2&quot;" data-script-only hidden>Use new address</button>`)
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
		`<span class="who"><b>Firefox &lt;1&gt;</b><span class="hint">10.0.0.1 · last used <time datetime="2020-01-01T00:00:00Z" title="2020-01-01 00:00 UTC">6 years ago</time></span></span><span class="badge admin">This browser</span>`,
		`<form method="post" action="/account/sessions/def/revoke"><button class="btn ico" type="submit" title="Sign out" aria-label="Sign out Unknown browser"><svg viewBox="0 0 16 16" aria-hidden="true">`,
		`<span class="lbl">Sign out</span></button></form>`,
		`At least 12 characters, without your username. Changing it signs you out everywhere else.`,
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

func TestTheSettingsPageHasAUserAgentBoxThatKeepsAndEscapesItsValue(t *testing.T) {
	page := SettingsPage(SettingsView{
		Shell:              alice(SectionSettings),
		LinkCheckUserAgent: `a"b<c>&d`,
		DefaultUserAgent:   "Default/1 (+https://example.test)",
		MaxUserAgentLength: 200,
	})
	requireContains(t, page,
		`name="link_check_user_agent"`,
		`value="a&quot;b&lt;c&gt;&amp;d"`,
		`placeholder="Default/1 (+https://example.test)"`,
		`maxlength="200"`,
	)
	requireAbsent(t, page, `role="alert"`)
}

// The User-Agent works for the "Check links" button even when the server runs
// no background check, so the note must not sit above it or claim it is idle.
func TestTheOffNoteSitsUnderTheCheckboxAndLeavesTheUserAgentOut(t *testing.T) {
	page := SettingsPage(SettingsView{Shell: alice(SectionSettings), MaxUserAgentLength: 200})
	note := strings.Index(page, "Background checks are turned off")
	checkbox := strings.Index(page, `name="link_check_enabled"`)
	box := strings.Index(page, `name="link_check_user_agent"`)
	if checkbox < 0 || note < checkbox || note > box {
		t.Errorf("positions: checkbox %d, note %d, box %d; want the note between them", checkbox, note, box)
	}
	requireContains(t, page, "the box above has no effect", `still applies to the &quot;Check links&quot; button and to reading a page`)
	requireAbsent(t, page, "this setting has no effect")
}

// A post without the checkbox turns the background check off, so the two
// settings must never be sent by separate forms.
func TestTheUserAgentHasItsOwnHeadingButSharesTheFormWithTheCheckbox(t *testing.T) {
	page := SettingsPage(SettingsView{Shell: alice(SectionSettings), MaxUserAgentLength: 200})
	requireContains(t, page, "<h2>Link check</h2>", "<h2>User-Agent</h2>")
	if got := strings.Count(page, `action="/settings"`); got != 1 {
		t.Fatalf("settings forms = %d, want 1", got)
	}
	start := strings.Index(page, `action="/settings"`)
	form := page[start : start+strings.Index(page[start:], "</form>")]
	requireContains(t, form, `name="link_check_enabled"`, `name="link_check_user_agent"`)
}

func TestTheSettingsPageShowsAnErrorOnlyWhenThereIsOne(t *testing.T) {
	page := SettingsPage(SettingsView{Shell: alice(SectionSettings), Error: "user agent is too long"})
	requireContains(t, page, `<p class="err" role="alert">user agent is too long</p>`)
}

func TestInactiveUserRowsHideTheirActionsForYourself(t *testing.T) {
	view := UsersView{Shell: alice(SectionUsers), Now: goldenNow, NewIsAdmin: true,
		Users: []UserRow{{ID: 7, Username: "me", IsAdmin: true, IsDisabled: true}}}
	page := UsersPage(view)
	requireAbsent(t, page, "/users/7/")
	requireContains(t, page, `<span class="badge admin">Admin</span><span class="badge">Disabled</span>`, `name="is_admin" checked>`)
}
