package server

import (
	"net/http"
	"testing"
)

// A request the handler cannot read is refused before it touches an account,
// and the refusal does not depend on which user route it is.
func TestUserRoutesRefuseARequestTheyCannotRead(t *testing.T) {
	app := runningApp(t)
	admin := app.confirmed("root")
	cases := []struct {
		name string
		send sent
		want int
	}{
		{"create, broken JSON", post("/api/users", nil).withRaw("application/json", "{"), http.StatusBadRequest},
		{"create, no username", post("/api/users", map[string]any{"password": testPassword}), http.StatusUnprocessableEntity},
		{"role, id is not a number", put("/api/users/abc/role", map[string]any{"is_admin": true}), http.StatusBadRequest},
		{"role, broken JSON", put("/api/users/2/role", nil).withRaw("application/json", "{"), http.StatusBadRequest},
		{"disabled, id is not a number", put("/api/users/abc/disabled", map[string]any{"is_disabled": true}), http.StatusBadRequest},
		{"disabled, broken JSON", put("/api/users/2/disabled", nil).withRaw("application/json", "{"), http.StatusBadRequest},
		{"disabled, no flag", put("/api/users/2/disabled", map[string]any{}), http.StatusUnprocessableEntity},
		{"password, id is not a number", put("/api/users/abc/password", map[string]any{"password": "x"}), http.StatusBadRequest},
		{"password, broken JSON", put("/api/users/2/password", nil).withRaw("application/json", "{"), http.StatusBadRequest},
		{"password, none given", put("/api/users/2/password", map[string]any{}), http.StatusUnprocessableEntity},
		{"delete, id is not a number", del("/api/users/abc"), http.StatusBadRequest},
	}
	for _, c := range cases {
		r := app.send(c.send.as(admin))
		if r.status != c.want {
			t.Errorf("%s: status = %d, want %d; body %q", c.name, r.status, c.want, r.text)
		}
	}
}
