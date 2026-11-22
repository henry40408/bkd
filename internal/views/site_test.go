package views

import (
	"strings"
	"testing"
)

func TestRowShowsThePagesDescriptionAsPlainText(t *testing.T) {
	row := BookmarkRow{ID: 1, URL: "https://a.test/", Title: "A", Site: &SiteInfo{Title: "T", Description: `Says <b>"hi"</b> & more`}}
	requireContains(t, string(bookmarkItem(row, goldenNow, "/", nil)),
		`<p class="site-desc">Says &lt;b&gt;&quot;hi&quot;&lt;/b&gt; &amp; more</p>`)
}

func TestRowHasNoDescriptionLineWithoutOne(t *testing.T) {
	plain := BookmarkRow{ID: 1, URL: "https://a.test/", Title: "A"}
	titleOnly := BookmarkRow{ID: 1, URL: "https://a.test/", Title: "A", Site: &SiteInfo{Title: "Only a title"}}
	for _, row := range []BookmarkRow{plain, titleOnly} {
		requireAbsent(t, string(bookmarkItem(row, goldenNow, "/", nil)), "site-desc")
	}
}

func TestDescriptionComesAfterTheNoteAndBeforeTheTags(t *testing.T) {
	row := BookmarkRow{ID: 1, URL: "https://a.test/", Title: "A", Note: "my note", Tags: []TagChip{{Name: "go"}},
		Site: &SiteInfo{Description: "about it"}}
	body := string(bookmarkItem(row, goldenNow, "/", nil))
	note, description, tags := strings.Index(body, `class="note"`), strings.Index(body, `class="site-desc"`), strings.Index(body, `class="tags"`)
	if note < 0 || note >= description || description >= tags {
		t.Fatalf("order is note %d, description %d, tags %d", note, description, tags)
	}
}

func TestRandomPickShowsTheDescriptionToo(t *testing.T) {
	pick := BookmarkRow{ID: 4, URL: "https://a.test/", Title: "A", Site: &SiteInfo{Description: "about it"}}
	view := BookmarksView{Shell: emptyShell("alice", false), List: ListView{Page: 1}, Now: goldenNow, Pick: &pick}
	requireContains(t, BookmarksPage(view), `<p class="site-desc">about it</p>`)
}

func TestRowOffersARefreshThatPostsBackToTheSameList(t *testing.T) {
	row := BookmarkRow{ID: 12, URL: "https://a.test/", Title: `A "quoted"`}
	requireContains(t, string(bookmarkItem(row, goldenNow, "/?q=go&page=2", nil)),
		`<form class="row-refresh" method="post" action="/bookmarks/12/refresh" data-keep-scroll>`,
		`<input type="hidden" name="back" value="/?q=go&amp;page=2">`,
		`aria-label="Refresh page details of A &quot;quoted&quot;"`,
		`title="Refresh page details"`)
}

func TestSelectModeRowsHaveNoRefreshFormBecauseFormsCannotNest(t *testing.T) {
	ticked := false
	row := BookmarkRow{ID: 12, URL: "https://a.test/", Title: "A"}
	requireAbsent(t, string(bookmarkItem(row, goldenNow, "/", &ticked)), "row-refresh", "/refresh")
}

func TestEditPageShowsThePagesTitleOnlyWhenItDiffers(t *testing.T) {
	view := EditView{Shell: alice(SectionBookmarks), ID: 7, URL: "https://a.test/", Title: "Mine", Back: "/"}
	requireAbsent(t, EditPage(view), "site-title", "data-site-title")

	view.SiteTitle = "Mine"
	requireAbsent(t, EditPage(view), "site-title", "data-site-title")

	view.SiteTitle = `Theirs <i>"x"</i>`
	requireContains(t, EditPage(view),
		`<p class="hint site-title">Page title: Theirs &lt;i&gt;&quot;x&quot;&lt;/i&gt;</p>`,
		`<button class="btn" type="button" data-site-title="Theirs &lt;i&gt;&quot;x&quot;&lt;/i&gt;" data-script-only hidden>Use page title</button>`)
}

func TestEditPageShowsThePagesDescriptionOnlyWhenItDiffers(t *testing.T) {
	view := EditView{Shell: alice(SectionBookmarks), ID: 7, URL: "https://a.test/", Note: "Mine", Back: "/"}
	requireAbsent(t, EditPage(view), "site-description", "data-site-description")

	view.SiteDescription = "Mine"
	requireAbsent(t, EditPage(view), "site-description", "data-site-description")

	view.SiteDescription = `Theirs <i>"x"</i>`
	body := EditPage(view)
	requireContains(t, body,
		`<p class="hint site-description">Page description: Theirs &lt;i&gt;&quot;x&quot;&lt;/i&gt;</p>`,
		`<button class="btn" type="button" data-site-description="Theirs &lt;i&gt;&quot;x&quot;&lt;/i&gt;" data-script-only hidden>Use page description</button>`)
	if strings.Index(body, "<textarea") > strings.Index(body, "site-description") {
		t.Fatal("the page's description should come after the Note field")
	}
}

func TestRefreshedNoticeHasFixedText(t *testing.T) {
	if got := NoticeText("site-refreshed"); got != "Page details updated." {
		t.Fatalf("got %q", got)
	}
}
