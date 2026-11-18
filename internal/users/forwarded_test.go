package users

import (
	"io"
	"log/slog"
	"testing"

	"github.com/henry40408/bkd/internal/dbtest"
)

var quietLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestUsernameOfNamesAnAccountAndReportsAMissingOne(t *testing.T) {
	pool := dbtest.Open(t)
	id := add(t, pool, "mei", false)

	name, found, err := UsernameOf(bg, pool, id)
	if err != nil || !found || name != "mei" {
		t.Fatalf("UsernameOf(%d) = %q, %v, %v", id, name, found, err)
	}
	name, found, err = UsernameOf(bg, pool, id+100)
	if err != nil || found || name != "" {
		t.Fatalf("UsernameOf(missing) = %q, %v, %v, want nothing and no error", name, found, err)
	}
}

func TestAForwardedAccountThatIsDisabledGetsNoAccess(t *testing.T) {
	pool := dbtest.Open(t)
	id := add(t, pool, "mei", false)
	if _, err := pool.Exec("UPDATE users SET disabled_at = 1 WHERE id = ?1", id); err != nil {
		t.Fatal(err)
	}

	_, found, err := AdoptForwarded(bg, pool, quietLogger, ForwardedAccount{Username: "mei", Now: 2})

	if err != nil || found {
		t.Fatalf("found = %v, err = %v, want no access and no error", found, err)
	}
}

func TestTheProxyCannotDemoteTheLastActiveAdmin(t *testing.T) {
	pool := dbtest.Open(t)
	root := add(t, pool, "root", true)
	plain := false

	adopted, found, err := AdoptForwarded(bg, pool, quietLogger, ForwardedAccount{Username: "root", IsAdmin: &plain, Now: 2})

	if err != nil || !found || !adopted.IsAdmin {
		t.Fatalf("adopted = %+v, %v, %v, want root to stay admin", adopted, found, err)
	}
	if got := count(t, pool, "SELECT is_admin FROM users WHERE id = ?1", root); got != 1 {
		t.Fatalf("is_admin = %d, want 1", got)
	}
}

func TestTheProxyDemotesAnAdminWhenAnotherOneRemains(t *testing.T) {
	pool := dbtest.Open(t)
	add(t, pool, "root", true)
	second := add(t, pool, "second", true)
	plain := false

	adopted, found, err := AdoptForwarded(bg, pool, quietLogger, ForwardedAccount{Username: "second", IsAdmin: &plain, Now: 2})

	if err != nil || !found || adopted.IsAdmin {
		t.Fatalf("adopted = %+v, %v, %v, want a plain user", adopted, found, err)
	}
	if got := count(t, pool, "SELECT is_admin FROM users WHERE id = ?1", second); got != 0 {
		t.Fatalf("is_admin = %d, want 0", got)
	}
}
