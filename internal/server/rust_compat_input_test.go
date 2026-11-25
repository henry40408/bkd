package server

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/henry40408/bkd/internal/views"
)

// addUserWithPassword stores a user whose password is not testPassword.
func (a *testApp) addUserWithPassword(username, password string) {
	a.t.Helper()
	hash, err := a.hasher.Hash(a.t.Context(), password)
	if err != nil {
		a.t.Fatal(err)
	}
	a.exec("INSERT INTO users (username, password_hash, is_admin, created_at) VALUES (?1, ?2, 0, 0)", username, hash)
}

// A broken %-escape stays as typed, and a "+" in such a value is still a space,
// like the form parser axum uses; Go's url.ParseQuery would drop the pair.
func TestAFormValueWithABrokenEscapeIsTakenAsTyped(t *testing.T) {
	app := runningApp(t)
	app.addUserWithPassword("alice", "ab%zzc")
	app.addUserWithPassword("bob", "a b%zz")

	cases := []struct {
		name   string
		fields string
		status int
	}{
		{"broken escape in the middle", "username=alice&password=ab%zzc", http.StatusSeeOther},
		{"another value is still refused", "username=alice&password=ab%zzd", http.StatusUnauthorized},
		{"a plus beside a broken escape is a space", "username=bob&password=a+b%zz", http.StatusSeeOther},
		{"a truncated escape at the end", "username=alice&password=ab%zzc%4", http.StatusUnauthorized},
	}
	for index, c := range cases {
		// A distinct address each, so the login rate limit does not interfere.
		r := app.send(formPost("/login", c.fields).from("192.0.2." + strconv.Itoa(index+2)))
		if r.status != c.status {
			t.Errorf("%s: status = %d, want %d; body %q", c.name, r.status, c.status, r.text)
		}
	}
}

// "%2B" is a literal plus, which Rust's integer parser accepts in front of the
// digits; the plus sign itself is a space in a query string.
func TestAQueryNumberMayStartWithAPlusSign(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	for index := range views.PageSize + 1 {
		app.createBookmark(token, fmt.Sprintf("bookmark %02d", index))
	}

	second := app.send(get("/?page=%2B2").as(token))
	assertStatus(t, second, http.StatusOK)
	wantSecond := app.send(get("/?page=2").as(token))
	if second.text != wantSecond.text {
		t.Error("page=+2 does not show what page=2 shows")
	}
	if first := app.send(get("/?page=1").as(token)); second.text == first.text {
		t.Error("page=2 shows the first page")
	}
}

func TestAFormNumberMayStartWithAPlusSign(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	created := app.createBookmark(token, "doomed")

	r := app.send(formPost("/bookmarks/delete", fmt.Sprintf("id=%%2B%d", created.ID)).as(token))

	assertStatus(t, r, http.StatusSeeOther)
	if left := app.bookmarkCount(1); left != 0 {
		t.Errorf("%d bookmarks left after the delete", left)
	}
}

// A form body is read up to maxBodyBytes: that many bytes are accepted, one more
// is a 413.
func TestAFormBodyIsRefusedOnlyBeyondTheLimit(t *testing.T) {
	app := runningApp(t)
	prefix := "username=alice&password="
	atLimit := prefix + strings.Repeat("a", maxBodyBytes-len(prefix))

	r := app.send(formPost("/login", atLimit))
	if r.status == http.StatusRequestEntityTooLarge {
		t.Fatalf("a body of exactly %d bytes was refused: %q", maxBodyBytes, r.text)
	}

	r = app.send(formPost("/login", atLimit+"a"))
	assertStatus(t, r, http.StatusRequestEntityTooLarge)
	if !strings.Contains(r.text, "length limit exceeded") {
		t.Errorf("body = %q", r.text)
	}
}

// A JSON body may carry parameters and any application/*+json type; others are
// a 415. An accepted body reaches the handler, which refuses the empty check.
func TestJSONContentTypesAreMatchedByMediaType(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	const body = `{"ids":[]}`
	cases := []struct {
		contentType string
		status      int
	}{
		{"application/json", http.StatusBadRequest},
		{"application/json; charset=utf-8", http.StatusBadRequest},
		{"Application/JSON", http.StatusBadRequest},
		{"application/problem+json", http.StatusBadRequest},
		{"application/vnd.api+json; charset=utf-8", http.StatusBadRequest},
		{"text/json", http.StatusUnsupportedMediaType},
		{"application/jsonx", http.StatusUnsupportedMediaType},
		{"application/json+text", http.StatusUnsupportedMediaType},
		{"text/plain+json", http.StatusUnsupportedMediaType},
		{"", http.StatusUnsupportedMediaType},
	}
	for _, c := range cases {
		r := app.send(sent{method: "POST", uri: "/api/bookmarks/check"}.withRaw(c.contentType, body).as(token))
		if r.status != c.status {
			t.Errorf("Content-Type %q: status = %d, want %d; body %q", c.contentType, r.status, c.status, r.text)
		}
	}
}

// Timestamps are Unix seconds as integers, so a millisecond or fractional value
// fails here.
func TestExportedTimestampsAreWholeUnixSeconds(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	app.createBookmark(token, "t")
	exported := app.send(get("/api/export?format=json").as(token))
	assertStatus(t, exported, http.StatusOK)

	found := regexp.MustCompile(`"created_at": (\d+)[,\n]`).FindAllStringSubmatch(exported.text, -1)
	if len(found) != 1 {
		t.Fatalf("found %d timestamps in %s, want 1", len(found), exported.text)
	}
	seconds, err := strconv.ParseInt(found[0][1], 10, 64)
	if err != nil || len(found[0][1]) != 10 {
		t.Errorf("created_at = %q, want 10 digits", found[0][1])
	}
	if drift := time.Now().Unix() - seconds; drift < 0 || drift > 5 {
		t.Errorf("created_at = %d is %d seconds from now", seconds, drift)
	}
}
