package auth

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"

	"github.com/henry40408/bkd/internal/dbtest"
)

const testNow int64 = 1_000_000

var bg = context.Background()

func newSessions(t *testing.T) (*Sessions, *sql.DB) {
	t.Helper()
	pool := dbtest.Open(t)
	return NewSessions(pool, slog.New(slog.NewTextHandler(io.Discard, nil))), pool
}

func startAt(userID, now int64) SessionStart {
	return SessionStart{UserID: userID, Now: now}
}

func createToken(t *testing.T, sessions *Sessions, start SessionStart) string {
	t.Helper()
	token, err := sessions.Create(bg, start)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func lookupFound(t *testing.T, sessions *Sessions, token string, now int64) bool {
	t.Helper()
	_, ok, err := sessions.Lookup(bg, token, now)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func countRows(t *testing.T, pool *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var count int64
	if err := pool.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func mustExec(t *testing.T, pool *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func TestANewSessionFindsItsUser(t *testing.T) {
	sessions, pool := newSessions(t)
	userID := dbtest.InsertUser(t, pool, "henry")
	token := createToken(t, sessions, startAt(userID, testNow))

	user, ok, err := sessions.Lookup(bg, token, testNow)
	if err != nil || !ok {
		t.Fatalf("lookup = %v, %v", ok, err)
	}
	want := SessionUser{UserID: userID, Username: "henry"}
	if user != want {
		t.Fatalf("got %+v, want %+v", user, want)
	}
}

func TestALoginIsRefusedWhenTheAccountChangedMeanwhile(t *testing.T) {
	sessions, pool := newSessions(t)
	userID := dbtest.InsertUser(t, pool, "henry")
	var hash string
	if err := pool.QueryRow("SELECT password_hash FROM users WHERE id = ?1", userID).Scan(&hash); err != nil {
		t.Fatal(err)
	}

	token, ok, err := sessions.CreateIfUnchanged(bg, startAt(userID, testNow), hash)
	if err != nil || !ok || !lookupFound(t, sessions, token, testNow) {
		t.Fatalf("unchanged account: ok = %v, err = %v", ok, err)
	}

	// The password was reset after the old one was verified.
	if _, ok, err := sessions.CreateIfUnchanged(bg, startAt(userID, testNow), "old-hash"); ok || err != nil {
		t.Fatalf("stale hash: ok = %v, err = %v", ok, err)
	}

	// The account was disabled in the same gap.
	mustExec(t, pool, "UPDATE users SET disabled_at = 1 WHERE id = ?1", userID)
	if _, ok, err := sessions.CreateIfUnchanged(bg, startAt(userID, testNow), hash); ok || err != nil {
		t.Fatalf("disabled account: ok = %v, err = %v", ok, err)
	}
	if count := countRows(t, pool, "SELECT count(*) FROM sessions"); count != 1 {
		t.Fatalf("%d sessions, want 1", count)
	}
}

func TestTheDatabaseNeverHoldsTheRawToken(t *testing.T) {
	sessions, pool := newSessions(t)
	userID := dbtest.InsertUser(t, pool, "henry")
	token := createToken(t, sessions, startAt(userID, testNow))

	var stored string
	if err := pool.QueryRow("SELECT token_hash FROM sessions").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == token || stored != sha256Hex(token) {
		t.Fatalf("stored %q for token %q", stored, token)
	}
	if len(token) != 64 {
		t.Fatalf("token has %d characters, want 64", len(token))
	}
}

func TestTokensAreUniqueAndUnknownTokensFail(t *testing.T) {
	sessions, pool := newSessions(t)
	userID := dbtest.InsertUser(t, pool, "henry")
	first := createToken(t, sessions, startAt(userID, testNow))
	second := createToken(t, sessions, startAt(userID, testNow))
	if first == second {
		t.Fatal("two sessions share a token")
	}
	if lookupFound(t, sessions, "not-a-token", testNow) {
		t.Fatal("unknown token was found")
	}
}

func TestAnExpiredSessionIsRejected(t *testing.T) {
	sessions, pool := newSessions(t)
	userID := dbtest.InsertUser(t, pool, "henry")
	token := createToken(t, sessions, startAt(userID, testNow))

	// Use keeps the session from going idle; only the absolute lifetime ends it.
	for day := int64(1); day < 30; day += 5 {
		if !lookupFound(t, sessions, token, testNow+day*24*60*60) {
			t.Fatalf("session ended on day %d", day)
		}
	}
	if !lookupFound(t, sessions, token, testNow+SessionLifetimeSeconds-1) {
		t.Fatal("session ended before its lifetime")
	}
	if lookupFound(t, sessions, token, testNow+SessionLifetimeSeconds) {
		t.Fatal("session outlived its lifetime")
	}
}

func TestAnIdleSessionEndsButUseKeepsItAlive(t *testing.T) {
	sessions, pool := newSessions(t)
	userID := dbtest.InsertUser(t, pool, "henry")
	idle := createToken(t, sessions, startAt(userID, testNow))
	active := createToken(t, sessions, startAt(userID, testNow))

	// Used just before the idle limit, so its clock restarts.
	if !lookupFound(t, sessions, active, testNow+SessionIdleSeconds-1) {
		t.Fatal("session ended just before the idle limit")
	}
	later := testNow + SessionIdleSeconds + 1
	if lookupFound(t, sessions, idle, later) {
		t.Fatal("idle session was found")
	}
	if !lookupFound(t, sessions, active, later) {
		t.Fatal("active session was lost")
	}
	// The absolute lifetime still applies to a session that is always in use.
	if lookupFound(t, sessions, active, testNow+SessionLifetimeSeconds) {
		t.Fatal("active session outlived its lifetime")
	}
}

func TestTheIdleClockMovesAtMostOnceAnInterval(t *testing.T) {
	sessions, pool := newSessions(t)
	userID := dbtest.InsertUser(t, pool, "henry")
	token := createToken(t, sessions, startAt(userID, testNow))
	lastUsed := func() int64 {
		return countRows(t, pool, "SELECT last_used_at FROM sessions")
	}

	lookupFound(t, sessions, token, testNow+60)
	if got := lastUsed(); got != testNow {
		t.Fatalf("last_used_at = %d after 60 s, want %d", got, testNow)
	}
	lookupFound(t, sessions, token, testNow+idleRefreshSeconds)
	if got := lastUsed(); got != testNow+idleRefreshSeconds {
		t.Fatalf("last_used_at = %d after an interval, want %d", got, testNow+idleRefreshSeconds)
	}
}

func TestTheListShowsOnlyTheUsersLiveSessionsAndMarksTheCurrentOne(t *testing.T) {
	sessions, pool := newSessions(t)
	henry := dbtest.InsertUser(t, pool, "henry")
	mei := dbtest.InsertUser(t, pool, "mei")
	first := createToken(t, sessions, SessionStart{UserID: henry, Now: testNow, IP: "192.0.2.7", UserAgent: "Firefox"})
	second := createToken(t, sessions, startAt(henry, testNow+10))
	createToken(t, sessions, startAt(mei, testNow))
	// One that went idle is not listed.
	createToken(t, sessions, startAt(henry, testNow-SessionIdleSeconds))

	list, err := sessions.ListForUser(bg, henry, first, testNow+20)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("%d sessions, want 2", len(list))
	}
	if list[0].LastUsedAt != testNow+10 || list[0].IsCurrent || !list[1].IsCurrent {
		t.Fatalf("unexpected order or marks: %+v", list)
	}
	if list[1].IP != "192.0.2.7" || list[1].UserAgent != "Firefox" {
		t.Fatalf("unexpected details: %+v", list[1])
	}

	list, err = sessions.ListForUser(bg, henry, second, testNow+20)
	if err != nil {
		t.Fatal(err)
	}
	if !list[0].IsCurrent || list[1].IsCurrent {
		t.Fatalf("unexpected marks: %+v", list)
	}
}

func TestASessionCanBeEndedOnlyByItsOwner(t *testing.T) {
	sessions, pool := newSessions(t)
	henry := dbtest.InsertUser(t, pool, "henry")
	mei := dbtest.InsertUser(t, pool, "mei")
	mine := createToken(t, sessions, startAt(henry, testNow))
	theirs := createToken(t, sessions, startAt(mei, testNow))
	list, err := sessions.ListForUser(bg, mei, theirs, testNow)
	if err != nil {
		t.Fatal(err)
	}
	theirID := list[0].ID

	if found, err := sessions.RevokeByID(bg, henry, theirID); found || err != nil {
		t.Fatalf("henry ended mei's session: %v, %v", found, err)
	}
	if !lookupFound(t, sessions, theirs, testNow) {
		t.Fatal("mei's session is gone")
	}
	if found, err := sessions.RevokeByID(bg, mei, theirID); !found || err != nil {
		t.Fatalf("mei could not end her session: %v, %v", found, err)
	}
	if lookupFound(t, sessions, theirs, testNow) {
		t.Fatal("mei's session survived")
	}
	if !lookupFound(t, sessions, mine, testNow) {
		t.Fatal("henry's session is gone")
	}
}

func TestEndingTheOthersKeepsTheCurrentSession(t *testing.T) {
	sessions, pool := newSessions(t)
	henry := dbtest.InsertUser(t, pool, "henry")
	mei := dbtest.InsertUser(t, pool, "mei")
	keep := createToken(t, sessions, startAt(henry, testNow))
	other := createToken(t, sessions, startAt(henry, testNow))
	meiToken := createToken(t, sessions, startAt(mei, testNow))

	if ended, err := sessions.RevokeOthers(bg, henry, keep); ended != 1 || err != nil {
		t.Fatalf("ended = %d, err = %v", ended, err)
	}
	if !lookupFound(t, sessions, keep, testNow) || lookupFound(t, sessions, other, testNow) ||
		!lookupFound(t, sessions, meiToken, testNow) {
		t.Fatal("wrong sessions survived")
	}
}

func confirmed(t *testing.T, sessions *Sessions, token string, now int64) bool {
	t.Helper()
	ok, err := sessions.IsConfirmed(bg, token, now)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestAConfirmationLastsFiveMinutes(t *testing.T) {
	sessions, pool := newSessions(t)
	userID := dbtest.InsertUser(t, pool, "henry")
	token := createToken(t, sessions, startAt(userID, testNow))

	// Signing in counts as a confirmation.
	if !confirmed(t, sessions, token, testNow+ConfirmWindowSeconds-1) {
		t.Fatal("sign-in was not a confirmation")
	}
	stale := testNow + ConfirmWindowSeconds
	if confirmed(t, sessions, token, stale) {
		t.Fatal("confirmation outlived its window")
	}
	if err := sessions.Confirm(bg, token, stale); err != nil {
		t.Fatal(err)
	}
	if !confirmed(t, sessions, token, stale+1) {
		t.Fatal("confirmation did not register")
	}
	if confirmed(t, sessions, "not-a-token", stale) {
		t.Fatal("unknown token was confirmed")
	}
}

func TestTheSweepDeletesIdleSessions(t *testing.T) {
	sessions, pool := newSessions(t)
	userID := dbtest.InsertUser(t, pool, "henry")
	createToken(t, sessions, startAt(userID, testNow))

	if deleted, err := sessions.DeleteExpired(bg, testNow+SessionIdleSeconds-1); deleted != 0 || err != nil {
		t.Fatalf("before idle: %d, %v", deleted, err)
	}
	if deleted, err := sessions.DeleteExpired(bg, testNow+SessionIdleSeconds); deleted != 1 || err != nil {
		t.Fatalf("at idle: %d, %v", deleted, err)
	}
}

func TestADisabledUserLosesAccessImmediately(t *testing.T) {
	sessions, pool := newSessions(t)
	userID := dbtest.InsertUser(t, pool, "henry")
	token := createToken(t, sessions, startAt(userID, testNow))

	mustExec(t, pool, "UPDATE users SET disabled_at = ?1 WHERE id = ?2", testNow, userID)
	if lookupFound(t, sessions, token, testNow) {
		t.Fatal("disabled user still has a session")
	}
}

func TestTheAdminFlagIsReadOnEveryLookup(t *testing.T) {
	sessions, pool := newSessions(t)
	userID := dbtest.InsertUser(t, pool, "henry")
	token := createToken(t, sessions, startAt(userID, testNow))

	mustExec(t, pool, "UPDATE users SET is_admin = 1 WHERE id = ?1", userID)
	user, ok, err := sessions.Lookup(bg, token, testNow)
	if err != nil || !ok || !user.IsAdmin {
		t.Fatalf("user = %+v, ok = %v, err = %v", user, ok, err)
	}
}

func TestRevokeRemovesOnlyThatSession(t *testing.T) {
	sessions, pool := newSessions(t)
	userID := dbtest.InsertUser(t, pool, "henry")
	kept := createToken(t, sessions, startAt(userID, testNow))
	revoked := createToken(t, sessions, startAt(userID, testNow))

	if err := sessions.Revoke(bg, revoked); err != nil {
		t.Fatal(err)
	}
	if lookupFound(t, sessions, revoked, testNow) || !lookupFound(t, sessions, kept, testNow) {
		t.Fatal("wrong session was revoked")
	}
}

func TestRevokeAllRemovesEverySessionOfOneUser(t *testing.T) {
	sessions, pool := newSessions(t)
	henry := dbtest.InsertUser(t, pool, "henry")
	mei := dbtest.InsertUser(t, pool, "mei")
	henryToken := createToken(t, sessions, startAt(henry, testNow))
	meiToken := createToken(t, sessions, startAt(mei, testNow))

	if err := RevokeAllForUser(bg, pool, henry); err != nil {
		t.Fatal(err)
	}
	if lookupFound(t, sessions, henryToken, testNow) || !lookupFound(t, sessions, meiToken, testNow) {
		t.Fatal("wrong sessions were revoked")
	}
}

func TestDeleteExpiredKeepsLiveSessions(t *testing.T) {
	sessions, pool := newSessions(t)
	userID := dbtest.InsertUser(t, pool, "henry")
	old := createToken(t, sessions, startAt(userID, testNow))
	later := testNow + SessionLifetimeSeconds
	fresh := createToken(t, sessions, startAt(userID, later))

	if deleted, err := sessions.DeleteExpired(bg, later); deleted != 1 || err != nil {
		t.Fatalf("deleted = %d, err = %v", deleted, err)
	}
	if lookupFound(t, sessions, old, testNow) || !lookupFound(t, sessions, fresh, later) {
		t.Fatal("wrong sessions were deleted")
	}
}
