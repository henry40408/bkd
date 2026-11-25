package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/henry40408/bkd/internal/linkcheck"
)

// saveSettings posts the settings form. A nil agent leaves the box out, as a
// page from before the upgrade does.
func saveSettings(app *testApp, token string, enabled bool, agent *string) reply {
	fields := url.Values{}
	if enabled {
		fields.Set("link_check_enabled", "on")
	}
	if agent != nil {
		fields.Set("link_check_user_agent", *agent)
	}
	return app.send(formPost("/settings", fields.Encode()).as(token))
}

func agentOf(text string) *string { return &text }

// storedSettings is what the database holds for the user.
func (a *testApp) storedSettings(username string) (enabled bool, agent string) {
	a.t.Helper()
	query := "SELECT link_check_enabled, COALESCE(link_check_user_agent, '') FROM users WHERE username = ?1"
	if err := a.db.QueryRow(query, username).Scan(&enabled, &agent); err != nil {
		a.t.Fatal(err)
	}
	return enabled, agent
}

func (a *testApp) assertSettings(username string, wantEnabled bool, wantAgent string) {
	a.t.Helper()
	if enabled, agent := a.storedSettings(username); enabled != wantEnabled || agent != wantAgent {
		a.t.Errorf("settings of %s = (%v, %q), want (%v, %q)", username, enabled, agent, wantEnabled, wantAgent)
	}
}

func TestSettingsStartWithoutAUserAgent(t *testing.T) {
	app := runningApp(t)
	app.signIn("henry", false)
	app.assertSettings("henry", false, "")
}

func TestAUserAgentIsSavedTrimmedKeptWhenOmittedAndCleared(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)

	assertStatus(t, saveSettings(app, token, true, agentOf("  Custom/1.0  ")), http.StatusSeeOther)
	app.assertSettings("henry", true, "Custom/1.0")

	// A form without the box must not wipe the User-Agent.
	assertStatus(t, saveSettings(app, token, false, nil), http.StatusSeeOther)
	app.assertSettings("henry", false, "Custom/1.0")

	assertStatus(t, saveSettings(app, token, false, agentOf("")), http.StatusSeeOther)
	app.assertSettings("henry", false, "")
}

func TestABadUserAgentIsRefusedAndChangesNothing(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	cases := map[string]string{
		"a line break":      "bkd\r\nX-Injected: 1",
		"non-ASCII":         "bkd é",
		"over the limit":    strings.Repeat("a", linkcheck.MaxUserAgentLength+1),
		"a control letter":  "a\x07b",
		"a multi-byte text": strings.Repeat("é", 3),
	}
	for name, agent := range cases {
		t.Run(name, func(t *testing.T) {
			r := saveSettings(app, token, true, agentOf(agent))
			assertStatus(t, r, http.StatusBadRequest)
			mustContain(t, r, "user agent")
			app.assertSettings("henry", false, "")
		})
	}
	atLimit := strings.Repeat("a", linkcheck.MaxUserAgentLength)
	assertStatus(t, saveSettings(app, token, false, agentOf(atLimit)), http.StatusSeeOther)
}

func TestEachUserHasTheirOwnUserAgent(t *testing.T) {
	app := runningApp(t)
	henry := app.signIn("henry", false)
	app.signIn("mei", false)
	assertStatus(t, saveSettings(app, henry, false, agentOf("Henry/1")), http.StatusSeeOther)
	app.assertSettings("mei", false, "")
}

// userAgentSite records the User-Agent of each request by its path.
func userAgentSite() (site *httptest.Server, seen func(path string) string) {
	var mu sync.Mutex
	agents := map[string]string{}
	site = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		agents[r.URL.Path] = r.Header.Get("User-Agent")
	}))
	return site, func(path string) string {
		mu.Lock()
		defer mu.Unlock()
		return agents[path]
	}
}

func TestTheCheckButtonSendsTheUsersUserAgent(t *testing.T) {
	app := runningApp(t)
	henry := app.signIn("henry", false)
	mei := app.signIn("mei", false)
	site, seen := userAgentSite()
	defer site.Close()
	assertStatus(t, saveSettings(app, henry, false, agentOf("Henry/1")), http.StatusSeeOther)
	henrysPage := app.addBookmarkAt(henry, site.URL+"/henry")
	meisPage := app.addBookmarkAt(mei, site.URL+"/mei")

	assertStatus(t, app.send(checkAs(henry, int(henrysPage))), http.StatusOK)
	assertStatus(t, app.send(checkAs(mei, int(meisPage))), http.StatusOK)

	if got := seen("/henry"); got != "Henry/1" {
		t.Errorf("henry's request carried %q", got)
	}
	if got := seen("/mei"); got != linkcheck.DefaultUserAgent() {
		t.Errorf("mei's request carried %q, want the default", got)
	}
}

func TestTheNoScriptCheckSendsTheUsersUserAgent(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	site, seen := userAgentSite()
	defer site.Close()
	assertStatus(t, saveSettings(app, token, false, agentOf("Henry/1")), http.StatusSeeOther)
	app.addBookmarkAt(token, site.URL+"/page")

	body := "id-1=1&action=check&" + bulkContext
	mustRedirect(t, app.send(formPost("/bookmarks/bulk", body).as(token)), "/?notice=bulk-done")

	if got := seen("/page"); got != "Henry/1" {
		t.Errorf("the request carried %q", got)
	}
}

func TestTheSettingsPageShowsTheUserAgentBoxWithTheDefaultAsHint(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	r := app.send(get("/settings").as(token))
	assertStatus(t, r, http.StatusOK)
	mustContain(t, r,
		`name="link_check_user_agent"`,
		`placeholder="`+linkcheck.DefaultUserAgent()+`"`,
		`maxlength="200"`,
	)
}

func TestTheSettingsFormSavesTheUserAgentAndShowsItBack(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)

	r := app.send(formPost("/settings", "link_check_enabled=on&link_check_user_agent=+Custom%2F1.0+").as(token))
	mustRedirect(t, r, "/settings?notice=settings-saved")
	mustContain(t, app.send(get("/settings").as(token)), `value="Custom/1.0"`)
	app.assertSettings("henry", true, "Custom/1.0")

	// An empty box goes back to the default.
	mustRedirect(t, app.send(formPost("/settings", "link_check_user_agent=").as(token)), "/settings?notice=settings-saved")
	app.assertSettings("henry", false, "")
}

func TestTheSettingsFormShowsARefusedUserAgentAgainWithTheReason(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	assertStatus(t, saveSettings(app, token, false, agentOf("Kept/1")), http.StatusSeeOther)

	r := app.send(formPost("/settings", "link_check_enabled=on&link_check_user_agent=caf%C3%A9").as(token))
	assertStatus(t, r, http.StatusBadRequest)
	mustContain(t, r, "user agent must use printable ASCII characters only", `value="café"`,
		`name="link_check_enabled" value="on" checked>`)
	app.assertSettings("henry", false, "Kept/1")
}

// A script or a page from before the upgrade posts no User-Agent box.
func TestTheSettingsFormWithoutTheUserAgentBoxKeepsTheUserAgent(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	assertStatus(t, saveSettings(app, token, false, agentOf("Kept/1")), http.StatusSeeOther)

	assertStatus(t, app.send(formPost("/settings", "link_check_enabled=on").as(token)), http.StatusSeeOther)
	app.assertSettings("henry", true, "Kept/1")

	assertStatus(t, app.send(formPost("/settings", "link_check_enabled=on&link_check_user_agent=").as(token)), http.StatusSeeOther)
	app.assertSettings("henry", true, "")
}

// Both settings go in one statement: when it fails, neither changes.
func TestAFailedSettingsWriteChangesNeitherSetting(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	assertStatus(t, saveSettings(app, token, false, agentOf("Kept/1")), http.StatusSeeOther)
	app.exec("CREATE TRIGGER refuse_enabled BEFORE UPDATE OF link_check_enabled ON users " +
		"BEGIN SELECT RAISE(ABORT, 'refused'); END")

	r := saveSettings(app, token, true, agentOf("New/1"))
	assertStatus(t, r, http.StatusInternalServerError)
	app.exec("DROP TRIGGER refuse_enabled")
	app.assertSettings("henry", false, "Kept/1")
}

func TestAUserAgentWithMarkupIsEscapedOnThePage(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	assertStatus(t, saveSettings(app, token, false, agentOf(`"><script>alert(1)</script>`)), http.StatusSeeOther)

	r := app.send(get("/settings").as(token))
	mustLack(t, r, "<script>alert(1)")
	mustContain(t, r, `value="&quot;&gt;&lt;script&gt;alert(1)&lt;/script&gt;"`)
}
