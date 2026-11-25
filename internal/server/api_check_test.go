package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/linkcheck"
)

// eventsOf lists the event names of a stream, in order.
func eventsOf(stream string) []string {
	var names []string
	for _, line := range strings.Split(stream, "\n") {
		if name, found := strings.CutPrefix(line, "event: "); found {
			names = append(names, name)
		}
	}
	return names
}

func checkAs(token string, ids ...int) sent {
	if ids == nil {
		ids = []int{}
	}
	return post("/api/bookmarks/check", map[string]any{"ids": ids}).as(token)
}

// addBookmarkAt saves a bookmark whose address is a path on the site.
func (a *testApp) addBookmarkAt(token, address string) int64 {
	a.t.Helper()
	return a.createBookmarkAt(token, address, "Page").ID
}

// linkStatuses is the stored link status of every bookmark, oldest first;
// a bookmark that was never checked has an empty one.
func (a *testApp) linkStatuses() []string {
	a.t.Helper()
	rows, err := a.db.Query("SELECT COALESCE(link_status, '') FROM bookmarks ORDER BY id")
	if err != nil {
		a.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var statuses []string
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			a.t.Fatal(err)
		}
		statuses = append(statuses, status)
	}
	return statuses
}

func TestCheckStreamsAResultPerLinkThenDone(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	site := linkSite()
	defer site.Close()
	app.addBookmarkAt(token, site.URL+"/ok")
	app.addBookmarkAt(token, site.URL+"/missing")
	r := app.send(checkAs(token, 1, 2))
	assertStatus(t, r, http.StatusOK)
	if got := eventsOf(r.text); strings.Join(got, ",") != "result,result,done" {
		t.Fatalf("events %v in %q", got, r.text)
	}
	if ct := r.headers.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content type %q", ct)
	}
	if r.headers.Get("X-Accel-Buffering") != "no" {
		t.Error("x-accel-buffering is not no")
	}
	mustContain(t, r, `"status":"ok"`, `"status":"broken"`, `"detail":"HTTP 404"`, "event: done\ndata: {}\n\n")
	if got := strings.Join(app.linkStatuses(), ","); got != "ok,broken" {
		t.Errorf("stored statuses %q", got)
	}
}

func TestCheckSkipsTheRestOfASiteThatAsksToWait(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	busy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer busy.Close()
	app.addBookmarkAt(token, busy.URL+"/a")
	app.addBookmarkAt(token, busy.URL+"/b")
	r := app.send(checkAs(token, 1, 2))
	if got := eventsOf(r.text); strings.Join(got, ",") != "result,skipped,done" {
		t.Fatalf("events %v in %q", got, r.text)
	}
	mustContain(t, r, `event: skipped`+"\n"+`data: {"id":2,"reason":"slow_down","host":"127.0.0.1"}`)
}

func TestCheckDoesNotSendAResultThatWasNotSaved(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	edited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		app.exec("UPDATE bookmarks SET url = 'https://elsewhere.test/'")
	}))
	defer edited.Close()
	app.addBookmarkAt(token, edited.URL+"/")
	r := app.send(checkAs(token, 1))
	if got := eventsOf(r.text); strings.Join(got, ",") != "done" {
		t.Fatalf("events %v in %q", got, r.text)
	}
	if got := strings.Join(app.linkStatuses(), ","); got != "" {
		t.Errorf("stored statuses %q", got)
	}
}

func TestCheckThatCannotSaveSendsErrorAndStops(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	site := linkSite()
	defer site.Close()
	app.addBookmarkAt(token, site.URL+"/ok")
	app.addBookmarkAt(token, site.URL+"/ok?2")
	app.exec(`CREATE TRIGGER no_link_update BEFORE UPDATE OF link_status ON bookmarks
		BEGIN SELECT RAISE(ABORT, 'read only'); END`)
	r := app.send(checkAs(token, 1, 2))
	if got := eventsOf(r.text); strings.Join(got, ",") != "error" {
		t.Fatalf("events %v in %q", got, r.text)
	}
	mustContain(t, r, "event: error\ndata: {}\n\n")
}

func TestCheckOfAnotherUsersBookmarkIsNotFoundAndFreesTheSlot(t *testing.T) {
	app := runningApp(t)
	alice := app.signIn("alice", false)
	bob := app.signIn("bob", false)
	app.addBookmarkAt(bob, "https://bob.example/")
	r := app.send(checkAs(alice, 1))
	assertStatus(t, r, http.StatusNotFound)
	if r.text != `{"error":"not found"}` {
		t.Errorf("body %q", r.text)
	}
	release, ok := app.app.activeChecks.Start(1)
	if !ok {
		t.Fatal("the refused request kept the slot")
	}
	release()
}

func TestSecondCheckOfTheSameUserIsAskedToWait(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	app.addBookmarkAt(token, "https://a.example/")
	release, ok := app.app.activeChecks.Start(1)
	if !ok {
		t.Fatal("slot not free")
	}
	defer release()
	r := app.send(checkAs(token, 1))
	assertStatus(t, r, http.StatusTooManyRequests)
	if got := r.headers.Get("Retry-After"); got != "10" {
		t.Errorf("Retry-After %q", got)
	}
	// Another user is not held back.
	other := app.signIn("other", false)
	app.addBookmarkAt(other, "https://b.example/")
	site := linkSite()
	defer site.Close()
	app.exec("UPDATE bookmarks SET url = ?1 WHERE id = 2", site.URL+"/ok")
	assertStatus(t, app.send(checkAs(other, 2)), http.StatusOK)
}

func TestCheckGuardsRunInOrder(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	app.addBookmarkAt(token, "https://a.example/")
	bad := func(who string) sent {
		request := sent{method: "POST", uri: "/api/bookmarks/check"}.withRaw("application/json", "{")
		if who != "" {
			request = request.as(who)
		}
		return request
	}
	assertStatus(t, app.send(bad("")), http.StatusUnauthorized)
	assertStatus(t, app.send(bad(token)), http.StatusBadRequest)
	wrongType := sent{method: "POST", uri: "/api/bookmarks/check"}.withRaw("text/plain", "{}").as(token)
	assertStatus(t, app.send(wrongType), http.StatusUnsupportedMediaType)
	missing := app.send(post("/api/bookmarks/check", map[string]any{}).as(token))
	assertStatus(t, missing, http.StatusUnprocessableEntity)
	mustContain(t, missing, "missing field `ids`")

	release, _ := app.app.activeChecks.Start(1)
	defer release()
	// Validation comes before the slot, the slot before the lookup.
	assertStatus(t, app.send(checkAs(token)), http.StatusBadRequest)
	assertStatus(t, app.send(checkAs(token, 999)), http.StatusTooManyRequests)
	assertStatus(t, app.send(checkAs(token, ids(201)...)), http.StatusBadRequest)
}

// openStream starts a check against a real server and returns once the headers arrived.
func openStream(t *testing.T, serverURL, token string, ctx context.Context) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL+"/api/bookmarks/check",
		strings.NewReader(`{"ids":[1]}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Cookie", "bkd_session="+token)
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestClientThatLeavesEndsTheCheckAndFreesTheSlot(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	started := make(chan struct{})
	requestEnded := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(requestEnded)
	}))
	defer slow.Close()
	app.addBookmarkAt(token, slow.URL+"/")
	server := httptest.NewServer(app.app.Handler())
	defer server.Close()
	before := runtime.NumGoroutine()

	ctx, leave := context.WithCancel(context.Background())
	defer leave()
	response := openStream(t, server.URL, token, ctx)
	assertStatusCode(t, response, http.StatusOK)
	<-started
	if _, ok := app.app.activeChecks.Start(1); ok {
		t.Fatal("the slot was free during the check")
	}
	leave()

	select {
	case <-requestEnded:
	case <-time.After(5 * time.Second):
		t.Fatal("the request to the site was not cancelled")
	}
	waitFor(t, "the slot", func() bool {
		release, ok := app.app.activeChecks.Start(1)
		if ok {
			release()
		}
		return ok
	})
	_ = response.Body.Close()
	waitFor(t, "goroutines to end", func() bool { return runtime.NumGoroutine() <= before })
}

func assertStatusCode(t *testing.T, response *http.Response, want int) {
	t.Helper()
	if response.StatusCode != want {
		t.Fatalf("status = %d, want %d", response.StatusCode, want)
	}
}

func TestQuietCheckWritesKeepAliveComments(t *testing.T) {
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
	buffer := make([]byte, len(":\n\n"))
	if _, err := io.ReadFull(response.Body, buffer); err != nil {
		t.Fatal(err)
	}
	if string(buffer) != ":\n\n" {
		t.Errorf("first bytes %q, want a keep-alive comment", buffer)
	}
}

func TestAResultIsSavedEvenWhenTheClientHasLeft(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	site := linkSite()
	defer site.Close()
	id := app.addBookmarkAt(token, site.URL+"/ok")
	gone, leave := context.WithCancel(context.Background())
	leave()

	stored, err := app.app.storeChecked(gone, auth.SessionUser{UserID: 1}, linkcheck.Checked{
		Target:  linkcheck.Target{ID: id, URL: site.URL + "/ok"},
		Verdict: linkcheck.Verdict{Status: linkcheck.StatusOK},
	})

	if err != nil || len(stored) != 1 {
		t.Fatalf("stored %v, error %v", stored, err)
	}
	if got := strings.Join(app.linkStatuses(), ","); got != "ok" {
		t.Errorf("stored statuses %q", got)
	}
}

func TestACheckStreamOutlivesTheBodyReadDeadline(t *testing.T) {
	app := runningApp(t)
	app.app.bodyTimeout = 50 * time.Millisecond
	token := app.signIn("henry", false)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(400 * time.Millisecond)
	}))
	defer slow.Close()
	app.addBookmarkAt(token, slow.URL+"/")
	server := httptest.NewServer(app.app.Handler())
	defer server.Close()

	response := openStream(t, server.URL, token, context.Background())
	stream, err := io.ReadAll(response.Body)

	if err != nil || strings.Join(eventsOf(string(stream)), ",") != "result,done" {
		t.Fatalf("events in %q, error %v", stream, err)
	}
}

func TestABodyThatArrivesTooSlowlyIsGivenUpOn(t *testing.T) {
	app := runningApp(t)
	app.app.bodyTimeout = 100 * time.Millisecond
	token := app.signIn("henry", false)
	server := httptest.NewServer(app.app.Handler())
	defer server.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	// 50 bytes are promised and one arrives.
	_, _ = io.WriteString(conn, "POST /api/bookmarks/check HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\n"+
		"Cookie: bkd_session="+token+"\r\nContent-Length: 50\r\n\r\n{")
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	answer, _ := io.ReadAll(conn)

	if len(answer) == 0 {
		t.Fatal("the server kept waiting for the body")
	}
}

func TestStoppingTheServerEndsARunningCheckStream(t *testing.T) {
	stopping, stop := context.WithCancel(context.Background())
	defer stop()
	app := newTestApp(t, Config{Stopping: stopping})
	token := app.signIn("henry", false)
	started := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer slow.Close()
	app.addBookmarkAt(token, slow.URL+"/")
	server := httptest.NewServer(app.app.Handler())
	defer server.Close()

	response := openStream(t, server.URL, token, context.Background())
	<-started
	stop()

	finished := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, response.Body)
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream outlived the stop")
	}
}

func ids(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i + 1
	}
	return out
}
