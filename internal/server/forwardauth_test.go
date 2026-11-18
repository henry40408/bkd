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

	state := app.send(viaProxy(get("/api/state"), "alice")).json()

	user, _ := state["user"].(map[string]any)
	if user["username"] != "alice" || user["is_admin"] != true || user["forwarded"] != true {
		t.Fatalf("state = %v", state)
	}
}

func TestForwardedHeaderFromAnUntrustedPeerIsIgnored(t *testing.T) {
	app := newTestApp(t, forwardAuthConfig(t))
	request := viaProxy(get("/api/state"), "alice")
	request.ip = "192.0.2.9"

	state := app.send(request).json()

	if state["user"] != nil {
		t.Fatalf("an untrusted peer signed in: %v", state)
	}
}

func TestForwardedInvalidUserNameSignsNobodyIn(t *testing.T) {
	app := newTestApp(t, forwardAuthConfig(t))
	token := app.signIn("bob", false)
	request := viaProxy(get("/api/state"), "not a valid name!").as(token)

	state := app.send(request).json()

	if state["user"] != nil {
		t.Fatalf("the cookie filled the gap: %v", state)
	}
}

func TestForwardedFirstUserClosesSetup(t *testing.T) {
	token, _ := auth.NewSetupToken()
	config := forwardAuthConfig(t)
	config.SetupToken = token
	app := newTestApp(t, config)

	if state := app.send(get("/api/state")).json(); state["setup_required"] != true {
		t.Fatalf("setup should be open before the proxy user: %v", state)
	}
	state := app.send(viaProxy(get("/api/state"), "alice")).json()

	if state["setup_required"] != false {
		t.Fatalf("setup stayed open after the first account: %v", state)
	}
	if app.app.currentSetupToken() != nil {
		t.Fatal("the setup token was not cleared")
	}
}

func TestForwardedUserCannotChangeAPasswordItDoesNotHave(t *testing.T) {
	app := newTestApp(t, forwardAuthConfig(t))
	change := put("/api/account/password", map[string]any{"current": "x", "new": testPassword})

	reply := app.send(viaProxy(change, "alice"))

	if reply.status != http.StatusForbidden {
		t.Fatalf("status = %d %s", reply.status, reply.text)
	}
}

func TestForwardedAdminNeedsNoPasswordConfirmation(t *testing.T) {
	app := newTestApp(t, forwardAuthConfig(t))
	app.send(viaProxy(get("/api/state"), "alice"))

	reply := app.send(viaProxy(put("/api/users/1/role", map[string]any{"is_admin": true}), "alice"))

	if reply.status == http.StatusUnauthorized || reply.status == http.StatusForbidden {
		t.Fatalf("a proxy admin was asked to confirm: %d %s", reply.status, reply.text)
	}
}
