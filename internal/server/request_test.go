package server

import (
	"net/http"
	"testing"
)

func TestIntegerQueryErrorsUseRustWording(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	cases := map[string]string{
		"":            "cannot parse integer from empty string",
		"x":           "invalid digit found in string",
		"99999999999": "number too large to fit in target type",
		"-1":          "invalid digit found in string",
	}
	for value, wording := range cases {
		r := app.send(get("/api/bookmarks?limit=" + value).as(token))
		assertStatus(t, r, http.StatusBadRequest)
		want := "Failed to deserialize query string: limit: " + wording
		if r.text != want {
			t.Errorf("limit=%q: body = %q, want %q", value, r.text, want)
		}
	}
}

func TestUncleanPathsAreRedirectedToTheCleanPath(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	for _, path := range []string{"/api//state", "/api/./state", "/api/x/../state"} {
		r := app.send(get(path).as(token))
		assertStatus(t, r, http.StatusTemporaryRedirect)
		if got := r.headers.Get("Location"); got != "/api/state" {
			t.Errorf("%s: Location = %q", path, got)
		}
	}
	assertStatus(t, app.send(get("/api/state").as(token)), http.StatusOK)
}
