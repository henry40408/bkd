package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestAPIReadEndpointsReportWhatWasStored(t *testing.T) {
	app := runningApp(t)
	admin := app.signIn("olga", true)
	app.addUser("plain", false)
	plain := sessionToken(t, app.login("plain", testPassword))

	t.Run("state names the signed in user", func(t *testing.T) {
		state := app.send(get("/api/state").as(admin)).json()
		user, _ := state["user"].(map[string]any)
		if state["setup_required"] != false || user["username"] != "olga" || user["is_admin"] != true {
			t.Fatalf("state = %v", state)
		}
	})
	t.Run("sessions has one entry per login", func(t *testing.T) {
		app.login("olga", testPassword)
		r := app.send(get("/api/account/sessions").as(admin))
		assertStatus(t, r, http.StatusOK)
		if got := len(r.jsonList()); got != 2 {
			t.Fatalf("sessions = %d, want 2: %s", got, r.text)
		}
	})
	t.Run("an empty list is an array, not null", func(t *testing.T) {
		r := app.send(get("/api/bookmarks").as(admin))
		if r.text != `{"bookmarks":[]}` {
			t.Fatalf("body = %s", r.text)
		}
		if r.headers.Get("Content-Type") != "application/json" {
			t.Errorf("content type = %q", r.headers.Get("Content-Type"))
		}
		if got := app.send(get("/api/tags").as(admin)).text; got != `{"tags":[],"links":[]}` {
			t.Fatalf("empty tags = %s", got)
		}
	})
	for _, title := range []string{"a", "b", "c"} {
		app.createBookmark(admin, title, "go")
	}
	app.createBookmark(admin, "d", "web")
	t.Run("tags count the bookmarks of each tag", func(t *testing.T) {
		got := app.send(get("/api/tags").as(admin)).json()["tags"]
		want := []any{map[string]any{"tag": "go", "count": float64(3)}, map[string]any{"tag": "web", "count": float64(1)}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("tags = %v, want %v", got, want)
		}
	})
	t.Run("the list pages newest first and rejects a bad limit", func(t *testing.T) {
		got := titlesOf(t, app.send(get("/api/bookmarks?limit=2&offset=1").as(admin)))
		if want := []string{"c", "b"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("titles = %v, want %v", got, want)
		}
		for _, uri := range []string{"/api/bookmarks?limit=x", "/api/bookmarks?offset=-1"} {
			assertStatus(t, app.send(get(uri).as(admin)), http.StatusBadRequest)
		}
	})
	t.Run("the link check setting is off until saved", func(t *testing.T) {
		if app.send(get("/api/settings").as(admin)).json()["link_check_enabled"] != false {
			t.Error("default should be off")
		}
		assertStatus(t, app.send(put("/api/settings", map[string]any{"link_check_enabled": true}).as(admin)), http.StatusOK)
		if app.send(get("/api/settings").as(admin)).json()["link_check_enabled"] != true {
			t.Error("setting not saved")
		}
	})
	t.Run("the user list is for admins only", func(t *testing.T) {
		users := app.send(get("/api/users").as(admin)).json()["users"].([]any)
		if len(users) != 2 {
			t.Fatalf("users = %v", users)
		}
		assertStatus(t, app.send(get("/api/users").as(plain)), http.StatusForbidden)
	})
}

func TestAwaitingSetupStateAsksForSetup(t *testing.T) {
	app := awaitingSetupApp(t)
	if app.send(get("/api/state")).json()["setup_required"] != true {
		t.Error("setup should be required")
	}
}

func TestPeerAddrHandlesMappedAndBrokenAddresses(t *testing.T) {
	tests := map[string]string{
		"[::ffff:192.0.2.7]:80": "192.0.2.7",
		"192.0.2.8:80":          "192.0.2.8",
		"garbage":               "invalid IP",
	}
	for remote, want := range tests {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.RemoteAddr = remote
		if got := peerAddr(request).String(); got != want {
			t.Errorf("peerAddr(%q) = %q, want %q", remote, got, want)
		}
	}
}

func TestWriteJSONAnswers500ForAValueThatCannotBeEncoded(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeJSON(recorder, http.StatusOK, map[string]any{"bad": make(chan int)})
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestEventStreamReportsAnUnencodableEvent(t *testing.T) {
	stream, err := startSSE(httptest.NewRecorder())
	if err != nil {
		t.Fatal(err)
	}
	if stream.event("progress", make(chan int)) == nil {
		t.Error("event accepted a value JSON cannot hold")
	}
}

type brokenWriter struct{ http.ResponseWriter }

func (w brokenWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("client gone") }

func TestEventStreamReportsAClosedClient(t *testing.T) {
	stream, err := startSSE(brokenWriter{httptest.NewRecorder()})
	if err != nil {
		t.Fatal(err)
	}
	if stream.event("progress", 1) == nil || stream.keepAlive() == nil {
		t.Error("a failed write was not reported")
	}
}

func TestForwardedUserIsSignedInUntilDisabled(t *testing.T) {
	app := newTestApp(t, forwardAuthConfig(t))
	assertStatus(t, app.send(viaProxy(get("/api/settings"), "alice")), http.StatusOK)
	assertStatus(t, app.send(viaProxy(get("/users"), "alice")), http.StatusOK)
	app.exec("UPDATE users SET disabled_at = 1 WHERE username = ?1", "alice")
	if app.send(viaProxy(get("/api/state"), "alice")).json()["user"] != nil {
		t.Error("a disabled account was signed in")
	}
}

func TestReadRoutesAnswer500WhenTheirQueryFails(t *testing.T) {
	app := runningApp(t)
	admin := app.signIn("root", true)
	breakages := []struct {
		name, statement string
		uris            []string
	}{
		{"link counts", "ALTER TABLE bookmarks DROP COLUMN link_status", []string{"/api/tags"}},
		{"settings", "ALTER TABLE users DROP COLUMN link_check_enabled", []string{"/api/settings", "/settings"}},
		{"user list", "ALTER TABLE users DROP COLUMN created_at", []string{"/api/users", "/users"}},
	}
	for _, breakage := range breakages {
		t.Run(breakage.name, func(t *testing.T) {
			app.exec(breakage.statement)
			for _, uri := range breakage.uris {
				assertStatus(t, app.send(get(uri).as(admin)), http.StatusInternalServerError)
			}
		})
	}
}
