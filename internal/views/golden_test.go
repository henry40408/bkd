package views

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/henry40408/bkd/internal/search"
)

// goldenNow makes the January 2020 timestamps in the goldens read "6 years ago".
const goldenNow = 1_794_900_000

// golden returns a Rust-rendered page body. The first line is the capture
// script's comment (see testdata/README.txt) and is not part of the page.
func golden(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".golden.html"))
	if err != nil {
		t.Fatal(err)
	}
	_, page, found := strings.Cut(string(data), "\n")
	if !found {
		t.Fatalf("%s: no capture comment line", name)
	}
	return page
}

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	want := golden(t, name)
	if got == want {
		return
	}
	at := 0
	for at < len(got) && at < len(want) && got[at] == want[at] {
		at++
	}
	window := func(s string) string {
		return s[max(at-80, 0):min(at+160, len(s))]
	}
	t.Fatalf("%s differs at byte %d (got %d bytes, want %d)\n got: ...%s\nwant: ...%s",
		name, at, len(got), len(want), window(got), window(want))
}

var sidebarLink = regexp.MustCompile(`<a href="(/\?(status|tag)=[^"]*)"( aria-current="page")?>([^<]*)<span class="count">(\d+)</span></a>`)

// shellOf rebuilds the sidebar filter lists from the golden itself, for pages
// whose sidebar counts depend on the state the capture had reached. The
// sidebar markup is checked against hand-built data in the list goldens.
func shellOf(t *testing.T, name string, shell Shell) Shell {
	t.Helper()
	for _, m := range sidebarLink.FindAllStringSubmatch(golden(t, name), -1) {
		count, err := strconv.ParseInt(m[5], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		link := FilterLink{Label: m[4], Count: count, Href: m[1], Current: m[3] != ""}
		if m[2] == "status" {
			shell.Statuses = append(shell.Statuses, link)
			continue
		}
		shell.Tags = append(shell.Tags, link)
	}
	return shell
}

func alice(section Section) Shell {
	return Shell{Username: "alice", IsAdmin: true, Section: section}
}

func listShell(statusCurrent, tagCurrent string) Shell {
	link := func(label, href string, count int64, current bool) FilterLink {
		return FilterLink{Label: label, Count: count, Href: href, Current: current}
	}
	status := func(key, label string, count int64) FilterLink {
		return link(label, "/?status="+key, count, statusCurrent == key)
	}
	tag := func(name string, count int64) FilterLink {
		return link(name, "/?tag="+name, count, statusCurrent == "" && tagCurrent == name)
	}
	return Shell{
		Username: "alice", IsAdmin: true, Section: SectionBookmarks,
		AllCurrent: statusCurrent == "" && tagCurrent == "",
		Statuses: []FilterLink{
			status("broken", "Broken", 1), status("redirected", "Moved", 1),
			status("ok", "OK", 1), status("unchecked", "Not checked", 60),
		},
		Tags: []FilterLink{tag("rust", 2), tag("tools", 1), tag("web", 2)},
	}
}

func emptyShell(username string, isAdmin bool) Shell {
	return Shell{Username: username, IsAdmin: isAdmin, Section: SectionBookmarks, AllCurrent: true}
}

func realRows() []BookmarkRow {
	day := int64(86_400)
	return []BookmarkRow{
		{
			ID: 3, URL: "https://rust-lang.org/", Title: "Rust language", Note: "systems",
			Tags:      []TagChip{{Name: "rust"}},
			Link:      &LinkCheck{Status: "redirected", CheckedAt: 1577900000, Detail: "https://www.rust-lang.org/"},
			CreatedAt: 1577836800 + 3*day,
		},
		{
			ID: 2, URL: "http://example.org/b", Title: "Beta",
			Tags:      []TagChip{{Name: "tools"}, {Name: "web"}},
			Link:      &LinkCheck{Status: "broken", CheckedAt: 1577900000, Detail: "HTTP 404"},
			CreatedAt: 1577836800 + 2*day,
		},
		{
			ID: 1, URL: "https://example.com/a?x=1&y=<2>", Title: `Example "A"`, Note: "A <note> & more",
			Tags:      []TagChip{{Name: "rust"}, {Name: "web"}},
			Link:      &LinkCheck{Status: "ok", CheckedAt: 1577900000},
			CreatedAt: 1577836800 + day,
		},
	}
}

// pageRows are the filler bookmarks "Page <high>" down to "Page <low>".
func pageRows(high, low int) []BookmarkRow {
	var rows []BookmarkRow
	for i := high; i >= low; i-- {
		rows = append(rows, BookmarkRow{
			ID: int64(4 + i), URL: fmt.Sprintf("https://page.example/%d", i), Title: fmt.Sprintf("Page %d", i),
			CreatedAt: 1577836800 + 1000 + int64(i),
		})
	}
	return rows
}

// withTerms marks the rows the way the list page does for a search.
func withTerms(rows []BookmarkRow, filter search.Filter) []BookmarkRow {
	out := make([]BookmarkRow, len(rows))
	for i, row := range rows {
		row.TitleTerms = filter.Highlights(search.Highlight{Part: search.Title})
		row.DomainTerms = filter.Highlights(search.Highlight{Part: search.URL})
		row.NoteTerms = filter.Highlights(search.Highlight{Part: search.Note})
		tags := make([]TagChip, len(row.Tags))
		for j, tag := range row.Tags {
			tags[j] = TagChip{Name: tag.Name, Terms: filter.Highlights(search.Highlight{Part: search.Tag, Tag: tag.Name})}
		}
		row.Tags = tags
		out[i] = row
	}
	return out
}

func listView(page uint32) ListView { return ListView{Page: page} }

func TestListGoldens(t *testing.T) {
	all := append(realRows(), pageRows(59, 13)...)
	tests := []struct {
		name string
		view BookmarksView
	}{
		{"home-list", BookmarksView{Shell: listShell("", ""), List: listView(1), Rows: all, HasNext: true}},
		{"home-page-2", BookmarksView{Shell: listShell("", ""), List: listView(2), Rows: pageRows(12, 0)}},
		{"home-empty", BookmarksView{Shell: emptyShell("alice", true), List: listView(1)}},
		{"home-bob-empty", BookmarksView{Shell: emptyShell("bob", false), List: listView(1)}},
		{"home-search-no-results", BookmarksView{
			Shell: listShell("", ""), List: ListView{Q: "zzzz", Page: 1},
		}},
		{"home-tag", BookmarksView{
			Shell: listShell("", "web"), List: ListView{Tag: "web", Page: 1}, Rows: realRows()[1:],
		}},
		{"home-status-broken", BookmarksView{
			Shell: listShell("broken", ""), List: ListView{Status: "broken", Page: 1}, Rows: realRows()[1:2],
		}},
		{"home-status-redirected", BookmarksView{
			Shell: listShell("redirected", ""), List: ListView{Status: "redirected", Page: 1}, Rows: realRows()[:1],
		}},
		{"home-status-ok-tag", BookmarksView{
			Shell: listShell("ok", "rust"), List: ListView{Tag: "rust", Status: "ok", Page: 1},
			Rows: realRows()[2:],
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			view := test.view
			view.Now = goldenNow
			assertGolden(t, test.name, BookmarksPage(view))
		})
	}
}

func TestSearchGoldensMarkTheMatches(t *testing.T) {
	tests := []struct {
		name  string
		query string
		rows  []BookmarkRow
	}{
		{"home-search", "rust -tag:tools", []BookmarkRow{realRows()[0], realRows()[2]}},
		{"home-search-highlight-escape", `"A <note>"`, realRows()[2:]},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			filter := search.NewFilter(test.query, "")
			view := BookmarksView{
				Shell: listShell("", ""), List: ListView{Q: test.query, Page: 1},
				Now: goldenNow, Rows: withTerms(test.rows, filter),
			}
			assertGolden(t, test.name, BookmarksPage(view))
		})
	}
}

func TestFormGoldens(t *testing.T) {
	tests := []struct {
		name   string
		render func() string
	}{
		{"login-form", func() string { return LoginPage(LoginView{Next: "/"}) }},
		{"login-form-open-redirect", func() string { return LoginPage(LoginView{Next: "/"}) }},
		{"login-form-next", func() string {
			return LoginPage(LoginView{Next: "/users", SessionEnded: true})
		}},
		{"setup-form", func() string { return SetupPage(SetupView{}) }},
		{"not-found-page-signed-out", func() string { return ErrorPage(404, "That page does not exist.") }},
		{"login-rate-limited-page", func() string { return ErrorPage(429, "Too many attempts. Try again later.") }},
		{"setup-cross-origin-page", func() string { return ErrorPage(403, "This request came from another site.") }},
		{"users-forbidden", func() string { return ErrorPage(403, "admin access required") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) { assertGolden(t, test.name, test.render()) })
	}
}

func TestSignedInPageGoldens(t *testing.T) {
	tests := []struct {
		name    string
		section Section
		render  func(shell Shell) string
	}{
		{"edit-form", SectionBookmarks, func(shell Shell) string {
			shell.AllCurrent = true
			return EditPage(EditView{
				Shell: shell, ID: 1, URL: "https://example.com/a?x=1&y=<2>", Title: `Example "A"`,
				Note: "A <note> & more", Tags: "rust, web", Back: "/",
			})
		}},
		{"edit-submit-bad", SectionBookmarks, func(shell Shell) string {
			shell.AllCurrent = true
			return EditPage(EditView{
				Shell: shell, ID: 1, URL: "nope", Note: "n", Tags: "x", Back: "/",
				Error: "url must start with http:// or https://",
			})
		}},
		{"delete-form", SectionBookmarks, func(shell Shell) string {
			shell.AllCurrent = true
			return ConfirmDeletePage(ConfirmDeleteView{
				Shell: shell, Heading: "Delete this bookmark?", Detail: "Beta", Action: "/bookmarks/delete",
				Hidden:     []HiddenField{{"id", "2"}, {"back", "/"}},
				CancelHref: "/",
			})
		}},
		{"bulk-delete-confirm", SectionBookmarks, func(shell Shell) string {
			shell.AllCurrent = true
			return ConfirmDeletePage(ConfirmDeleteView{
				Shell: shell, Heading: "Delete 2 bookmarks?", Detail: "This cannot be undone.", Action: "/bookmarks/bulk",
				Hidden: []HiddenField{
					{"action", "delete"}, {"confirm", "yes"}, {"q", "x"}, {"tag", "web"}, {"status", "ok"},
					{"page", "2"}, {"id-1", "1"}, {"id-2", "1"},
				},
				CancelHref: ListView{Q: "x", Tag: "web", Status: "ok", Page: 2, Select: true}.Href(),
			})
		}},
		{"users-password-form", SectionUsers, func(shell Shell) string {
			return PasswordPage(PasswordView{Shell: shell, ID: 2, Username: "bob"})
		}},
		{"confirm-form", SectionUsers, func(shell Shell) string { return ConfirmPage(ConfirmView{Shell: shell}) }},
		{"settings", SectionSettings, func(shell Shell) string {
			return SettingsPage(SettingsView{Shell: shell})
		}},
		{"transfer", SectionTransfer, func(shell Shell) string { return TransferPage(TransferView{Shell: shell}) }},
		{"import-report", SectionTransfer, func(shell Shell) string {
			return TransferPage(TransferView{Shell: shell, Report: &ImportReport{
				Added: 1, Invalid: 1,
				Examples: []InvalidExample{{URL: "ftp://nope", Reason: "url must start with http:// or https://"}},
			}})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			shell := shellOf(t, test.name, alice(test.section))
			assertGolden(t, test.name, test.render(shell))
		})
	}
}

func TestUsersGolden(t *testing.T) {
	shell := shellOf(t, "users-list", alice(SectionUsers))
	view := UsersView{Shell: shell, Now: goldenNow, Users: []UserRow{
		{ID: 1, Username: "alice", IsAdmin: true, CreatedAt: 1577836800 + 3600},
		{ID: 2, Username: "bob", CreatedAt: 1577836800 + 2*3600, CanChange: true},
		{ID: 3, Username: "carol", IsDisabled: true, CreatedAt: 1577836800 + 3*3600, CanChange: true},
	}}
	assertGolden(t, "users-list", UsersPage(view))
}

func TestFormErrorGoldens(t *testing.T) {
	tests := []struct {
		name   string
		render func() string
	}{
		{"login-wrong-password-page", func() string {
			return LoginPage(LoginView{Error: "invalid username or password", Username: "alice", Next: "/"})
		}},
		{"setup-bad-token-page", func() string {
			return SetupPage(SetupView{Error: "invalid setup token", Token: "AAAAA-AAAAA", Username: "alice"})
		}},
		{"random", func() string {
			return BookmarksPage(BookmarksView{
				Shell: emptyShell("bob", false), List: listView(1), PickEmpty: true,
			})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) { assertGolden(t, test.name, test.render()) })
	}
}

func TestUsersCreateRefusedGolden(t *testing.T) {
	shell := shellOf(t, "users-create-page-bad", alice(SectionUsers))
	view := UsersView{
		Shell: shell, Now: goldenNow, NewName: "dave", Error: "password must be at least 12 characters",
		Users: []UserRow{
			{ID: 1, Username: "alice", IsAdmin: true, CreatedAt: 1577836800 + 3600},
			{ID: 2, Username: "bob", CreatedAt: 1577836800 + 2*3600, CanChange: true},
			{ID: 3, Username: "carol", IsDisabled: true, CreatedAt: 1577836800 + 3*3600, CanChange: true},
		},
	}
	assertGolden(t, "users-create-page-bad", UsersPage(view))
}

func TestNoticeTextMatchesTheGoldens(t *testing.T) {
	keys := []string{
		"password-changed", "session-ended", "others-ended", "user-changed", "confirmed",
		"bookmark-saved", "bookmark-deleted", "settings-saved", "bulk-done", "check-incomplete", "nothing-selected",
	}
	for _, key := range keys {
		want := `<p class="notice" role="status">` + string(text(NoticeText(key))) + `</p>`
		if !strings.Contains(golden(t, "notice-"+key), want) {
			t.Errorf("notice-%s: golden lacks %q", key, want)
		}
	}
	if NoticeText("bogus") != "" || NoticeText("<script>") != "" {
		t.Error("unknown keys must give no text")
	}
}
