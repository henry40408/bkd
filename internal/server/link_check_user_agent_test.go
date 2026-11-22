package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/henry40408/bkd/internal/linkcheck"
)

func putSettings(app *testApp, token string, body map[string]any) reply {
	return app.send(put("/api/settings", body).as(token))
}

func TestSettingsStartWithoutAUserAgent(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	got := app.send(get("/api/settings").as(token)).text
	if want := `{"link_check_enabled":false,"link_check_user_agent":""}`; got != want {
		t.Fatalf("settings = %s, want %s", got, want)
	}
}

func TestAUserAgentIsSavedTrimmedKeptWhenOmittedAndCleared(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	agentOf := func(r reply) any {
		t.Helper()
		assertStatus(t, r, http.StatusOK)
		return r.json()["link_check_user_agent"]
	}

	saved := putSettings(app, token, map[string]any{"link_check_enabled": true, "link_check_user_agent": "  Custom/1.0  "})
	if got := agentOf(saved); got != "Custom/1.0" {
		t.Fatalf("answered User-Agent = %v", got)
	}
	if got := agentOf(app.send(get("/api/settings").as(token))); got != "Custom/1.0" {
		t.Fatalf("stored User-Agent = %v", got)
	}

	// A client that only knows link_check_enabled must not wipe the User-Agent.
	omitted := putSettings(app, token, map[string]any{"link_check_enabled": false})
	if got := agentOf(omitted); got != "Custom/1.0" {
		t.Errorf("User-Agent after an update without it = %v", got)
	}
	if omitted.json()["link_check_enabled"] != false {
		t.Error("the update without a User-Agent did not change the other setting")
	}
	if got := agentOf(putSettings(app, token, map[string]any{"link_check_enabled": false, "link_check_user_agent": nil})); got != "Custom/1.0" {
		t.Errorf("User-Agent after null = %v", got)
	}

	if got := agentOf(putSettings(app, token, map[string]any{"link_check_enabled": false, "link_check_user_agent": ""})); got != "" {
		t.Errorf("User-Agent after clearing = %v", got)
	}
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
			r := putSettings(app, token, map[string]any{"link_check_enabled": true, "link_check_user_agent": agent})
			assertStatus(t, r, http.StatusBadRequest)
			mustContain(t, r, "user agent")
			got := app.send(get("/api/settings").as(token)).text
			if want := `{"link_check_enabled":false,"link_check_user_agent":""}`; got != want {
				t.Errorf("settings changed to %s", got)
			}
		})
	}
	atLimit := strings.Repeat("a", linkcheck.MaxUserAgentLength)
	assertStatus(t, putSettings(app, token, map[string]any{"link_check_enabled": false, "link_check_user_agent": atLimit}), http.StatusOK)
}

func TestEachUserHasTheirOwnUserAgent(t *testing.T) {
	app := runningApp(t)
	henry := app.signIn("henry", false)
	mei := app.signIn("mei", false)
	assertStatus(t, putSettings(app, henry, map[string]any{"link_check_enabled": false, "link_check_user_agent": "Henry/1"}), http.StatusOK)
	if got := app.send(get("/api/settings").as(mei)).json()["link_check_user_agent"]; got != "" {
		t.Errorf("mei's User-Agent = %v", got)
	}
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
	assertStatus(t, putSettings(app, henry, map[string]any{"link_check_enabled": false, "link_check_user_agent": "Henry/1"}), http.StatusOK)
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
	assertStatus(t, putSettings(app, token, map[string]any{"link_check_enabled": false, "link_check_user_agent": "Henry/1"}), http.StatusOK)
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
	if got := app.send(get("/api/settings").as(token)).json()["link_check_user_agent"]; got != "Custom/1.0" {
		t.Errorf("stored User-Agent = %v", got)
	}

	// An empty box goes back to the default.
	mustRedirect(t, app.send(formPost("/settings", "link_check_user_agent=").as(token)), "/settings?notice=settings-saved")
	if got := app.send(get("/api/settings").as(token)).json()["link_check_user_agent"]; got != "" {
		t.Errorf("User-Agent after clearing = %v", got)
	}
}

func TestTheSettingsFormShowsARefusedUserAgentAgainWithTheReason(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	assertStatus(t, putSettings(app, token, map[string]any{"link_check_enabled": false, "link_check_user_agent": "Kept/1"}), http.StatusOK)

	r := app.send(formPost("/settings", "link_check_enabled=on&link_check_user_agent=caf%C3%A9").as(token))
	assertStatus(t, r, http.StatusBadRequest)
	mustContain(t, r, "user agent must use printable ASCII characters only", `value="café"`,
		`name="link_check_enabled" value="on" checked>`)
	stored := app.send(get("/api/settings").as(token)).json()
	if stored["link_check_user_agent"] != "Kept/1" || stored["link_check_enabled"] != false {
		t.Errorf("a refused form changed the settings to %v", stored)
	}
}

// A script or a page from before the upgrade posts no User-Agent box.
func TestTheSettingsFormWithoutTheUserAgentBoxKeepsTheUserAgent(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	assertStatus(t, putSettings(app, token, map[string]any{"link_check_enabled": false, "link_check_user_agent": "Kept/1"}), http.StatusOK)

	assertStatus(t, app.send(formPost("/settings", "link_check_enabled=on").as(token)), http.StatusSeeOther)
	stored := app.send(get("/api/settings").as(token)).json()
	if stored["link_check_user_agent"] != "Kept/1" || stored["link_check_enabled"] != true {
		t.Errorf("settings = %v, want the User-Agent kept and the check on", stored)
	}

	assertStatus(t, app.send(formPost("/settings", "link_check_enabled=on&link_check_user_agent=").as(token)), http.StatusSeeOther)
	if got := app.send(get("/api/settings").as(token)).json()["link_check_user_agent"]; got != "" {
		t.Errorf("an empty box left the User-Agent %q, want it cleared", got)
	}
}

// Both settings go in one statement: when it fails, neither changes.
func TestAFailedSettingsWriteChangesNeitherSetting(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	assertStatus(t, putSettings(app, token, map[string]any{"link_check_enabled": false, "link_check_user_agent": "Kept/1"}), http.StatusOK)
	app.exec("CREATE TRIGGER refuse_enabled BEFORE UPDATE OF link_check_enabled ON users " +
		"BEGIN SELECT RAISE(ABORT, 'refused'); END")

	r := putSettings(app, token, map[string]any{"link_check_enabled": true, "link_check_user_agent": "New/1"})
	assertStatus(t, r, http.StatusInternalServerError)
	app.exec("DROP TRIGGER refuse_enabled")
	stored := app.send(get("/api/settings").as(token)).json()
	if stored["link_check_user_agent"] != "Kept/1" || stored["link_check_enabled"] != false {
		t.Errorf("a failed write left %v", stored)
	}
}

func TestAUserAgentWithMarkupIsEscapedOnThePage(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	assertStatus(t, putSettings(app, token, map[string]any{
		"link_check_enabled": false, "link_check_user_agent": `"><script>alert(1)</script>`,
	}), http.StatusOK)

	r := app.send(get("/settings").as(token))
	mustLack(t, r, "<script>alert(1)")
	mustContain(t, r, `value="&quot;&gt;&lt;script&gt;alert(1)&lt;/script&gt;"`)
}
