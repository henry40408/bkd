package server

import (
	"net/http"
	"strings"
	"testing"
)

// Every bookmark route refuses a request it cannot read before it touches a
// bookmark, with one status per kind of fault.
func TestBookmarkRoutesRefuseARequestTheyCannotRead(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("alice", false)
	broken := func(base sent) sent { return base.withRaw("application/json", "{") }
	asJSON := func(method, uri string) sent {
		return sent{method: method, uri: uri}.withRaw("application/json", `{"x":1}`).originated("http://bkd.test")
	}
	cases := []struct {
		name   string
		send   sent
		status int
		says   string
	}{
		{"create, broken JSON", broken(sent{method: "POST", uri: "/api/bookmarks"}), http.StatusBadRequest, ""},
		{"update, id is not a number", put("/api/bookmarks/abc", bookmarkBody("https://example.com", "x")), http.StatusBadRequest, "Cannot parse `abc`"},
		{"update, broken JSON", broken(sent{method: "PUT", uri: "/api/bookmarks/1"}), http.StatusBadRequest, ""},
		{"delete, id is not a number", del("/api/bookmarks/abc"), http.StatusBadRequest, "Cannot parse `abc`"},
		{"bulk, broken JSON", broken(sent{method: "POST", uri: "/api/bookmarks/bulk"}), http.StatusBadRequest, ""},
		{"check, broken JSON", broken(sent{method: "POST", uri: "/api/bookmarks/check"}), http.StatusBadRequest, ""},
		{"list, page size is not a number", get("/api/bookmarks?limit=abc"), http.StatusBadRequest, ""},
		{"a page form sent as JSON", asJSON("POST", "/bookmarks"), http.StatusUnsupportedMediaType, "application/x-www-form-urlencoded"},
		{"edit form sent as JSON", asJSON("POST", "/bookmarks/1/edit"), http.StatusUnsupportedMediaType, "application/x-www-form-urlencoded"},
		{"delete form sent as JSON", asJSON("POST", "/bookmarks/delete"), http.StatusUnsupportedMediaType, "application/x-www-form-urlencoded"},
		{"bulk form sent as JSON", asJSON("POST", "/bookmarks/bulk"), http.StatusUnsupportedMediaType, "application/x-www-form-urlencoded"},
		{"edit page, id is not a number", get("/bookmarks/abc/edit"), http.StatusBadRequest, "Cannot parse `abc`"},
		{"edit form, id is not a number", formPost("/bookmarks/abc/edit", "url=x&title=x&note=&tags="), http.StatusBadRequest, "Cannot parse `abc`"},
		{"delete page, id is not a number", get("/bookmarks/abc/delete"), http.StatusBadRequest, "Cannot parse `abc`"},
	}
	for _, c := range cases {
		r := app.send(c.send.as(token))
		if r.status != c.status || !strings.Contains(r.text, c.says) {
			t.Errorf("%s: status = %d, want %d; body %q, want it to say %q", c.name, r.status, c.status, r.text, c.says)
		}
	}
}
