package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Ported from the page tests of the Rust server (src/web/tests.rs and
// pages/support.rs).

const bulkContext = "q=&tag=&status=&page=1"

func formPost(uri, fields string) sent {
	return sent{method: "POST", uri: uri}.withRaw("application/x-www-form-urlencoded", fields).originated("http://bkd.test")
}

func locationOf(r reply) string { return r.headers.Get("Location") }

func (a *testApp) exec(query string, args ...any) {
	a.t.Helper()
	if _, err := a.db.Exec(query, args...); err != nil {
		a.t.Fatal(err)
	}
}

func (a *testApp) makeSessionsStale() { a.exec("UPDATE sessions SET confirmed_at = 0") }

func (a *testApp) userFlag(id int64, column string) bool {
	a.t.Helper()
	var set bool
	query := "SELECT " + column + " FROM users WHERE id = ?1"
	if err := a.db.QueryRow(query, id).Scan(&set); err != nil {
		a.t.Fatal(err)
	}
	return set
}

func (a *testApp) saveThroughForm(token, fields string) {
	a.t.Helper()
	r := a.send(formPost("/bookmarks", fields).as(token))
	if r.status != http.StatusSeeOther {
		a.t.Fatalf("save: %d %s", r.status, r.text)
	}
}

func mustContain(t *testing.T, r reply, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(r.text, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func mustLack(t *testing.T, r reply, unwanted string) {
	t.Helper()
	if strings.Contains(r.text, unwanted) {
		t.Errorf("page has %q", unwanted)
	}
}

func mustRedirect(t *testing.T, r reply, to string) {
	t.Helper()
	if r.status != http.StatusSeeOther || locationOf(r) != to {
		t.Fatalf("got %d to %q, want 303 to %q; body %q", r.status, locationOf(r), to, r.text)
	}
}

func adminWithMember(t *testing.T) (app *testApp, admin string, member int64) {
	app = runningApp(t)
	admin = app.signIn("root", true)
	member = app.addUser("mei", false)
	return app, admin, member
}

func TestUsersPageMarksAdminsAndDisabledAccounts(t *testing.T) {
	app, admin, member := adminWithMember(t)
	app.addUser("bob", true)
	app.exec("UPDATE users SET disabled_at = 1 WHERE id = ?1", member)
	page := app.send(get("/users").as(admin))
	mustContain(t, page, "Admin · ", "Disabled · ", `aria-label="Demote bob"`, `aria-label="Enable mei"`)
	mustLack(t, page, `aria-label="Demote root"`)
	mustContain(t, app.send(get("/users?notice=user-changed").as(admin)), "Saved.")
}

func TestAdminPromotesAndDemotesThroughForm(t *testing.T) {
	app, admin, member := adminWithMember(t)
	uri := fmt.Sprintf("/users/%d/role", member)
	mustRedirect(t, app.send(formPost(uri, "is_admin=true").as(admin)), "/users?notice=user-changed")
	if !app.userFlag(member, "is_admin") {
		t.Error("not promoted")
	}
	assertStatus(t, app.send(formPost(uri, "is_admin=false").as(admin)), http.StatusSeeOther)
	if app.userFlag(member, "is_admin") {
		t.Error("not demoted")
	}
}

func TestAdminDisablesAndEnablesThroughForm(t *testing.T) {
	app, admin, member := adminWithMember(t)
	uri := fmt.Sprintf("/users/%d/disabled", member)
	mustRedirect(t, app.send(formPost(uri, "is_disabled=true").as(admin)), "/users?notice=user-changed")
	if !app.userFlag(member, "disabled_at IS NOT NULL") {
		t.Error("not disabled")
	}
	assertStatus(t, app.send(formPost(uri, "is_disabled=false").as(admin)), http.StatusSeeOther)
	if app.userFlag(member, "disabled_at IS NOT NULL") {
		t.Error("not enabled")
	}
}

func TestLastAdminCannotBeDemotedThroughForm(t *testing.T) {
	app, admin, _ := adminWithMember(t)
	r := app.send(formPost("/users/1/role", "is_admin=false").as(admin))
	assertStatus(t, r, http.StatusConflict)
	mustContain(t, r, "at least one active admin")
}

func TestAdminCreatesUserThroughForm(t *testing.T) {
	app, admin, _ := adminWithMember(t)
	fields := "username=Bob&password=" + strings.ReplaceAll(testPassword, " ", "+") + "&is_admin=on"
	mustRedirect(t, app.send(formPost("/users", fields).as(admin)), "/users?notice=user-changed")
	assertStatus(t, app.login("bob", testPassword), http.StatusOK)
	var bob int64
	if err := app.db.QueryRow("SELECT id FROM users WHERE username = 'bob'").Scan(&bob); err != nil {
		t.Fatal(err)
	}
	if !app.userFlag(bob, "is_admin") {
		t.Error("bob is not an admin")
	}
}

func TestRefusedNewUserShowsFormWithWhatWasTyped(t *testing.T) {
	app, admin, _ := adminWithMember(t)
	cases := []struct {
		username, password string
		status             int
	}{
		{"mei", "correct+horse+battery", http.StatusConflict},
		{"bob", "short", http.StatusBadRequest},
	}
	for _, c := range cases {
		r := app.send(formPost("/users", "username="+c.username+"&password="+c.password+"&is_admin=on").as(admin))
		assertStatus(t, r, c.status)
		mustContain(t, r, `role="alert"`, `value="`+c.username+`"`, "checked")
	}
}

func TestAdminResetsPasswordThroughForm(t *testing.T) {
	app, admin, member := adminWithMember(t)
	uri := fmt.Sprintf("/users/%d/password", member)
	form := app.send(get(uri).as(admin))
	assertStatus(t, form, http.StatusOK)
	mustContain(t, form, "Reset password for mei")
	mustRedirect(t, app.send(formPost(uri, "password=a+brand+new+secret").as(admin)), "/users?notice=user-changed")
	assertStatus(t, app.login("mei", "a brand new secret"), http.StatusOK)
	assertStatus(t, app.login("mei", testPassword), http.StatusUnauthorized)
}

func TestTooShortResetPasswordShowsFormAgain(t *testing.T) {
	app, admin, member := adminWithMember(t)
	r := app.send(formPost(fmt.Sprintf("/users/%d/password", member), "password=short").as(admin))
	assertStatus(t, r, http.StatusBadRequest)
	mustContain(t, r, `role="alert"`, "Reset password for mei")
}

func TestMissingUserPagesAreNotFound(t *testing.T) {
	app, admin, _ := adminWithMember(t)
	assertStatus(t, app.send(get("/users/99/password").as(admin)), http.StatusNotFound)
	assertStatus(t, app.send(formPost("/users/99/password", "password=a+brand+new+secret").as(admin)), http.StatusNotFound)
	assertStatus(t, app.send(get("/users/99/delete").as(admin)), http.StatusNotFound)
	assertStatus(t, app.send(formPost("/users/99/delete", "").as(admin)), http.StatusNotFound)
}

func TestDeletingUserAsksFirstThenRemoves(t *testing.T) {
	app, admin, member := adminWithMember(t)
	uri := fmt.Sprintf("/users/%d/delete", member)
	confirm := app.send(get(uri).as(admin))
	assertStatus(t, confirm, http.StatusOK)
	mustContain(t, confirm, "Delete mei?")
	mustRedirect(t, app.send(formPost(uri, "").as(admin)), "/users?notice=user-changed")
	var remaining int
	if err := app.db.QueryRow("SELECT count(*) FROM users WHERE username = 'mei'").Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Errorf("%d mei left", remaining)
	}
}

func TestStalePasswordSendsEveryUserActionToConfirm(t *testing.T) {
	app, admin, member := adminWithMember(t)
	app.makeSessionsStale()
	actions := [][2]string{
		{"/users", "username=bob&password=x"},
		{fmt.Sprintf("/users/%d/disabled", member), "is_disabled=true"},
		{fmt.Sprintf("/users/%d/password", member), "password=a+brand+new+secret"},
		{fmt.Sprintf("/users/%d/delete", member), ""},
	}
	for _, action := range actions {
		mustRedirect(t, app.send(formPost(action[0], action[1]).as(admin)), "/confirm")
	}
	if app.userFlag(member, "disabled_at IS NOT NULL") {
		t.Error("a refused action took effect")
	}
}

func TestConfirmingPasswordLetsChangeThrough(t *testing.T) {
	app, admin, member := adminWithMember(t)
	app.makeSessionsStale()
	page := app.send(get("/confirm").as(admin))
	assertStatus(t, page, http.StatusOK)
	mustContain(t, page, "Confirm your password")
	confirmed := app.send(formPost("/confirm", "password="+strings.ReplaceAll(testPassword, " ", "+")).as(admin))
	mustRedirect(t, confirmed, "/users?notice=confirmed")
	mustContain(t, app.send(get(locationOf(confirmed)).as(admin)), "Password confirmed.")
	change := formPost(fmt.Sprintf("/users/%d/disabled", member), "is_disabled=true").as(admin)
	mustRedirect(t, app.send(change), "/users?notice=user-changed")
}

func TestWrongPasswordOnConfirmShowsFormAgain(t *testing.T) {
	app, admin, _ := adminWithMember(t)
	app.makeSessionsStale()
	r := app.send(formPost("/confirm", "password=nope").as(admin))
	assertStatus(t, r, http.StatusForbidden)
	mustContain(t, r, `role="alert"`, "Confirm your password")
}

func TestConfirmPageShowsNoticeAndNeedsSession(t *testing.T) {
	app, admin, _ := adminWithMember(t)
	mustContain(t, app.send(get("/confirm?notice=user-changed").as(admin)), "Saved.")
	assertStatus(t, app.send(get("/confirm")), http.StatusSeeOther)
}

func TestSetupPageIsOpenOnlyBeforeFirstAdmin(t *testing.T) {
	waiting := awaitingSetupApp(t)
	page := waiting.send(get("/setup"))
	assertStatus(t, page, http.StatusOK)
	mustContain(t, page, "Set up bkd")
	mustRedirect(t, waiting.send(get("/login")), "/setup")
	mustRedirect(t, runningApp(t).send(get("/setup")), "/login")
}

func TestSetupFormCreatesFirstAdminAndSignsThemIn(t *testing.T) {
	app := awaitingSetupApp(t)
	fields := "token=" + app.setupDisplay + "&username=henry&password=correct+horse+battery"
	r := app.send(formPost("/setup", fields))
	mustRedirect(t, r, "/")
	if r.headers.Get("Set-Cookie") == "" {
		t.Error("no session cookie")
	}
}

func TestRefusedSetupFormKeepsTokenAndUsername(t *testing.T) {
	app := awaitingSetupApp(t)
	cases := []struct {
		token, password string
		status          int
	}{
		{"00000-00000", "correct+horse+battery", http.StatusForbidden},
		{app.setupDisplay, "short", http.StatusBadRequest},
	}
	for _, c := range cases {
		r := app.send(formPost("/setup", "token="+c.token+"&username=henry&password="+c.password))
		assertStatus(t, r, c.status)
		mustContain(t, r, `role="alert"`, `value="`+c.token+`"`, `value="henry"`)
		if r.headers.Get("Set-Cookie") != "" {
			t.Error("a refused setup set a cookie")
		}
	}
}

func TestGuessingSetupTokenOnFormEndsInTooManyAttempts(t *testing.T) {
	app := awaitingSetupApp(t)
	guess := formPost("/setup", "token=00000-00000&username=henry&password=correct+horse+battery")
	for range 5 {
		app.send(guess)
	}
	blocked := app.send(guess)
	assertStatus(t, blocked, http.StatusTooManyRequests)
	mustContain(t, blocked, "Too many attempts")
	if blocked.headers.Get("Retry-After") == "" {
		t.Error("no Retry-After")
	}
}

func TestGuessingPasswordsOnFormEndsInTooManyAttempts(t *testing.T) {
	app := runningApp(t)
	app.addUser("henry", false)
	var last reply
	for range 21 {
		last = app.send(formPost("/login", "username=henry&password=nope"))
		if last.status == http.StatusTooManyRequests {
			break
		}
	}
	assertStatus(t, last, http.StatusTooManyRequests)
	mustContain(t, last, "Too many attempts")
	if last.headers.Get("Retry-After") == "" {
		t.Error("no Retry-After")
	}
}

func TestLoginPageSendsSignedInVisitorToNextPage(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	mustRedirect(t, app.send(get("/login?next=%2Faccount").as(token)), "/account")
}

func TestUnknownPagesAndAPIPathsAreNotFoundInTheirOwnWay(t *testing.T) {
	app := runningApp(t)
	page := app.send(get("/nowhere"))
	assertStatus(t, page, http.StatusNotFound)
	mustContain(t, page, "That page does not exist.")
	api := app.send(get("/api/nowhere"))
	assertStatus(t, api, http.StatusNotFound)
	if api.text != "" {
		t.Errorf("api body %q", api.text)
	}
}

func TestDatabaseFaultShowsPlainErrorPage(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	app.exec("DROP TABLE bookmarks")
	r := app.send(get("/").as(token))
	assertStatus(t, r, http.StatusInternalServerError)
	mustContain(t, r, "Something went wrong.")
	mustLack(t, r, "bookmarks")
}

func revokeAction(t *testing.T, page reply) string {
	t.Helper()
	end := strings.Index(page.text, `/revoke"`)
	start := strings.LastIndex(page.text[:end], "/account/sessions/")
	if end < 0 || start < 0 {
		t.Fatal("no revoke button")
	}
	return page.text[start:end] + "/revoke"
}

func TestAccountPageListsOtherBrowsersWithButtonToEndEach(t *testing.T) {
	app := runningApp(t)
	here := app.signIn("henry", false)
	mustLack(t, app.send(get("/account").as(here)), "Sign out all other browsers")
	app.login("henry", testPassword)
	page := app.send(get("/account").as(here))
	mustContain(t, page, "Unknown browser", "Sign out all other browsers")
	ended := app.send(formPost(revokeAction(t, page), "").as(here))
	mustRedirect(t, ended, "/account?notice=session-ended")
	after := app.send(get(locationOf(ended)).as(here))
	mustContain(t, after, "That browser was signed out.")
	mustLack(t, after, "Sign out all other browsers")
}

func TestEndingSessionThatDoesNotExistIsNotFound(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	assertStatus(t, app.send(formPost("/account/sessions/0123456789abcdef/revoke", "").as(token)), http.StatusNotFound)
}

func TestSigningOutOtherBrowsersKeepsThisOne(t *testing.T) {
	app := runningApp(t)
	here := app.signIn("henry", false)
	other := sessionToken(t, app.login("henry", testPassword))
	r := app.send(formPost("/account/sessions/revoke-others", "").as(here))
	mustRedirect(t, r, "/account?notice=others-ended")
	mustContain(t, app.send(get(locationOf(r)).as(here)), "All other browsers were signed out.")
	assertStatus(t, app.send(get("/account").as(other)), http.StatusSeeOther)
}

func passwordChangeFields(current, next, again string) string {
	encode := func(text string) string { return strings.ReplaceAll(text, " ", "+") }
	return "current=" + encode(current) + "&new=" + encode(next) + "&again=" + encode(again)
}

func TestChangingPasswordThroughFormKeepsThisBrowserOnly(t *testing.T) {
	app := runningApp(t)
	here := app.signIn("henry", false)
	other := sessionToken(t, app.login("henry", testPassword))
	r := app.send(formPost("/account/password", passwordChangeFields(testPassword, "a brand new secret", "a brand new secret")).as(here))
	mustRedirect(t, r, "/account?notice=password-changed")
	fresh := sessionToken(t, r)
	mustContain(t, app.send(get(locationOf(r)).as(fresh)), "Password changed.")
	assertStatus(t, app.send(get("/account").as(other)), http.StatusSeeOther)
	assertStatus(t, app.login("henry", "a brand new secret"), http.StatusOK)
}

func TestRefusedPasswordChangeShowsAccountPageWithReason(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	cases := []struct {
		fields string
		status int
		reason string
	}{
		{passwordChangeFields(testPassword, "another long secret", "different one"), http.StatusBadRequest, "are different"},
		{passwordChangeFields("wrong one", "another long secret", "another long secret"), http.StatusForbidden, "current password is wrong"},
		{passwordChangeFields(testPassword, "short", "short"), http.StatusBadRequest, "at least"},
		{passwordChangeFields(testPassword, testPassword, testPassword), http.StatusBadRequest, "must differ"},
	}
	for _, c := range cases {
		r := app.send(formPost("/account/password", c.fields).as(token))
		assertStatus(t, r, c.status)
		mustContain(t, r, `role="alert"`, c.reason, "This browser")
	}
	assertStatus(t, app.login("henry", testPassword), http.StatusOK)
}

func TestTransferPageLinksToBothExports(t *testing.T) {
	app := runningApp(t)
	page := app.send(get("/transfer").as(app.signIn("henry", false)))
	assertStatus(t, page, http.StatusOK)
	mustContain(t, page, "/api/export?format=html", "/api/export?format=json")
	mustLack(t, page, "import-result")
}

const firefoxFile = `<!DOCTYPE NETSCAPE-Bookmark-file-1>
<DL><p>
<DT><A HREF="https://a.test/">A</A>
<DT><A HREF="https://b.test/">B</A>
<DT><A HREF="https://a.test/">A again</A>
<DT><A HREF="place:parent=1">Smart</A>
</DL><p>`

func upload(token, field, contents string) sent {
	body := "--XBOUNDARYX\r\nContent-Disposition: form-data; name=\"" + field + "\"; filename=\"b.html\"\r\n" +
		"Content-Type: text/html\r\n\r\n" + contents + "\r\n--XBOUNDARYX--\r\n"
	return sent{method: "POST", uri: "/transfer/import"}.
		withRaw("multipart/form-data; boundary=XBOUNDARYX", body).originated("http://bkd.test").as(token)
}

func TestImportReportsWhatWasAddedSkippedAndIgnored(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	first := app.send(upload(token, "file", firefoxFile))
	mustContain(t, first, "Added 2, skipped 1 you already had", "ignored 1 that are not web links", "place:parent=1")
	mustContain(t, app.send(upload(token, "file", firefoxFile)), "Added 0, skipped 3 you already had")
}

func TestImportWithoutFileShowsFormAgain(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	for _, c := range [][2]string{{"file", ""}, {"other", "text"}} {
		r := app.send(upload(token, c[0], c[1]))
		assertStatus(t, r, http.StatusBadRequest)
		mustContain(t, r, "Choose a file first.")
	}
}

func TestBrokenUploadIsRefused(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	broken := sent{method: "POST", uri: "/transfer/import"}.
		withRaw("multipart/form-data; boundary=XBOUNDARYX", "not multipart").originated("http://bkd.test").as(token)
	assertStatus(t, app.send(broken), http.StatusBadRequest)
}

func TestRandomPageShowsOneBookmarkWithWaysToGoOn(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	app.saveThroughForm(token, "url=https%3A%2F%2Fa.test&title=Aaa&note=worth+a+read&tags=rust")
	app.saveThroughForm(token, "url=https%3A%2F%2Fb.test&title=Bbb&note=&tags=go")
	pick := app.send(get("/random?tag=rust").as(token))
	assertStatus(t, pick, http.StatusOK)
	mustContain(t, pick, "Random bookmark", "worth a read", "Another", "/bookmarks/1/edit", "Close")
	empty := app.send(get("/random?q=zebra&status=broken").as(token))
	assertStatus(t, empty, http.StatusOK)
	mustContain(t, empty, "Nothing to pick from here.")
}

func TestTagSearchMarksTheTagThatMatched(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	app.saveThroughForm(token, "url=https%3A%2F%2Fa.test&title=Aaa&note=&tags=rust")
	mustContain(t, app.send(get("/?q=tag:rust").as(token)), "<mark>rust</mark>")
}

func TestOldBookmarkIsDatedInMonths(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	app.saveThroughForm(token, "url=https%3A%2F%2Fa.test&title=Aaa&note=&tags=")
	app.exec("UPDATE bookmarks SET created_at = ?1", time.Now().Unix()-65*86400)
	mustContain(t, app.send(get("/").as(token)), "2 months ago")
}

func TestListFilteredByLinkStatusHandsStatusOnToEdit(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	app.saveThroughForm(token, "url=https%3A%2F%2Fa.test&title=Aaa&note=&tags=")
	app.exec("UPDATE bookmarks SET link_status = 'broken', link_checked_at = 1, link_detail = 'HTTP 404'")
	mustContain(t, app.send(get("/?status=broken").as(token)), "/bookmarks/1/edit?back=%2F%3Fstatus%3Dbroken")
}

func linkSite() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("/moved", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/ok", http.StatusMovedPermanently) })
	mux.HandleFunc("/missing", http.NotFound)
	mux.HandleFunc("/forbidden", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) })
	return httptest.NewServer(mux)
}

func TestLinkCheckResultIsShownInWordsOnTheRow(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	site := linkSite()
	defer site.Close()
	for i, path := range []string{"/ok", "/moved", "/missing", "/forbidden"} {
		url := strings.NewReplacer(":", "%3A", "/", "%2F").Replace(site.URL + path)
		app.saveThroughForm(token, fmt.Sprintf("url=%s&title=Page%d&note=&tags=", url, i))
	}
	body := "id-1=1&id-2=1&id-3=1&id-4=1&action=check&" + bulkContext
	mustRedirect(t, app.send(formPost("/bookmarks/bulk", body).as(token)), "/?notice=bulk-done")
	mustContain(t, app.send(get("/").as(token)),
		"Link OK", "Link moved", "now at", "Broken link", "HTTP 404", "Link not confirmed", "HTTP 403")
}

func TestLinkCheckThatRunsOutOfTimeSaysSo(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer slow.Close()
	app.saveThroughForm(token, "url="+strings.NewReplacer(":", "%3A", "/", "%2F").Replace(slow.URL)+"&title=Slow&note=&tags=")
	app.app.checkLimit = 50 * time.Millisecond
	r := app.send(formPost("/bookmarks/bulk", "id-1=1&action=check&"+bulkContext).as(token))
	mustRedirect(t, r, "/?notice=check-incomplete")
}

func twoBookmarksInSelectMode(app *testApp) string {
	token := app.signIn("henry", false)
	app.saveThroughForm(token, "url=https%3A%2F%2Fa.test&title=Aaa&note=&tags=")
	app.saveThroughForm(token, "url=https%3A%2F%2Fb.test&title=Bbb&note=&tags=")
	return token
}

func TestLinkCheckOfGoneBookmarksShowsListWithReason(t *testing.T) {
	app := runningApp(t)
	token := twoBookmarksInSelectMode(app)
	r := app.send(formPost("/bookmarks/bulk", "id-1=1&id-99=1&action=check&"+bulkContext).as(token))
	assertStatus(t, r, http.StatusNotFound)
	mustContain(t, r, "no longer exist")
}

func TestSecondLinkCheckAtTheSameTimeIsAskedToWait(t *testing.T) {
	app := runningApp(t)
	token := twoBookmarksInSelectMode(app)
	release, ok := app.app.activeChecks.Start(1)
	if !ok {
		t.Fatal("slot not free")
	}
	defer release()
	r := app.send(formPost("/bookmarks/bulk", "id-1=1&action=check&"+bulkContext).as(token))
	assertStatus(t, r, http.StatusTooManyRequests)
	if r.headers.Get("Retry-After") == "" {
		t.Error("no Retry-After")
	}
}

func TestBulkDeleteAsksFirstAndCountsWhatIsTicked(t *testing.T) {
	app := runningApp(t)
	token := twoBookmarksInSelectMode(app)
	two := app.send(formPost("/bookmarks/bulk", "id-1=1&id-2=1&action=delete&"+bulkContext).as(token))
	assertStatus(t, two, http.StatusOK)
	mustContain(t, two, "Delete 2 bookmarks?", `name="confirm" value="yes"`, `name="id-2" value="1"`)
	mustContain(t, app.send(formPost("/bookmarks/bulk", "id-1=1&action=delete&"+bulkContext).as(token)), "Delete 1 bookmark?")
	list := app.send(get("/").as(token))
	mustContain(t, list, "Aaa", "Bbb")
}

func TestUnknownBulkActionIsRefusedWithSelectionKept(t *testing.T) {
	app := runningApp(t)
	token := twoBookmarksInSelectMode(app)
	r := app.send(formPost("/bookmarks/bulk", "id-1=1&action=explode&"+bulkContext).as(token))
	assertStatus(t, r, http.StatusBadRequest)
	mustContain(t, r, "Choose an action.", `name="id-1" value="1" checked`)
}

func TestBookmarkPagesForMissingBookmarkAreNotFound(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	fields := "url=https%3A%2F%2Fa.test&title=New&note=&tags="
	for _, s := range []sent{
		get("/bookmarks/99/edit").as(token),
		get("/bookmarks/99/delete").as(token),
		formPost("/bookmarks/99/edit", fields).as(token),
		formPost("/bookmarks/delete", "id=99").as(token),
	} {
		r := app.send(s)
		assertStatus(t, r, http.StatusNotFound)
		mustContain(t, r, `role="alert"`)
	}
}

func TestRefusedEditShowsEditFormWithWhatWasTyped(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	app.saveThroughForm(token, "url=https%3A%2F%2Fa.test&title=Aaa&note=&tags=")
	fields := "url=not+a+url&title=Mine&note=&tags=&back=%2F%3Ftag%3Drust"
	r := app.send(formPost("/bookmarks/1/edit", fields).as(token))
	assertStatus(t, r, http.StatusBadRequest)
	mustContain(t, r, `role="alert"`, `value="Mine"`, `name="back" value="/?tag=rust"`)
}

func TestFormPostNeedsMatchingOrigin(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	fields := "url=https%3A%2F%2Fa.test&title=Aaa&note=&tags="
	good := app.send(formPost("/bookmarks", fields).as(token))
	assertStatus(t, good, http.StatusSeeOther)
	for _, origin := range []string{"", "null", "http://evil.test"} {
		r := app.send(formPost("/bookmarks", fields).originated(origin).as(token))
		assertStatus(t, r, http.StatusForbidden)
	}
}

func TestOnlyPathOnThisSiteIsSameSitePath(t *testing.T) {
	for _, good := range []string{"/", "/account", "/?q=a&page=2", "/a/b?x=%2F%2F"} {
		if !sameSitePath(good) {
			t.Errorf("%q refused", good)
		}
	}
	for _, bad := range []string{"", "evil.test", "https://evil.test", "//evil.test", `/\evil.test`,
		`\\evil.test`, "javascript:alert(1)", "/a\r\nSet-Cookie: x=1", "/a\tb"} {
		if sameSitePath(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	if pathOrHome("//evil.test") != "/" {
		t.Error("an off-site path was not sent home")
	}
}
