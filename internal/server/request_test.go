package server

import (
	"net/http"
	"testing"
)

func TestIntegerQueryErrorsNameTheParameter(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	cases := map[string]string{
		"":            "invalid digit found in string",
		"x":           "invalid digit found in string",
		"99999999999": "number out of range",
		"-1":          "invalid digit found in string",
	}
	for value, wording := range cases {
		r := app.send(get("/?page=" + value).as(token))
		assertStatus(t, r, http.StatusBadRequest)
		mustContain(t, r, "Failed to deserialize query string: page: "+wording)
	}
}

func TestUncleanPathsAreRedirectedToTheCleanPath(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("henry", false)
	for _, path := range []string{"//settings", "/./settings", "/x/../settings"} {
		r := app.send(get(path).as(token))
		assertStatus(t, r, http.StatusTemporaryRedirect)
		if got := r.headers.Get("Location"); got != "/settings" {
			t.Errorf("%s: Location = %q", path, got)
		}
	}
	assertStatus(t, app.send(get("/settings").as(token)), http.StatusOK)
}
