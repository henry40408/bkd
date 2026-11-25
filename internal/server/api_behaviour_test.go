package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
	assertStatus(t, app.send(viaProxy(get("/settings"), "alice")), http.StatusOK)
	assertStatus(t, app.send(viaProxy(get("/users"), "alice")), http.StatusOK)
	app.exec("UPDATE users SET disabled_at = 1 WHERE username = ?1", "alice")
	assertStatus(t, app.send(viaProxy(get("/settings"), "alice")), http.StatusSeeOther)
}

func TestReadRoutesAnswer500WhenTheirQueryFails(t *testing.T) {
	app := runningApp(t)
	admin := app.signIn("root", true)
	breakages := []struct {
		name, statement string
		uris            []string
	}{
		// Before "settings": once link_check_enabled is gone, the read of it fails first.
		{"settings user agent", "ALTER TABLE users DROP COLUMN link_check_user_agent", []string{"/settings"}},
		{"settings", "ALTER TABLE users DROP COLUMN link_check_enabled", []string{"/settings"}},
		{"user list", "ALTER TABLE users DROP COLUMN created_at", []string{"/users"}},
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
