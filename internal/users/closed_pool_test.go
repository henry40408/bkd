package users

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/henry40408/bkd/internal/dbtest"
)

// Every operation must report a database failure as an error that says what
// was being done, and must not pretend the account was found or changed.
func TestEveryOperationReportsADatabaseFailure(t *testing.T) {
	pool := dbtest.Open(t)
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	disabledAt := int64(5)
	cases := []struct {
		name string
		run  func() error
		want string
	}{
		{"FindForLogin", func() error {
			_, found, err := FindForLogin(bg, pool, "alice")
			mustNotFind(t, found)
			return err
		}, "find user for login"},
		{"AdoptForwarded", func() error {
			_, found, err := AdoptForwarded(bg, pool, logger, ForwardedAccount{Username: "alice"})
			mustNotFind(t, found)
			return err
		}, "find forwarded user"},
		{"UsernameOf", func() error {
			_, found, err := UsernameOf(bg, pool, 1)
			mustNotFind(t, found)
			return err
		}, "find username"},
		{"LinkCheckEnabled", func() error { _, err := LinkCheckEnabled(bg, pool, 1); return err }, "read user flag"},
		{"WantsBackgroundCheck", func() error { _, err := WantsBackgroundCheck(bg, pool, 1); return err }, "read user flag"},
		{"SetLinkCheckEnabled", func() error { return SetLinkCheckEnabled(bg, pool, 1, true) }, "set link check"},
		{"LinkCheckUserAgent", func() error { _, err := LinkCheckUserAgent(bg, pool, 1); return err }, "read link check user agent"},
		{"List", func() error { _, err := List(bg, pool); return err }, "list users"},
		{"Create", func() error {
			_, change, err := Create(bg, pool, 1, account("bob"))
			mustBeDone(t, change)
			return err
		}, "create user"},
		{"SetAdmin", func() error {
			change, err := SetAdmin(bg, pool, 1, 2, true)
			mustBeDone(t, change)
			return err
		}, "change user"},
		{"SetDisabled", func() error {
			change, err := SetDisabled(bg, pool, 1, 2, &disabledAt)
			mustBeDone(t, change)
			return err
		}, "change user"},
		{"ResetPassword", func() error {
			change, err := ResetPassword(bg, pool, 1, 2, "hash")
			mustBeDone(t, change)
			return err
		}, "change user"},
		{"Delete", func() error {
			change, err := Delete(bg, pool, 1, 2)
			mustBeDone(t, change)
			return err
		}, "change user"},
		{"ChangeOwnPassword", func() error {
			changed, err := ChangeOwnPassword(bg, pool, OwnPasswordChange{ID: 1, VerifiedHash: "a", NewHash: "b"})
			if changed {
				t.Error("a password change was reported although the database failed")
			}
			return err
		}, "change own password"},
	}
	for _, c := range cases {
		err := c.run()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error = %v, want one that says %q", c.name, err, c.want)
		}
	}
}

func mustNotFind(t *testing.T, found bool) {
	t.Helper()
	if found {
		t.Error("an account was found although the database failed")
	}
}

func mustBeDone(t *testing.T, change Change) {
	t.Helper()
	if change != ChangeDone {
		t.Errorf("change = %v although the database failed", change)
	}
}

// A write the database refuses, after the reads that came before it worked,
// must come back as an error, not as a change that did not happen.
func TestEveryWriteReportsARefusedChange(t *testing.T) {
	pool := dbtest.Open(t)
	admin := add(t, pool, "root", true)
	member := add(t, pool, "mei", false)
	for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
		if _, err := pool.Exec("CREATE TRIGGER refuse_" + event + " BEFORE " + event + " ON users BEGIN SELECT RAISE(ABORT, 'refused'); END"); err != nil {
			t.Fatal(err)
		}
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	disabledAt, makeAdmin := int64(5), true
	cases := []struct {
		name string
		run  func() error
	}{
		{"Create", func() error { _, _, err := Create(bg, pool, admin, account("bob")); return err }},
		{"SetAdmin", func() error { _, err := SetAdmin(bg, pool, admin, member, true); return err }},
		{"SetDisabled", func() error { _, err := SetDisabled(bg, pool, admin, member, &disabledAt); return err }},
		{"ResetPassword", func() error { _, err := ResetPassword(bg, pool, admin, member, "hash"); return err }},
		{"Delete", func() error { _, err := Delete(bg, pool, admin, member); return err }},
		{"SetLinkCheckEnabled", func() error { return SetLinkCheckEnabled(bg, pool, member, true) }},
		{"SetLinkCheck", func() error {
			agent := "Agent/1"
			return SetLinkCheck(bg, pool, member, LinkCheckChange{Enabled: true, UserAgent: &agent})
		}},
		{"ChangeOwnPassword", func() error {
			_, err := ChangeOwnPassword(bg, pool, OwnPasswordChange{ID: member, VerifiedHash: "hash", NewHash: "new"})
			return err
		}},
		{"AdoptForwarded creating", func() error {
			_, _, err := AdoptForwarded(bg, pool, logger, ForwardedAccount{Username: "newcomer", Now: 1})
			return err
		}},
		{"AdoptForwarded changing the role", func() error {
			_, _, err := AdoptForwarded(bg, pool, logger, ForwardedAccount{Username: "mei", IsAdmin: &makeAdmin, Now: 1})
			return err
		}},
	}
	for _, c := range cases {
		if err := c.run(); err == nil {
			t.Errorf("%s: no error although the database refused the write", c.name)
		}
	}
}
