package server

import "testing"

func TestHealthzOKWithoutSession(t *testing.T) {
	app := runningApp(t)
	got := app.send(get("/healthz"))
	if got.status != 200 || got.text != "ok\n" {
		t.Fatalf("got %d %q", got.status, got.text)
	}
	if got.headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", got.headers.Get("Cache-Control"))
	}
}

func TestHealthzUnavailableWhenDatabaseClosed(t *testing.T) {
	app := runningApp(t)
	if err := app.db.Close(); err != nil {
		t.Fatal(err)
	}
	got := app.send(get("/healthz"))
	if got.status != 503 || got.text != "unavailable\n" {
		t.Fatalf("got %d %q", got.status, got.text)
	}
}

func TestHealthzRejectsPost(t *testing.T) {
	got := runningApp(t).send(post("/healthz", nil))
	if got.status != 405 {
		t.Fatalf("status = %d", got.status)
	}
}
