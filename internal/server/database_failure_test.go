package server

import (
	"fmt"
	"net/http"
	"testing"
)

// breakTable makes every statement on the table fail, as a damaged database
// would, while the rest of the schema keeps working.
func (a *testApp) breakTable(table string) {
	a.t.Helper()
	a.exec("ALTER TABLE " + table + " RENAME TO " + table + "_gone")
}

// A database failure behind a signed-in request is a 500 that never shows the
// cause, never a success and never a crash.
func TestBookmarkRoutesAnswer500WhenTheBookmarkTableFails(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("alice", false)
	app.breakTable("bookmarks")
	cases := []sent{
		get("/api/export?format=json"),
		get("/api/export?format=html"),
		get("/"),
		get("/random"),
		get("/bookmarks/1/edit"),
		get("/bookmarks/1/delete"),
		get("/transfer"),
	}
	for _, c := range cases {
		r := app.send(c.as(token))
		if r.status != http.StatusInternalServerError {
			t.Errorf("%s %s: status = %d, want 500; body %q", c.method, c.uri, r.status, r.text)
		}
	}
}

// refuseChanges makes the table reject the given kinds of write while it stays
// readable.
func (a *testApp) refuseChanges(table string, events ...string) {
	a.t.Helper()
	for _, event := range events {
		a.exec("CREATE TRIGGER refuse_" + table + "_" + event + " BEFORE " + event + " ON " + table +
			" BEGIN SELECT RAISE(ABORT, 'refused'); END")
	}
}

func expectEvery500(t *testing.T, app *testApp, token string, cases []sent) {
	t.Helper()
	for _, c := range cases {
		r := app.send(c.as(token))
		if r.status != http.StatusInternalServerError {
			t.Errorf("%s %s: status = %d, want 500; body %q", c.method, c.uri, r.status, r.text)
		}
	}
}

func TestAccountRoutesAnswer500WhenTheSessionTableRefusesChanges(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("alice", false)
	app.login("alice", testPassword)
	var otherSession string
	if err := app.db.QueryRow("SELECT substr(token_hash, 1, 16) FROM sessions ORDER BY rowid DESC LIMIT 1").Scan(&otherSession); err != nil {
		t.Fatal(err)
	}
	app.refuseChanges("sessions", "INSERT", "DELETE", "UPDATE")
	passwords := passwordChangeFields(testPassword, "a brand new secret", "a brand new secret")
	expectEvery500(t, app, token, []sent{
		formPost("/login", "username=alice&password=correct+horse+battery"),
		formPost("/logout", ""),
		formPost("/confirm", "password=correct+horse+battery"),
		formPost("/account/sessions/revoke-others", ""),
		formPost("/account/sessions/"+otherSession+"/revoke", ""),
		formPost("/account/password", passwords),
	})
}

func TestAccountRoutesAnswer500WhenTheUserTableRefusesChanges(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("alice", false)
	app.refuseChanges("users", "UPDATE", "DELETE", "INSERT")
	passwords := passwordChangeFields(testPassword, "a brand new secret", "a brand new secret")
	expectEvery500(t, app, token, []sent{
		formPost("/settings", "link_check_enabled=on"),
		formPost("/settings", "link_check_enabled=on&link_check_user_agent=Agent%2F1"),
		formPost("/account/password", passwords),
	})
}

func TestSetupAnswers500WhenTheUserTableRefusesTheFirstAdmin(t *testing.T) {
	app := awaitingSetupApp(t)
	app.refuseChanges("users", "INSERT")
	fields := "token=" + app.setupDisplay + "&username=henry&password=correct+horse+battery"
	expectEvery500(t, app, "", []sent{
		formPost("/setup", fields),
	})
}

func TestUserPagesAnswer500WhenTheUserTableRefusesChanges(t *testing.T) {
	app := runningApp(t)
	admin := app.confirmed("root")
	app.addUser("mei", false)
	app.refuseChanges("users", "UPDATE", "DELETE", "INSERT")
	var mei int64
	if err := app.db.QueryRow("SELECT id FROM users WHERE username = 'mei'").Scan(&mei); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/users/%d", mei)
	expectEvery500(t, app, admin, []sent{
		formPost("/users", "username=bob&password=correct+horse+battery"),
		formPost(base+"/role", "is_admin=true"),
		formPost(base+"/disabled", "is_disabled=true"),
		formPost(base+"/password", "password=a+brand+new+secret"),
		formPost(base+"/delete", ""),
	})
}

func TestBookmarkPagesAnswer500WhenTheBookmarkTableRefusesChanges(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("alice", false)
	app.createBookmark(token, "first")
	app.createBookmark(token, "second")
	app.refuseChanges("bookmarks", "UPDATE", "DELETE", "INSERT")
	fields := "url=https%3A%2F%2Fexample.com%2Fz&title=Z&note=&tags="
	expectEvery500(t, app, token, []sent{
		formPost("/bookmarks", fields),
		formPost("/bookmarks/1/edit", fields),
		formPost("/bookmarks/delete", "id=1"),
		formPost("/bookmarks/bulk", "id-1=1&action=delete&confirm=yes&"+bulkContext),
		formPost("/bookmarks/bulk", "id-1=1&id-2=1&action=add_tags&tags=x&"+bulkContext),
	})
}

func TestPagesAnswer500WhenTheSessionTableFails(t *testing.T) {
	app := runningApp(t)
	admin := app.confirmed("root")
	app.breakTable("sessions")
	expectEvery500(t, app, admin, []sent{
		get("/"),
		get("/account"),
		get("/settings"),
		get("/transfer"),
		get("/users"),
		get("/users/1/password"),
		get("/users/1/delete"),
		get("/confirm"),
		formPost("/settings", "link_check_enabled=on"),
		formPost("/users", "username=bob&password=correct+horse+battery"),
		formPost("/users/1/role", "is_admin=true"),
		formPost("/logout", ""),
		formPost("/login", "username=root&password=correct+horse+battery"),
	})
}
