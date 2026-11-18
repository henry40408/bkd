package server

import (
	"net/http"
	"strings"
	"testing"
)

// Every page that reads a form refuses a body of another type, and every route
// with an id in its path refuses an id that is not a number, whatever the
// route does with it afterwards.
func TestEveryFormPageRefusesABodyOfTheWrongType(t *testing.T) {
	app := runningApp(t)
	admin := app.confirmed("root")
	routes := []string{
		"/bookmarks", "/bookmarks/bulk", "/bookmarks/delete", "/bookmarks/1/edit",
		"/login", "/confirm", "/settings", "/account/password",
		"/users", "/users/2/role", "/users/2/disabled", "/users/2/password",
	}
	for _, route := range routes {
		request := sent{method: "POST", uri: route}.
			withRaw("application/json", `{}`).originated("http://bkd.test")
		r := app.send(request.as(admin))
		if r.status != http.StatusUnsupportedMediaType ||
			!strings.Contains(r.text, "application/x-www-form-urlencoded") {
			t.Errorf("POST %s: status = %d, body %q", route, r.status, r.text)
		}
	}
}

func TestEveryPageWithAnIdRefusesOneThatIsNotANumber(t *testing.T) {
	app := runningApp(t)
	admin := app.confirmed("root")
	routes := []sent{
		get("/users/abc/password"),
		get("/users/abc/delete"),
		formPost("/users/abc/disabled", "is_disabled=true"),
		formPost("/users/abc/password", "password=a+brand+new+secret"),
		formPost("/users/abc/delete", ""),
	}
	for _, route := range routes {
		r := app.send(route.as(admin))
		if r.status != http.StatusBadRequest || !strings.Contains(r.text, "Cannot parse `abc`") {
			t.Errorf("%s %s: status = %d, body %q", route.method, route.uri, r.status, r.text)
		}
	}
}

func TestEveryJSONRouteRefusesABrokenBody(t *testing.T) {
	app := runningApp(t)
	token := app.signIn("alice", false)
	for _, route := range []struct{ method, uri string }{
		{"POST", "/api/login"},
		{"POST", "/api/setup"},
		{"POST", "/api/account/confirm"},
		{"PUT", "/api/account/password"},
		{"PUT", "/api/settings"},
	} {
		request := sent{method: route.method, uri: route.uri}.withRaw("application/json", "{")
		r := app.send(request.as(token))
		if r.status != http.StatusBadRequest {
			t.Errorf("%s %s: status = %d, want 400; body %q", route.method, route.uri, r.status, r.text)
		}
	}
}
