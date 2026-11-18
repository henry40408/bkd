package auth

import (
	"testing"

	"github.com/henry40408/bkd/internal/dbtest"
)

// A session store that cannot reach its database must say so. It must never
// answer "signed in" or "revoked" on a guess.
func TestEverySessionOperationReportsAClosedDatabase(t *testing.T) {
	sessions, pool := newSessions(t)
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	start := startAt(1, testNow)
	cases := []struct {
		name string
		run  func() error
	}{
		{"Create", func() error { _, err := sessions.Create(bg, start); return err }},
		{"CreateIfUnchanged", func() error {
			_, ok, err := sessions.CreateIfUnchanged(bg, start, "hash")
			if ok {
				t.Error("CreateIfUnchanged made a session on a closed database")
			}
			return err
		}},
		{"Lookup", func() error {
			_, ok, err := sessions.Lookup(bg, "token", testNow)
			if ok {
				t.Error("Lookup signed a user in on a closed database")
			}
			return err
		}},
		{"Revoke", func() error { return sessions.Revoke(bg, "token") }},
		{"RevokeAllForUser", func() error { return RevokeAllForUser(bg, pool, 1) }},
		{"Confirm", func() error { return sessions.Confirm(bg, "token", testNow) }},
		{"IsConfirmed", func() error { _, err := sessions.IsConfirmed(bg, "token", testNow); return err }},
		{"ListForUser", func() error { _, err := sessions.ListForUser(bg, 1, "token", testNow); return err }},
		{"RevokeByID", func() error { _, err := sessions.RevokeByID(bg, 1, "id"); return err }},
		{"RevokeOthers", func() error { _, err := sessions.RevokeOthers(bg, 1, "token"); return err }},
		{"DeleteExpired", func() error { _, err := sessions.DeleteExpired(bg, testNow); return err }},
	}
	for _, c := range cases {
		if err := c.run(); err == nil {
			t.Errorf("%s: no error from a closed database", c.name)
		}
	}
}

// A write the database refuses must come back as an error: a session that was
// not revoked must not be reported as revoked.
func TestEverySessionWriteReportsARefusedChange(t *testing.T) {
	sessions, pool := newSessions(t)
	userID := dbtest.InsertUser(t, pool, "henry")
	token := createToken(t, sessions, startAt(userID, testNow))
	mustExec(t, pool, "UPDATE sessions SET expires_at = 1")
	for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
		mustExec(t, pool, "CREATE TRIGGER refuse_"+event+" BEFORE "+event+" ON sessions BEGIN SELECT RAISE(ABORT, 'refused'); END")
	}
	own := sha256Hex(token)[:16]
	cases := []struct {
		name string
		run  func() error
	}{
		{"Create", func() error { _, err := sessions.Create(bg, startAt(userID, testNow)); return err }},
		{"CreateIfUnchanged", func() error {
			_, _, err := sessions.CreateIfUnchanged(bg, startAt(userID, testNow), "x")
			return err
		}},
		{"Revoke", func() error { return sessions.Revoke(bg, token) }},
		{"RevokeAllForUser", func() error { return RevokeAllForUser(bg, pool, userID) }},
		{"Confirm", func() error { return sessions.Confirm(bg, token, testNow) }},
		{"RevokeByID", func() error { _, err := sessions.RevokeByID(bg, userID, own); return err }},
		{"RevokeOthers", func() error { _, err := sessions.RevokeOthers(bg, userID, "another token"); return err }},
		{"DeleteExpired", func() error { _, err := sessions.DeleteExpired(bg, testNow); return err }},
	}
	for _, c := range cases {
		if err := c.run(); err == nil {
			t.Errorf("%s: no error although the database refused the write", c.name)
		}
	}
}
