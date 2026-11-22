package views

import "testing"

func addPageWith(site SiteDetails) string {
	return BookmarksPage(BookmarksView{
		Shell: emptyShell("alice", false), List: listView(1),
		Add: AddForm{Open: true, URL: site.URL, Site: site},
	})
}

func editPageWith(site SiteDetails) string {
	return EditPage(EditView{Shell: emptyShell("alice", false), ID: 7, URL: site.URL, Back: "/", Site: site})
}

func TestFormsWithoutDetailsKeepTheUsualPlaceholderAndCarryEmptyFields(t *testing.T) {
	for name, page := range map[string]string{"add": addPageWith(SiteDetails{}), "edit": editPageWith(SiteDetails{})} {
		t.Run(name, func(t *testing.T) {
			requireContains(t, page,
				`data-site-preview`,
				`placeholder="Leave blank to use the domain"`, `data-default-placeholder="Leave blank to use the domain"`,
				`<input type="hidden" name="site_url" value="">`,
				`<input type="hidden" name="site_title" value="">`,
				`<input type="hidden" name="site_description" value="">`,
				`<p class="hint" role="status" data-site-status="" hidden></p>`)
			requireAbsent(t, page, `<textarea name="note" rows="2" placeholder`, `<textarea name="note" rows="3" placeholder`)
		})
	}
}

func TestFormsShowTheDetailsAsPlaceholdersAndCarryThemInHiddenFields(t *testing.T) {
	site := SiteDetails{URL: "https://example.com/a?x=1&y=2", Title: `Tom & "Jerry"`, Description: "<b>About</b>"}
	for name, page := range map[string]string{"add": addPageWith(site), "edit": editPageWith(site)} {
		t.Run(name, func(t *testing.T) {
			requireContains(t, page,
				`placeholder="Tom &amp; &quot;Jerry&quot;"`,
				`placeholder="&lt;b&gt;About&lt;/b&gt;"`,
				`name="site_url" value="https://example.com/a?x=1&amp;y=2"`,
				`name="site_title" value="Tom &amp; &quot;Jerry&quot;"`,
				`name="site_description" value="&lt;b&gt;About&lt;/b&gt;"`,
				`data-default-placeholder="Leave blank to use the domain"`)
			requireAbsent(t, page, `<b>About</b>`)
		})
	}
}

func TestTheDetailsAreNeverTheValueOfTheTitleOrTheNote(t *testing.T) {
	page := EditPage(EditView{
		Shell: emptyShell("alice", false), ID: 7, URL: "https://example.com/", Title: "Mine", Note: "My note", Back: "/",
		Site: SiteDetails{URL: "https://example.com/", Title: "Their title", Description: "Their description"},
	})
	requireContains(t, page, `name="title" value="Mine"`, `>My note</textarea>`)
	requireAbsent(t, page, `value="Their title" placeholder`, `>Their description</textarea>`)
}

// The button reads the page with a script, so it stays hidden until one runs.
func TestFormsOfferToReadThePageAgainButOnlyToAScript(t *testing.T) {
	for name, page := range map[string]string{"add": addPageWith(SiteDetails{}), "edit": editPageWith(SiteDetails{})} {
		t.Run(name, func(t *testing.T) {
			requireContains(t, page,
				`<button class="btn quiet" type="button" data-site-reread="" data-script-only hidden>Read the page again</button>`)
		})
	}
}

func TestTheListShowsNoPageDetails(t *testing.T) {
	requireAbsent(t, BookmarksPage(BookmarksView{
		Shell: emptyShell("alice", false), List: listView(1), Rows: realRows(),
	}), "site-desc", "row-refresh")
}
