package views

import (
	"strings"
	"testing"
)

func requireContains(t *testing.T, page string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(page, want) {
			t.Errorf("page does not contain %q", want)
		}
	}
}

func requireAbsent(t *testing.T, page string, unwanted ...string) {
	t.Helper()
	for _, bad := range unwanted {
		if strings.Contains(page, bad) {
			t.Errorf("page contains %q", bad)
		}
	}
}

func TestLoginPageShowsFormNoticeAndError(t *testing.T) {
	page := LoginPage(LoginView{Error: "Wrong <login>", Username: `a"b`, Next: "/x?a=1&b=2", SessionEnded: true})
	requireContains(t, page, `action="/login"`, `method="post"`, SessionEndedText,
		"Wrong &lt;login&gt;", `name="next"`, "a&quot;b", "a=1&amp;b=2", "Sign in")
}

func TestLoginPageHidesNoticeAndErrorByDefault(t *testing.T) {
	requireAbsent(t, LoginPage(LoginView{}), SessionEndedText, `role="alert"`)
}

func TestSetupPageAsksForTokenUsernameAndPassword(t *testing.T) {
	page := SetupPage(SetupView{Error: "Bad token", Token: "tok<1>", Username: "root"})
	requireContains(t, page, `action="/setup"`, "Set up bkd", "Bad token", "tok&lt;1&gt;",
		`value="root"`, `name="password"`, "Create admin")
}

func TestConfirmPageShowsErrorAndCancelLink(t *testing.T) {
	page := ConfirmPage(ConfirmView{Shell: alice(SectionUsers), Error: "Wrong password"})
	requireContains(t, page, "Confirm your password", `action="/confirm"`, "Wrong password", `href="/users"`)
}

func TestEditPageEscapesFieldsAndKeepsBackLink(t *testing.T) {
	page := EditPage(EditView{
		Shell: alice(SectionBookmarks), ID: 7, URL: "https://x.test/?a=1&b=2", Title: "<script>",
		Note: "R&D", Tags: "a, b", Error: "Nope", Back: "/?page=2",
	})
	requireContains(t, page, `action="/bookmarks/7/edit"`, `name="back"`, "a=1&amp;b=2",
		"&lt;script&gt;", "R&amp;D", "Nope", `href="/?page=2"`)
	requireAbsent(t, page, "<script>")
}

func TestConfirmDeletePageSendsHiddenFieldsAndOffersKeep(t *testing.T) {
	page := ConfirmDeletePage(ConfirmDeleteView{
		Shell: alice(SectionBookmarks), Heading: "Delete <b>?", Detail: "Gone & forgotten",
		Action: "/bookmarks/3/delete", CancelHref: "/?page=2",
		Hidden: []HiddenField{{Name: "back", Value: "/?q=a&b"}, {Name: "id", Value: "3"}},
	})
	requireContains(t, page, "Delete &lt;b&gt;?", "Gone &amp; forgotten", `action="/bookmarks/3/delete"`,
		`name="back"`, "/?q=a&amp;b", `name="id"`, `href="/?page=2"`, "Keep")
}

func TestConfirmDeletePageWithoutHiddenFieldsStillHasButtons(t *testing.T) {
	page := ConfirmDeletePage(ConfirmDeleteView{Shell: alice(SectionBookmarks), Action: "/x", CancelHref: "/"})
	requireContains(t, page, `action="/x"`, `href="/"`, "Keep", `type="submit"`)
	requireAbsent(t, page, `type="hidden"`)
}

func TestLinkStatusesRenderLabelGlyphAndText(t *testing.T) {
	tests := []struct {
		name string
		link *LinkCheck
		want []string
	}{
		{"ok", &LinkCheck{Status: "ok", CheckedAt: 1577900000}, []string{`class="link-note ok"`, "<b>Link OK</b>, checked ", `title="Checked 2020-01-01`}},
		{"redirected", &LinkCheck{Status: "redirected", Detail: "https://new.test/"}, []string{`class="link-note redirected"`, "<b>Link moved</b>, now at https://new.test/, checked "}},
		{"broken", &LinkCheck{Status: "broken", Detail: "HTTP <404>"}, []string{`class="link-note broken"`, "<b>Broken link</b>: HTTP &lt;404&gt;, checked "}},
		{"broken without detail", &LinkCheck{Status: "broken"}, []string{`class="link-note broken"`, "<b>Broken link</b>, checked "}},
		{"unknown", &LinkCheck{Status: "unknown", Detail: "timed out"}, []string{`class="link-note unknown"`, "<b>Link not confirmed</b>: timed out, checked ", `d="M5.6 6a2.4`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := BookmarkRow{ID: 1, URL: "https://a.test/", Title: "A", Link: tt.link}
			requireContains(t, string(bookmarkItem(row, goldenNow, "/", nil)), tt.want...)
		})
	}
}

func TestUncheckedLinksShowNoLinkLine(t *testing.T) {
	for _, link := range []*LinkCheck{nil, {Status: "unchecked"}} {
		row := BookmarkRow{ID: 1, URL: "https://a.test/", Title: "A", Link: link}
		requireAbsent(t, string(bookmarkItem(row, goldenNow, "/", nil)), "link-note")
	}
}

func TestBookmarkItemEscapesTextAndLinksToEditAndDelete(t *testing.T) {
	row := BookmarkRow{
		ID: 9, URL: "https://a.test/", Title: "<script>&", Note: "n<b>",
		Tags: []TagChip{{Name: "go"}, {Name: "web&co"}},
	}
	item := string(bookmarkItem(row, goldenNow, "/?page=2", nil))
	requireContains(t, item, "&lt;script&gt;&amp;", "n&lt;b&gt;", `data-id="9"`,
		"/bookmarks/9/edit?back=", "/bookmarks/9/delete?back=", `class="tag"`, "web&amp;co")
	requireAbsent(t, item, "<script>", `type="checkbox"`)
}

func TestBookmarkItemShowsCheckboxInSelectMode(t *testing.T) {
	row := BookmarkRow{ID: 1, URL: "https://a.test/", Title: "A"}
	yes, no := true, false
	requireContains(t, string(bookmarkItem(row, goldenNow, "/", &yes)), `type="checkbox"`, "checked")
	unticked := string(bookmarkItem(row, goldenNow, "/", &no))
	requireContains(t, unticked, `type="checkbox"`)
	requireAbsent(t, unticked, "checked")
}

func TestBookmarkItemWithoutTagsOrNoteOmitsThem(t *testing.T) {
	row := BookmarkRow{ID: 1, URL: "https://a.test/", Title: "A"}
	requireAbsent(t, string(bookmarkItem(row, goldenNow, "/", nil)), `class="tags"`, `class="note"`)
}

func TestErrorPageShowsStatusMessageAndWayBack(t *testing.T) {
	page := ErrorPage(404, "Not <found>")
	requireContains(t, page, "<h1>404</h1>", "Not &lt;found&gt;", `role="alert"`, `href="/"`, "<title>404 · bkd</title>")
}

func TestNoticeTextKnownKeysHaveDistinctText(t *testing.T) {
	keys := []string{
		"password-changed", "session-ended", "others-ended", "user-changed", "confirmed",
		"bookmark-saved", "bookmark-deleted", "settings-saved", "bulk-done", "check-incomplete",
		"nothing-selected",
	}
	seen := map[string]string{}
	for _, key := range keys {
		got := NoticeText(key)
		if got == "" {
			t.Errorf("NoticeText(%q) is empty", key)
		}
		if other, dup := seen[got]; dup {
			t.Errorf("NoticeText(%q) repeats the text of %q", key, other)
		}
		seen[got] = key
	}
}

func TestNoticeTextUnknownKeyIsEmpty(t *testing.T) {
	for _, key := range []string{"", "<script>", "nope"} {
		if got := NoticeText(key); got != "" {
			t.Errorf("NoticeText(%q) = %q", key, got)
		}
	}
}

func TestTransferPageHasImportFormAndExportLinks(t *testing.T) {
	page := TransferPage(TransferView{Shell: alice(SectionTransfer), Error: "Not HTML"})
	requireContains(t, page, `action="/transfer/import"`, `multipart/form-data`, "Not HTML",
		"/api/export?format=html", "/api/export?format=json")
	requireAbsent(t, page, "import-result")
}

func TestTransferPageImportsThroughTheDropBox(t *testing.T) {
	page := TransferPage(TransferView{Shell: alice(SectionTransfer)})
	requireContains(t, page, `<form class="drop" method="post" action="/transfer/import" enctype="multipart/form-data">`,
		`<label class="field">Bookmarks file <input type="file" name="file" accept=".html,.htm,text/html" required></label>`,
		`<button class="btn primary ico" type="submit" title="Import bookmarks">`,
		`<span class="lbl">Import bookmarks</span>`,
		`<p class="hint" data-script-only hidden>Or drop the file in this box.</p>`,
		`<a class="btn" href="/api/export?format=html" download><svg`,
		`<a class="btn" href="/api/export?format=json" download><svg`)
	requireAbsent(t, page, `role="alert"`)
}

func TestSignInAndSetUpShowTheBrandAboveTheTitle(t *testing.T) {
	login := LoginPage(LoginView{})
	requireContains(t, login, `<main class="auth" id="main">`, `<span class="brand">bkd</span><h1>Sign in</h1>`)
	requireAbsent(t, login, `class="card"`, `class="login"`)
	setup := SetupPage(SetupView{})
	requireContains(t, setup, `<span class="brand">bkd</span><h1>Set up bkd</h1>`)
}

func TestTransferPageReportsImportWithExamples(t *testing.T) {
	report := &ImportReport{
		Added: 3, Duplicates: 2, Invalid: 1,
		Examples: []InvalidExample{{URL: "javascript:<x>", Reason: "not a web link"}},
	}
	page := TransferPage(TransferView{Shell: alice(SectionTransfer), Report: report})
	requireContains(t, page, "Added 3, skipped 2 you already had, and ignored 1 that are not web links.",
		"javascript:&lt;x&gt; (not a web link)")
}

func TestTransferPageReportWithoutInvalidOmitsIgnoredPart(t *testing.T) {
	page := TransferPage(TransferView{Shell: alice(SectionTransfer), Report: &ImportReport{Added: 1}})
	requireContains(t, page, "Added 1, skipped 0 you already had.")
	requireAbsent(t, page, "ignored", "(not a web link)")
}

func TestPasswordPageTargetsTheUser(t *testing.T) {
	page := PasswordPage(PasswordView{Shell: alice(SectionUsers), ID: 12, Username: "bo<b>", Error: "Too short"})
	requireContains(t, page, "Reset password for bo&lt;b&gt;", `action="/users/12/password"`,
		"Too short", `minlength="12"`, `href="/users"`)
}

func TestUserActionsOfferTheOppositeChange(t *testing.T) {
	tests := []struct {
		name          string
		user          UserRow
		want, notWant string
	}{
		{"active member can be promoted and disabled", UserRow{ID: 5, Username: "bo"}, "Promote bo", "Demote bo"},
		{"admin can be demoted", UserRow{ID: 5, Username: "bo", IsAdmin: true}, "Demote bo", "Promote bo"},
		{"active member can be disabled", UserRow{ID: 5, Username: "bo"}, "Disable bo", "Enable bo"},
		{"disabled member can be enabled", UserRow{ID: 5, Username: "bo", IsDisabled: true}, "Enable bo", "Disable bo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actions := string(userActions(tt.user))
			requireContains(t, actions, tt.want, "/users/5/role", "/users/5/disabled",
				"/users/5/password", "/users/5/delete", "Reset password of bo", "Delete bo")
			requireAbsent(t, actions, tt.notWant)
		})
	}
}

func TestUsersPageShowsActionsOnlyForOtherUsers(t *testing.T) {
	page := UsersPage(UsersView{Shell: alice(SectionUsers), Now: goldenNow, Users: []UserRow{
		{ID: 1, Username: "alice", IsAdmin: true},
		{ID: 2, Username: "bob", CanChange: true},
	}})
	requireContains(t, page, "/users/2/role")
	requireAbsent(t, page, "/users/1/role")
}

func TestUserRowShowsBadgesOnlyForItsState(t *testing.T) {
	plain := string(userRow(UserRow{ID: 3, Username: "bo"}, goldenNow))
	requireContains(t, plain, `<li><span class="who">bo<span class="hint">Created `)
	requireAbsent(t, plain, "badge")
	both := string(userRow(UserRow{ID: 3, Username: "bo", IsAdmin: true, IsDisabled: true}, goldenNow))
	requireContains(t, both, `<span class="badge admin">Admin</span><span class="badge">Disabled</span>`)
}

func TestUserActionsKeepTheirLabelsNextToTheirIcons(t *testing.T) {
	actions := string(userActions(UserRow{ID: 5, Username: "bo", IsDisabled: true}))
	requireContains(t, actions, `title="Enable"`, `<span class="lbl">Enable</span>`, `title="Reset password"`,
		`<span class="lbl">Reset password</span>`, `class="btn quiet ico danger-text"`, `<span class="lbl">Delete</span>`)
}
