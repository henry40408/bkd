package server

import (
	"net/http"
	"strings"
	"testing"
)

// A form a page cannot read is refused with the wording the Rust server used,
// before any account or bookmark is touched.
func TestPageFormsRefuseWhatTheyCannotRead(t *testing.T) {
	app := runningApp(t)
	admin := app.confirmed("root")
	wrongType := sent{method: "POST", uri: "/users/1/role"}.
		withRaw("application/json", `{"is_admin":true}`).originated("http://bkd.test")
	cases := []struct {
		name   string
		send   sent
		status int
		says   string
	}{
		{"a form sent as JSON", wrongType, http.StatusUnsupportedMediaType,
			"Form requests must have `Content-Type: application/x-www-form-urlencoded`"},
		{"a user id that is not a number", formPost("/users/abc/role", "is_admin=true"), http.StatusBadRequest,
			"Invalid URL: Cannot parse `abc` to a `i64`"},
		{"a role flag that is missing", formPost("/users/1/role", ""), http.StatusUnprocessableEntity,
			"missing field `is_admin`"},
		{"a disabled flag that is missing", formPost("/users/1/disabled", ""), http.StatusUnprocessableEntity,
			"missing field `is_disabled`"},
		{"a password that is missing", formPost("/users/1/password", ""), http.StatusUnprocessableEntity,
			"missing field `password`"},
		{"a user name that is missing", formPost("/users", "password=x"), http.StatusUnprocessableEntity,
			"missing field `username`"},
		{"a bookmark id that is not a number", formPost("/bookmarks/delete", "id=abc"), http.StatusUnprocessableEntity,
			"Failed to deserialize form body: id:"},
	}
	for _, c := range cases {
		r := app.send(c.send.as(admin))
		if r.status != c.status || !strings.Contains(r.text, c.says) {
			t.Errorf("%s: status = %d, want %d; body %q, want it to say %q", c.name, r.status, c.status, r.text, c.says)
		}
	}
}

func TestImportRefusesAnUploadItCannotRead(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("alice", false)
	upload := func(contentType, body string) reply {
		request := sent{method: "POST", uri: "/transfer/import"}.
			withRaw(contentType, body).originated("http://bkd.test").as(token)
		return app.send(request)
	}
	cases := []struct {
		name        string
		contentType string
		body        string
		status      int
		says        string
	}{
		{"a body that is not multipart", "text/plain", "x", http.StatusUnsupportedMediaType,
			"Multipart requests must have `Content-Type: multipart/form-data`"},
		{"a multipart body without a boundary", "multipart/form-data", "x", http.StatusBadRequest,
			"Invalid `boundary` for `multipart/form-data` request"},
		{"a multipart body cut short", "multipart/form-data; boundary=b", "--b\r\nContent-Disposition: form-data; name=\"file\"\r\n\r\nabc", http.StatusBadRequest,
			""},
	}
	for _, c := range cases {
		r := upload(c.contentType, c.body)
		if r.status != c.status || !strings.Contains(r.text, c.says) {
			t.Errorf("%s: status = %d, want %d; body %q, want it to say %q", c.name, r.status, c.status, r.text, c.says)
		}
	}
}
