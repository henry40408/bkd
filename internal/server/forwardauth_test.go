package server

import (
	"net/http"
	"testing"

	"github.com/henry40408/bkd/internal/auth"
)

const proxyPeer = "127.0.0.1"

func forwardAuthConfig(t *testing.T) Config {
	t.Helper()
	forward, err := auth.NewForwardAuth(auth.ForwardAuthConfig{
		TrustedProxies: []string{proxyPeer},
		UserHeader:     "Remote-User",
		GroupsHeader:   "Remote-Groups",
		AdminGroup:     "bkd-admins",
	})
	if err != nil {
		t.Fatal(err)
	}
	return Config{ForwardAuth: forward}
}

func viaProxy(request sent, user string) sent {
	request.ip = proxyPeer
	request.headers = map[string]string{"Remote-User": user, "Remote-Groups": "bkd-admins"}
	return request
}

func TestForwardedUserIsSignedInWithoutACookie(t *testing.T) {
	app := newTestApp(t, forwardAuthConfig(t))

	page := app.send(viaProxy(get("/users"), "alice"))

	assertStatus(t, page, http.StatusOK)
	mustContain(t, page, "alice")
}

func TestForwardedHeaderFromAnUntrustedPeerIsIgnored(t *testing.T) {
	app := newTestApp(t, forwardAuthConfig(t))
	request := viaProxy(get("/users"), "alice")
	request.ip = "192.0.2.9"

	mustRedirect(t, app.send(request), "/login?next=%2Fusers")
}

func TestForwardedInvalidUserNameSignsNobodyIn(t *testing.T) {
	app := newTestApp(t, forwardAuthConfig(t))
	token := app.signIn("bob", false)
	request := viaProxy(get("/settings"), "not a valid name!").as(token)

	assertStatus(t, app.send(request), http.StatusSeeOther)
}

func TestForwardedFirstUserClosesSetup(t *testing.T) {
	token, _ := auth.NewSetupToken()
	config := forwardAuthConfig(t)
	config.SetupToken = token
	app := newTestApp(t, config)

	assertStatus(t, app.send(get("/setup")), http.StatusOK)
	assertStatus(t, app.send(viaProxy(get("/users"), "alice")), http.StatusOK)

	mustRedirect(t, app.send(get("/setup")), "/login")
	if app.app.currentSetupToken() != nil {
		t.Fatal("the setup token was not cleared")
	}
}

func TestForwardedAdminNeedsNoPasswordConfirmation(t *testing.T) {
	app := newTestApp(t, forwardAuthConfig(t))
	app.send(viaProxy(get("/users"), "alice"))

	reply := app.send(viaProxy(formPost("/users/1/role", "is_admin=true"), "alice"))

	mustRedirect(t, reply, "/users?notice=user-changed")
}
