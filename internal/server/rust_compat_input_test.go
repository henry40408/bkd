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

	limited := app.send(get("/api/bookmarks?limit=%2B5").as(token))
	assertStatus(t, limited, http.StatusOK)
	if got := len(titlesOf(t, limited)); got != 5 {
		t.Errorf("limit=+5 listed %d bookmarks, want 5", got)
	}

	skipped := app.send(get("/api/bookmarks?limit=3&offset=%2B2").as(token))
	assertStatus(t, skipped, http.StatusOK)
	plain := app.send(get("/api/bookmarks?limit=3&offset=2").as(token))
	if skipped.text != plain.text {
		t.Errorf("offset=+2 gave %q, want what offset=2 gave, %q", skipped.text, plain.text)
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

	r := app.send(formPost("/bookmarks/delete", fmt.Sprintf("id=%%2B%v", created["id"])).as(token))

	assertStatus(t, r, http.StatusSeeOther)
	if listed := titlesOf(t, app.send(get("/api/bookmarks").as(token))); len(listed) != 0 {
		t.Errorf("bookmarks left after the delete: %v", listed)
	}
}

// Every required JSON field that is absent, or null, is a 422 naming it.
func TestARequiredJSONFieldThatIsMissingOrNullIsRefused(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	bookmark := app.createBookmark(token, "kept")
	bookmarkURI := fmt.Sprintf("/api/bookmarks/%v", bookmark["id"])
	cases := []struct {
		name  string
		send  sent
		field string
	}{
		{"setup without token", post("/api/setup", map[string]any{"username": "a", "password": "b"}), "token"},
		{"setup with a null token", post("/api/setup", map[string]any{"token": nil, "username": "a", "password": "b"}), "token"},
		{"setup without password", post("/api/setup", map[string]any{"token": "t", "username": "a"}), "password"},
		{"setup with a null password", post("/api/setup", map[string]any{"token": "t", "username": "a", "password": nil}), "password"},
		{"change password without current", put("/api/account/password", map[string]any{"new": "x"}), "current"},
		{"change password without new", put("/api/account/password", map[string]any{"current": "x"}), "new"},
		{"change password with a null new", put("/api/account/password", map[string]any{"current": "x", "new": nil}), "new"},
		{"confirm without password", post("/api/account/confirm", map[string]any{}), "password"},
		{"confirm with a null password", post("/api/account/confirm", map[string]any{"password": nil}), "password"},
		{"create without url", post("/api/bookmarks", map[string]any{"title": "t"}), "url"},
		{"create without title", post("/api/bookmarks", map[string]any{"url": "https://a.b"}), "title"},
		{"create with a null url", post("/api/bookmarks", map[string]any{"url": nil, "title": "t"}), "url"},
		{"create with a null title", post("/api/bookmarks", map[string]any{"url": "https://a.b", "title": nil}), "title"},
		{"update without url", put(bookmarkURI, map[string]any{"title": "t"}), "url"},
		{"update without title", put(bookmarkURI, map[string]any{"url": "https://a.b"}), "title"},
		{"update with a null url", put(bookmarkURI, map[string]any{"url": nil, "title": "t"}), "url"},
		{"update with a null title", put(bookmarkURI, map[string]any{"url": "https://a.b", "title": nil}), "title"},
	}
	for _, c := range cases {
		r := app.send(c.send.as(token))
		want := "Failed to deserialize the JSON body into the target type: missing field `" + c.field + "`"
		if r.status != http.StatusUnprocessableEntity || r.text != want {
			t.Errorf("%s: status = %d, body %q; want 422 %q", c.name, r.status, r.text, want)
		}
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
// a 415. An accepted body reaches the handler, which refuses the login as 401.
func TestJSONContentTypesAreMatchedByMediaType(t *testing.T) {
	app := runningApp(t)
	const body = `{"username":"nobody","password":"wrong"}`
	cases := []struct {
		contentType string
		status      int
	}{
		{"application/json", http.StatusUnauthorized},
		{"application/json; charset=utf-8", http.StatusUnauthorized},
		{"Application/JSON", http.StatusUnauthorized},
		{"application/problem+json", http.StatusUnauthorized},
		{"application/vnd.api+json; charset=utf-8", http.StatusUnauthorized},
		{"text/json", http.StatusUnsupportedMediaType},
		{"application/jsonx", http.StatusUnsupportedMediaType},
		{"application/json+text", http.StatusUnsupportedMediaType},
		{"text/plain+json", http.StatusUnsupportedMediaType},
		{"", http.StatusUnsupportedMediaType},
	}
	for _, c := range cases {
		r := app.send(sent{method: "POST", uri: "/api/login"}.withRaw(c.contentType, body))
		if r.status != c.status {
			t.Errorf("Content-Type %q: status = %d, want %d; body %q", c.contentType, r.status, c.status, r.text)
		}
	}
}

// Timestamps are Unix seconds as integers, so a millisecond or fractional value
// fails here.
func TestTimestampsAreWholeUnixSeconds(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	pattern := regexp.MustCompile(`"(created_at|updated_at)":(\d+)[,}]`)
	check := func(label string, r reply) {
		t.Helper()
		found := pattern.FindAllStringSubmatch(r.text, -1)
		if len(found) != 2 {
			t.Fatalf("%s: found %d timestamps in %s, want 2", label, len(found), r.text)
		}
		for _, match := range found {
			seconds, err := strconv.ParseInt(match[2], 10, 64)
			if err != nil || len(match[2]) != 10 {
				t.Errorf("%s: %s = %q, want 10 digits", label, match[1], match[2])
			}
			if drift := time.Now().Unix() - seconds; drift < 0 || drift > 5 {
				t.Errorf("%s: %s = %d is %d seconds from now", label, match[1], seconds, drift)
			}
		}
	}

	created := app.send(post("/api/bookmarks", bookmarkBody("https://a.b", "t")).as(token))
	assertStatus(t, created, http.StatusCreated)
	check("created", created)

	id := created.json()["id"]
	changed := app.send(put(fmt.Sprintf("/api/bookmarks/%v", id), bookmarkBody("https://a.b", "u")).as(token))
	assertStatus(t, changed, http.StatusOK)
	check("changed", changed)
}
