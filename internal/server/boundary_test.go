package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/henry40408/bkd/internal/dbtest"
	"github.com/henry40408/bkd/internal/linkcheck"
)

func TestSameSitePathStopsAtTheFirstUnsafeCharacter(t *testing.T) {
	cases := map[string]bool{
		"/":              true,
		"/a b":           true,
		"/a~b":           true,
		"/a b":           true,
		"/a\x1fb":        false,
		"/a\x7fb":        false,
		"/a\u009fb":      false,
		"/a\\b":          false,
		"//host":         false,
		"/a//b":          true,
		"":               false,
		"https://a.test": false,
	}
	for value, want := range cases {
		if got := sameSitePath(value); got != want {
			t.Errorf("sameSitePath(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestRetryAfterRoundsUpToWholeSeconds(t *testing.T) {
	cases := map[time.Duration]int64{
		0:                       0,
		time.Nanosecond:         1,
		999 * time.Millisecond:  1,
		time.Second:             1,
		1001 * time.Millisecond: 2,
		1500 * time.Millisecond: 2,
		2 * time.Second:         2,
	}
	for wait, want := range cases {
		if got := (&apiError{retryAfter: wait}).retryAfterSeconds(); got != want {
			t.Errorf("%v: got %d, want %d", wait, got, want)
		}
	}
}

// loggedApp is an app whose log is readable.
func loggedApp(t *testing.T) (*testApp, *bytes.Buffer) {
	t.Helper()
	var log bytes.Buffer
	app := newTestApp(t, Config{Logger: slog.New(slog.NewTextHandler(&log, nil))})
	return app, &log
}

func TestOnlyInternalFaultsAreLogged(t *testing.T) {
	app, log := loggedApp(t)
	app.app.writeAPIError(httptest.NewRecorder(), unauthorized())
	app.app.writePageError(httptest.NewRecorder(), &pageError{status: http.StatusNotFound, message: "gone"})
	if log.Len() != 0 {
		t.Fatalf("a client error was logged as a fault: %s", log)
	}

	app.app.writeAPIError(httptest.NewRecorder(), internal(errors.New("api boom")))
	app.app.writePageError(httptest.NewRecorder(), &pageError{
		status: http.StatusInternalServerError, message: "internal error", cause: errors.New("page boom"),
	})
	for _, cause := range []string{"api boom", "page boom"} {
		if !strings.Contains(log.String(), cause) {
			t.Errorf("the cause %q was not logged: %s", cause, log)
		}
	}
}

func TestAnAppWithoutAHasherOrLoggerStillWorks(t *testing.T) {
	app := New(Config{
		DB:          dbtest.Open(t),
		LinkChecker: linkcheck.New(linkcheck.Config{}),
	})
	request := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"nobody","password":"x"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	app.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d %s", recorder.Code, recorder.Body)
	}
}

func TestAQuietCheckKeepsWritingKeepAliveComments(t *testing.T) {
	app := runningApp(t)
	app.app.keepAlive = 10 * time.Millisecond
	token := app.signIn("henry", false)
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	defer close(release)
	app.addBookmarkAt(token, slow.URL+"/")
	server := httptest.NewServer(app.app.Handler())
	defer server.Close()
	ctx, leave := context.WithCancel(context.Background())
	defer leave()

	response := openStream(t, server.URL, token, ctx)

	buffer := make([]byte, 3*len(":\n\n"))
	if _, err := io.ReadFull(response.Body, buffer); err != nil {
		t.Fatal(err)
	}
	if string(buffer) != strings.Repeat(":\n\n", 3) {
		t.Errorf("got %q, want three keep-alive comments", buffer)
	}
}
