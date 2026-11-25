package server

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestBookmarkPages(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("olga", false)
	intruder := app.signIn("ivan", false)

	t.Run("another user's bookmark answers 404", func(t *testing.T) {
		id := app.createBookmark(token, "secret").ID
		edit := fmt.Sprintf("/bookmarks/%d/edit", id)
		requests := map[string]sent{
			"edit form":   get(edit),
			"delete form": get(fmt.Sprintf("/bookmarks/%d/delete", id)),
			"edit submit": formPost(edit, "url=https%3A%2F%2Fevil.example"),
		}
		for name, request := range requests {
			if got := app.send(request.as(intruder)).status; got != http.StatusNotFound {
				t.Errorf("%s: status = %d, want 404", name, got)
			}
		}
	})
	t.Run("the edit form shows the bookmark escaped", func(t *testing.T) {
		app.saveThroughForm(token, "url=https%3A%2F%2Fexample.com%2Fedit&title="+url.QueryEscape("<b>bold</b>")+"&tags=a,b")
		id := app.newestBookmarkID(token)
		r := app.send(get(fmt.Sprintf("/bookmarks/%d/edit?back=%%2F%%3Ftag%%3Da", id)).as(token))
		assertStatus(t, r, http.StatusOK)
		mustContain(t, r, fmt.Sprintf(`action="/bookmarks/%d/edit"`, id), "&lt;b&gt;bold&lt;/b&gt;", "a, b", "https://example.com/edit")
		mustLack(t, r, "<b>bold</b>")
	})
	t.Run("a submitted edit is saved and redirects back", func(t *testing.T) {
		id := app.createBookmark(token, "first").ID
		edit := fmt.Sprintf("/bookmarks/%d/edit", id)
		r := app.send(formPost(edit, "url=https%3A%2F%2Fexample.org&title=renamed&back=%2F%3Fpage%3D2").as(token))
		assertStatus(t, r, http.StatusSeeOther)
		mustContain(t, reply{text: locationOf(r)}, "page=2", "bookmark-saved")
		mustContain(t, app.send(get("/").as(token)), "renamed")
	})
	t.Run("a bad URL shows the error and keeps the input", func(t *testing.T) {
		id := app.createBookmark(token, "kept-original").ID
		edit := fmt.Sprintf("/bookmarks/%d/edit", id)
		r := app.send(formPost(edit, "url=not-a-url&title=kept").as(token))
		assertStatus(t, r, http.StatusBadRequest)
		mustContain(t, r, fmt.Sprintf(`action="%s"`, edit), "kept", "url must start with http")
	})
	t.Run("the create form keeps the input on error", func(t *testing.T) {
		r := app.send(formPost("/bookmarks", "url=not-a-url&title=typed").as(token))
		assertStatus(t, r, http.StatusBadRequest)
		mustContain(t, r, "typed", "not-a-url", "url must start with http")
	})
	t.Run("the delete confirmation names the bookmark", func(t *testing.T) {
		app.saveThroughForm(token, "url=https%3A%2F%2Fexample.com%2Fdoomed&title="+url.QueryEscape("doomed <i>"))
		id := app.newestBookmarkID(token)
		r := app.send(get(fmt.Sprintf("/bookmarks/%d/delete?back=%%2F%%3Fpage%%3D3", id)).as(token))
		assertStatus(t, r, http.StatusOK)
		mustContain(t, r, "Delete this bookmark?", "This cannot be undone.", `action="/bookmarks/delete"`, fmt.Sprintf(`name="id" value="%d"`, id), "doomed &lt;i&gt;")
		mustLack(t, r, "doomed <i>")
	})
	t.Run("a confirmed delete removes the bookmark", func(t *testing.T) {
		id := app.createBookmark(token, "removable").ID
		r := app.send(formPost("/bookmarks/delete", fmt.Sprintf("id=%d&back=%%2F", id)).as(token))
		assertStatus(t, r, http.StatusSeeOther)
		mustContain(t, reply{text: locationOf(r)}, "bookmark-deleted")
		mustLack(t, app.send(get("/").as(token)), "removable")
	})
}

func (a *testApp) newestBookmarkID(token string) int64 {
	a.t.Helper()
	var id int64
	if err := a.db.QueryRow("SELECT max(id) FROM bookmarks WHERE user_id = ?1", a.ownerOf(token)).Scan(&id); err != nil {
		a.t.Fatal(err)
	}
	return id
}

func TestListPageFiltersByTagAndSearchAndPicksRandom(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("olga", false)
	app.createBookmark(token, "alpha", "x")
	app.createBookmark(token, "beta")
	const alphaLink, betaLink = "https://example.com/alpha", "https://example.com/beta"

	byTag := app.send(get("/?tag=x").as(token))
	mustContain(t, byTag, alphaLink)
	mustLack(t, byTag, betaLink)
	bySearch := app.send(get("/?q=beta&page=1").as(token))
	mustContain(t, bySearch, betaLink)
	mustLack(t, bySearch, alphaLink)

	picked := app.send(get("/random?tag=x").as(token))
	assertStatus(t, picked, http.StatusOK)
	mustContain(t, picked, `aria-label="Random bookmark"`, alphaLink)
	mustLack(t, picked, betaLink)
	empty := app.send(get("/random?tag=missing").as(token))
	assertStatus(t, empty, http.StatusOK)
	mustContain(t, empty, "Nothing to pick from here.")
}

func TestListPageNamesTheListCountsItAndOffersChips(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("olga", false)
	app.createBookmark(token, "alpha", "x")
	app.createBookmark(token, "beta")
	app.exec("UPDATE bookmarks SET link_status = 'broken' WHERE title = 'alpha'")

	all := app.send(get("/").as(token))
	mustContain(t, all, "<h1>All bookmarks</h1>", ">2 bookmarks</span>", `<span class="count">2</span></a>`)
	mustLack(t, all, `class="active"`)

	tagged := app.send(get("/?tag=x&status=broken").as(token))
	mustContain(t, tagged, "<h1>Tagged “x”</h1>", ">1 of 2</span>",
		`<a class="chip" href="/?status=broken">Tag: x`, `<a class="chip" href="/?tag=x">Status: Broken`)
	mustContain(t, app.send(get("/?status=broken").as(token)), "<h1>Broken links</h1>")
	mustContain(t, app.send(get("/?q=beta&status=broken").as(token)), "<h1>Search results</h1>", ">0 of 2</span>")
}

func TestThePickCanBeEditedAndDeleted(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("olga", false)
	id := app.createBookmark(token, "alpha").ID
	picked := app.send(get("/random").as(token))
	mustContain(t, picked, fmt.Sprintf(`href="/bookmarks/%d/edit?back=%%2F"`, id),
		fmt.Sprintf(`href="/bookmarks/%d/delete?back=%%2F"`, id))
}

func TestTheEditFormOffersTheNewAddressOnlyOfAMovedLink(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("olga", false)
	id := app.createBookmark(token, "alpha").ID
	edit := fmt.Sprintf("/bookmarks/%d/edit", id)
	cases := []struct {
		name, status, detail string
		offered              bool
	}{
		{"never checked", "", "", false},
		{"moved", "redirected", "https://new.example/a?b=1", true},
		{"moved without a target", "redirected", "", false},
		{"broken", "broken", "https://new.example/a", false},
		{"ok", "ok", "https://new.example/a", false},
	}
	for _, c := range cases {
		app.exec("UPDATE bookmarks SET link_status = NULLIF(?1, ''), link_checked_at = 1, link_detail = ?2", c.status, c.detail)
		r := app.send(get(edit).as(token))
		assertStatus(t, r, http.StatusOK)
		if c.offered {
			mustContain(t, r, "This link moved to https://new.example/a?b=1", `data-moved-to="https://new.example/a?b=1"`)
			continue
		}
		if strings.Contains(r.text, "data-moved-to") || strings.Contains(r.text, "This link moved") {
			t.Errorf("%s: the form offers a new address", c.name)
		}
	}
}

func TestAccountPages(t *testing.T) {
	app := runningApp(t)
	admin := app.signIn("root", true)

	t.Run("pages need a session", func(t *testing.T) {
		for _, uri := range []string{"/settings", "/account", "/bookmarks/1/edit", "/bookmarks/1/delete", "/confirm"} {
			r := app.send(get(uri))
			assertStatus(t, r, http.StatusSeeOther)
			mustContain(t, reply{text: locationOf(r)}, "/login")
		}
	})
	t.Run("settings save the link check choice", func(t *testing.T) {
		assertStatus(t, app.send(get("/settings").as(admin)), http.StatusOK)
		r := app.send(formPost("/settings", "link_check_enabled=on").as(admin))
		mustRedirect(t, r, "/settings?notice=settings-saved")
		mustContain(t, app.send(get("/settings?notice=settings-saved").as(admin)), "checked")
		if enabled, _ := app.storedSettings("root"); !enabled {
			t.Fatal("setting not saved")
		}
		mustRedirect(t, app.send(formPost("/settings", "").as(admin)), "/settings?notice=settings-saved")
		if enabled, _ := app.storedSettings("root"); enabled {
			t.Error("unticked box did not turn the setting off")
		}
	})
	t.Run("a signed in user skips the login page", func(t *testing.T) {
		mustRedirect(t, app.send(get("/login?next=%2Fsettings").as(admin)), "/settings")
	})
	t.Run("the login page keeps next", func(t *testing.T) {
		r := app.send(get("/login?notice=session-ended&next=%2Faccount"))
		assertStatus(t, r, http.StatusOK)
		mustContain(t, r, `value="/account"`)
	})
	t.Run("the confirm form asks for the password", func(t *testing.T) {
		r := app.send(get("/confirm").as(admin))
		assertStatus(t, r, http.StatusOK)
		mustContain(t, r, `type="password"`)
	})
}

func TestLoginPageSendsToSetupWhileSetupIsOpen(t *testing.T) {
	app := awaitingSetupApp(t)
	mustRedirect(t, app.send(get("/login")), "/setup")
}

func TestBulkActions(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("olga", false)
	bulk := func(body string) reply { return app.send(formPost("/bookmarks/bulk", body).as(token)) }

	t.Run("page field parsing", func(t *testing.T) {
		id := app.createBookmark(token, "one").ID
		selected := fmt.Sprintf("id-%d=1&action=add_tags&tags=t", id)
		tests := []struct{ name, body, wantLocation string }{
			{"bad page falls back to one", selected + "&page=x", "/?notice=bulk-done"},
			{"page beyond the limit is clamped", selected + "&page=5000000", "/?page=1000000&notice=bulk-done"},
			{"page too large for a number falls back to one", selected + "&page=99999999999", "/?notice=bulk-done"},
			{"stray and bad ids are ignored", "id-x=1&other=1&action=add_tags&tags=t", "/?select=1&notice=nothing-selected"},
		}
		for _, test := range tests {
			if r := bulk(test.body); r.status != http.StatusSeeOther || locationOf(r) != test.wantLocation {
				t.Errorf("%s: got %d to %q, want 303 to %q", test.name, r.status, locationOf(r), test.wantLocation)
			}
		}
	})
	t.Run("tags are added then removed", func(t *testing.T) {
		id := app.createBookmark(token, "two").ID
		body := func(action string) string {
			return fmt.Sprintf("id-%d=1&action=%s&tags=fresh&%s", id, action, bulkContext)
		}
		mustRedirect(t, bulk(body("add_tags")), "/?notice=bulk-done")
		mustRedirect(t, bulk(body("remove_tags")), "/?notice=bulk-done")
		r := bulk(body("remove_tags"))
		assertStatus(t, r, http.StatusBadRequest)
		mustContain(t, r, "None of the selected bookmarks has that tag.")
	})
	t.Run("a confirmed delete removes the rows", func(t *testing.T) {
		id := app.createBookmark(token, "three").ID
		selected := fmt.Sprintf("id-%d=1&", id)
		mustRedirect(t, bulk(selected+"action=delete&confirm=yes&"+bulkContext), "/?notice=bulk-done")
		stale := bulk(selected + "action=add_tags&tags=t&" + bulkContext)
		assertStatus(t, stale, http.StatusNotFound)
		mustContain(t, stale, staleSelectionMessage)
	})
}

func TestAdminPages(t *testing.T) {
	app, admin, member := adminWithMember(t)
	plain := sessionToken(t, app.login("mei", testPassword))
	app.createBookmark(admin, "one")

	t.Run("the users page lists accounts for an admin only", func(t *testing.T) {
		r := app.send(get("/users").as(admin))
		assertStatus(t, r, http.StatusOK)
		mustContain(t, r, "root", "mei", `action="/users"`)
		assertStatus(t, app.send(get("/users").as(plain)), http.StatusForbidden)
	})
	t.Run("a plain user cannot change accounts", func(t *testing.T) {
		user := fmt.Sprintf("/users/%d", member)
		forms := []sent{
			formPost("/users", "username=eve&password="+url.QueryEscape(testPassword)),
			formPost(user+"/role", "is_admin=true"),
			formPost(user+"/disabled", "disabled=true"),
			formPost(user+"/password", "password="+url.QueryEscape(testPassword)),
			formPost(user+"/delete", "confirm=yes"),
		}
		for _, form := range forms {
			assertStatus(t, app.send(form.as(plain)), http.StatusForbidden)
		}
		if app.userFlag(member, "is_admin") || app.userFlag(member, "disabled_at IS NOT NULL") {
			t.Error("a plain user changed an account")
		}
		var accounts int
		if err := app.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&accounts); err != nil || accounts != 2 {
			t.Errorf("accounts = %d, error %v; want 2", accounts, err)
		}
	})
	t.Run("forms refuse malformed values", func(t *testing.T) {
		role := fmt.Sprintf("/users/%d/role", member)
		tests := []struct {
			name    string
			request sent
			want    int
		}{
			{"page is not a number", get("/?page=x"), http.StatusBadRequest},
			{"id is not a number", formPost("/bookmarks/delete", "id=x"), http.StatusUnprocessableEntity},
			{"id is missing", formPost("/bookmarks/delete", ""), http.StatusUnprocessableEntity},
			{"role is not a boolean", formPost(role, "is_admin=maybe"), http.StatusUnprocessableEntity},
			{"role is missing", formPost(role, ""), http.StatusUnprocessableEntity},
		}
		for _, test := range tests {
			if got := app.send(test.request.as(admin)).status; got != test.want {
				t.Errorf("%s: status = %d, want %d", test.name, got, test.want)
			}
		}
	})
}

func TestShellPagesAnswer500WhenTheBookmarkCountsFail(t *testing.T) {
	app, admin, member := adminWithMember(t)
	app.createBookmark(admin, "one")
	app.breakTable("bookmarks")
	user := fmt.Sprintf("/users/%d", member)
	requests := []sent{
		get("/confirm"), get("/settings"), get("/users"),
		get(user + "/password"), get(user + "/delete"),
		formPost("/bookmarks/bulk", "id-1=1&action=delete&"+bulkContext),
	}
	for _, request := range requests {
		t.Run(request.uri, func(t *testing.T) {
			assertStatus(t, app.send(request.as(admin)), http.StatusInternalServerError)
		})
	}
}

func TestTheSidebarStacksStatusAndTagAndCountsWhatIsLeft(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("olga", false)
	app.createBookmark(token, "alpha", "x")
	app.createBookmark(token, "beta", "x")
	app.createBookmark(token, "gamma", "y")
	app.createBookmark(token, "delta")
	app.exec("UPDATE bookmarks SET link_status = 'broken' WHERE title IN ('alpha', 'gamma')")

	page := app.send(get("/?tag=x&status=broken").as(token))
	mustContain(t, page,
		// A status link keeps the tag, a tag link keeps the status.
		`<a href="/?tag=x&amp;status=unchecked">Not checked<span class="count">1</span></a>`,
		`<a href="/?tag=y&amp;status=broken">y<span class="count">1</span></a>`,
		// The tag counts only broken bookmarks, the status counts only x ones.
		`>Broken<span class="count">1</span>`, `>x<span class="count">1</span>`,
		// The whole account stays the total.
		`<span class="count">4</span>`)
}
