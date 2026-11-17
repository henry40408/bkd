package server

import (
	"fmt"
	"net/http"
	"testing"
)

func (a *testApp) confirmed(username string) string {
	a.t.Helper()
	token := a.signIn(username, true)
	r := a.send(post("/api/account/confirm", map[string]any{"password": testPassword}).as(token))
	assertStatus(a.t, r, http.StatusNoContent)
	return token
}

func TestUserRoutesNeedAnAdmin(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	for _, request := range []sent{
		get("/api/users"),
		post("/api/users", map[string]any{"username": "x", "password": testPassword}),
		del("/api/users/1"),
	} {
		assertStatus(t, app.send(request), http.StatusUnauthorized)
		assertStatus(t, app.send(request.as(token)), http.StatusForbidden)
	}
}

func TestAnAdminCreatesListsAndDeletesUsers(t *testing.T) {
	app := runningApp(t)
	admin := app.confirmed("root")
	created := app.send(post("/api/users", map[string]any{"username": "Mei", "password": testPassword}).as(admin))
	assertStatus(t, created, http.StatusCreated)
	if created.json()["username"] != "mei" || created.json()["is_admin"] != false {
		t.Fatalf("created = %s", created.text)
	}
	list := app.send(get("/api/users").as(admin)).json()["users"].([]any)
	if len(list) != 2 {
		t.Fatalf("users = %v", list)
	}
	dup := app.send(post("/api/users", map[string]any{"username": "mei", "password": testPassword}).as(admin))
	assertStatus(t, dup, http.StatusConflict)
	uri := fmt.Sprintf("/api/users/%v", created.json()["id"])
	assertStatus(t, app.send(del(uri).as(admin)), http.StatusNoContent)
	assertStatus(t, app.send(del(uri).as(admin)), http.StatusNotFound)
}

func TestCreatingAUserNeedsRecentPasswordConfirmation(t *testing.T) {
	app := runningApp(t)
	admin := app.signIn("root", true)
	// A fresh login counts as confirmed; let the confirmation lapse.
	if _, err := app.db.Exec("UPDATE sessions SET confirmed_at = 0"); err != nil {
		t.Fatal(err)
	}
	r := app.send(post("/api/users", map[string]any{"username": "mei", "password": testPassword}).as(admin))
	assertStatus(t, r, http.StatusForbidden)
	if r.json()["code"] != "reauth_required" {
		t.Fatalf("body = %s", r.text)
	}
}

func TestTheLastAdminCannotBeRemoved(t *testing.T) {
	app := runningApp(t)
	admin := app.confirmed("root")
	id := app.send(get("/api/users").as(admin)).json()["users"].([]any)[0].(map[string]any)["id"]
	base := fmt.Sprintf("/api/users/%v", id)
	assertStatus(t, app.send(put(base+"/role", map[string]any{"is_admin": false}).as(admin)), http.StatusConflict)
	assertStatus(t, app.send(put(base+"/disabled", map[string]any{"is_disabled": true}).as(admin)), http.StatusConflict)
	assertStatus(t, app.send(del(base).as(admin)), http.StatusConflict)
}

func TestAnAdminCanDisableAndResetAUser(t *testing.T) {
	app := runningApp(t)
	admin := app.confirmed("root")
	app.addUser("mei", false)
	token := sessionToken(t, app.login("mei", testPassword))
	var id int64
	if err := app.db.QueryRow("SELECT id FROM users WHERE username = 'mei'").Scan(&id); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/users/%d", id)
	assertStatus(t, app.send(put(base+"/disabled", map[string]any{"is_disabled": true}).as(admin)), http.StatusNoContent)
	assertStatus(t, app.send(get("/api/bookmarks").as(token)), http.StatusUnauthorized)
	assertStatus(t, app.send(put(base+"/disabled", map[string]any{"is_disabled": false}).as(admin)), http.StatusNoContent)
	assertStatus(t, app.send(put(base+"/password", map[string]any{"password": "a brand new password"}).as(admin)), http.StatusNoContent)
	assertStatus(t, app.login("mei", "a brand new password"), http.StatusOK)
	assertStatus(t, app.send(put(base+"/role", map[string]any{"is_admin": true}).as(admin)), http.StatusNoContent)
	assertStatus(t, app.send(put("/api/users/99999/role", map[string]any{"is_admin": true}).as(admin)), http.StatusNotFound)
	assertStatus(t, app.send(put(base+"/role", map[string]any{}).as(admin)), http.StatusUnprocessableEntity)
}
