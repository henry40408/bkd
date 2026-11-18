package users

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/henry40408/bkd/internal/auth"
	"github.com/henry40408/bkd/internal/dbtest"
)

var bg = context.Background()

func add(t *testing.T, pool *sql.DB, name string, isAdmin bool) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(
		`INSERT INTO users (username, password_hash, is_admin, created_at)
         VALUES (?1, 'hash', ?2, 0) RETURNING id`, name, boolToInt(isAdmin)).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func account(username string) NewAccount {
	return NewAccount{Username: username, PasswordHash: "hash"}
}

func count(t *testing.T, pool *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func sessionsOf(t *testing.T, pool *sql.DB, id int64) int64 {
	return count(t, pool, "SELECT count(*) FROM sessions WHERE user_id = ?1", id)
}

func startSession(t *testing.T, pool *sql.DB, userID int64) {
	t.Helper()
	sessions := auth.NewSessions(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := sessions.Create(bg, auth.SessionStart{UserID: userID}); err != nil {
		t.Fatal(err)
	}
}

func mustChange(t *testing.T, got Change, err error, want Change) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("change = %d, want %d", got, want)
	}
}

func TestATakenUsernameIsReportedNotOverwritten(t *testing.T) {
	pool := dbtest.Open(t)
	admin := add(t, pool, "henry", true)

	_, change, err := Create(bg, pool, admin, account("henry"))
	mustChange(t, change, err, ChangeUsernameTaken)
	if list, err := List(bg, pool); err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}
}

func TestTheLastAdminCannotBeDemotedDisabledOrDeleted(t *testing.T) {
	pool := dbtest.Open(t)
	admin := add(t, pool, "henry", true)
	add(t, pool, "mei", false)
	one := int64(1)

	change, err := SetAdmin(bg, pool, admin, admin, false)
	mustChange(t, change, err, ChangeLastAdmin)
	change, err = SetDisabled(bg, pool, admin, admin, &one)
	mustChange(t, change, err, ChangeLastAdmin)
	change, err = Delete(bg, pool, admin, admin)
	mustChange(t, change, err, ChangeLastAdmin)

	users, err := List(bg, pool)
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range users {
		if user.ID == admin && user.IsAdmin && !user.IsDisabled {
			return
		}
	}
	t.Fatal("the admin is no longer an active admin")
}

func TestADisabledAdminDoesNotCountAsAnotherAdmin(t *testing.T) {
	pool := dbtest.Open(t)
	first := add(t, pool, "henry", true)
	second := add(t, pool, "mei", true)
	one := int64(1)

	change, err := SetDisabled(bg, pool, first, second, &one)
	mustChange(t, change, err, ChangeDone)
	change, err = SetAdmin(bg, pool, first, first, false)
	mustChange(t, change, err, ChangeLastAdmin)

	// Enabling the second admin makes the first one removable again.
	change, err = SetDisabled(bg, pool, first, second, nil)
	mustChange(t, change, err, ChangeDone)
	change, err = SetAdmin(bg, pool, first, first, false)
	mustChange(t, change, err, ChangeDone)
}

func TestWithTwoAdminsOneMayGo(t *testing.T) {
	pool := dbtest.Open(t)
	first := add(t, pool, "henry", true)
	add(t, pool, "mei", true)
	change, err := Delete(bg, pool, first, first)
	mustChange(t, change, err, ChangeDone)
}

func TestTwoAdminsRemovingEachOtherAtOnceLeaveOne(t *testing.T) {
	pool := dbtest.Open(t)
	first := add(t, pool, "henry", true)
	second := add(t, pool, "mei", true)

	outcomes := make([]Change, 2)
	var group sync.WaitGroup
	for index, pair := range [][2]int64{{first, second}, {second, first}} {
		group.Add(1)
		go func() {
			defer group.Done()
			change, err := SetAdmin(bg, pool, pair[0], pair[1], false)
			if err != nil {
				t.Error(err)
			}
			outcomes[index] = change
		}()
	}
	group.Wait()

	// The loser is refused either as the last admin or because it was just demoted.
	done := 0
	for _, outcome := range outcomes {
		if outcome == ChangeDone {
			done++
		}
	}
	if done != 1 {
		t.Fatalf("%d changes were done, want 1: %v", done, outcomes)
	}
	if admins := count(t, pool, "SELECT count(*) FROM users WHERE is_admin = 1"); admins != 1 {
		t.Fatalf("%d admins, want 1", admins)
	}
}

func TestACallerWhoLostAdminPowerChangesNothing(t *testing.T) {
	pool := dbtest.Open(t)
	admin := add(t, pool, "henry", true)
	demoted := add(t, pool, "mei", false)
	disabled := add(t, pool, "bob", true)
	one := int64(1)
	if change, err := SetDisabled(bg, pool, admin, disabled, &one); err != nil || change != ChangeDone {
		t.Fatalf("disable: %d, %v", change, err)
	}
	victim := add(t, pool, "sam", false)

	for _, caller := range []int64{demoted, disabled} {
		_, change, err := Create(bg, pool, caller, account("new"))
		mustChange(t, change, err, ChangeCallerNotAdmin)
		change, err = ResetPassword(bg, pool, caller, victim, "x")
		mustChange(t, change, err, ChangeCallerNotAdmin)
		change, err = SetAdmin(bg, pool, caller, victim, true)
		mustChange(t, change, err, ChangeCallerNotAdmin)
		change, err = Delete(bg, pool, caller, victim)
		mustChange(t, change, err, ChangeCallerNotAdmin)
	}
	if users, err := List(bg, pool); err != nil || len(users) != 4 {
		t.Fatalf("users = %d, err = %v", len(users), err)
	}
}

func TestDisablingAndPasswordResetEndEverySession(t *testing.T) {
	pool := dbtest.Open(t)
	admin := add(t, pool, "henry", true)
	user := add(t, pool, "mei", false)
	other := add(t, pool, "bob", false)
	for _, id := range []int64{user, user, other} {
		startSession(t, pool, id)
	}

	change, err := ResetPassword(bg, pool, admin, user, "new")
	mustChange(t, change, err, ChangeDone)
	if got := sessionsOf(t, pool, user); got != 0 {
		t.Fatalf("%d sessions after reset", got)
	}
	if got := sessionsOf(t, pool, other); got != 1 {
		t.Fatalf("%d sessions of the other user", got)
	}

	startSession(t, pool, user)
	one := int64(1)
	change, err = SetDisabled(bg, pool, admin, user, &one)
	mustChange(t, change, err, ChangeDone)
	if got := sessionsOf(t, pool, user); got != 0 {
		t.Fatalf("%d sessions after disable", got)
	}
	var hash string
	if err := pool.QueryRow("SELECT password_hash FROM users WHERE id = ?1", user).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash != "new" {
		t.Fatalf("hash = %q", hash)
	}
}

func TestDeletingAUserRemovesTheirBookmarksAndSessions(t *testing.T) {
	pool := dbtest.Open(t)
	admin := add(t, pool, "henry", true)
	user := add(t, pool, "mei", false)
	startSession(t, pool, user)
	_, err := pool.Exec(
		`INSERT INTO bookmarks (user_id, url, title, created_at, updated_at)
         VALUES (?1, 'https://a.b', 't', 0, 0)`, user)
	if err != nil {
		t.Fatal(err)
	}

	change, err := Delete(bg, pool, admin, user)
	mustChange(t, change, err, ChangeDone)
	if got := count(t, pool, "SELECT count(*) FROM bookmarks"); got != 0 {
		t.Fatalf("%d bookmarks left", got)
	}
	if got := sessionsOf(t, pool, user); got != 0 {
		t.Fatalf("%d sessions left", got)
	}
}

func TestChangingAnUnknownUserReportsNotFound(t *testing.T) {
	pool := dbtest.Open(t)
	admin := add(t, pool, "henry", true)
	zero := int64(0)

	change, err := SetAdmin(bg, pool, admin, 99, true)
	mustChange(t, change, err, ChangeNotFound)
	change, err = SetDisabled(bg, pool, admin, 99, &zero)
	mustChange(t, change, err, ChangeNotFound)
	change, err = ResetPassword(bg, pool, admin, 99, "x")
	mustChange(t, change, err, ChangeNotFound)
	change, err = Delete(bg, pool, admin, 99)
	mustChange(t, change, err, ChangeNotFound)
}

func TestOwnPasswordChangesOnlyWhileTheVerifiedHashIsCurrent(t *testing.T) {
	pool := dbtest.Open(t)
	user := add(t, pool, "mei", false)
	startSession(t, pool, user)

	stale, err := ChangeOwnPassword(bg, pool, OwnPasswordChange{ID: user, VerifiedHash: "old", NewHash: "new"})
	if err != nil || stale {
		t.Fatalf("stale hash: changed = %v, err = %v", stale, err)
	}
	changed, err := ChangeOwnPassword(bg, pool, OwnPasswordChange{ID: user, VerifiedHash: "hash", NewHash: "new"})
	if err != nil || !changed {
		t.Fatalf("current hash: changed = %v, err = %v", changed, err)
	}
	if got := sessionsOf(t, pool, user); got != 0 {
		t.Fatalf("%d sessions left", got)
	}
}

func TestForwardedAccountsAreCreatedAndSettled(t *testing.T) {
	pool := dbtest.Open(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	yes, no := true, false

	first, found, err := AdoptForwarded(bg, pool, logger, ForwardedAccount{Username: "henry"})
	if err != nil || !found || !first.IsAdmin {
		t.Fatalf("first account = %+v, %v, %v", first, found, err)
	}
	second, found, err := AdoptForwarded(bg, pool, logger, ForwardedAccount{Username: "mei"})
	if err != nil || !found || second.IsAdmin {
		t.Fatalf("second account = %+v, %v, %v", second, found, err)
	}
	// The group decides the role, but the last admin is never demoted.
	kept, _, err := AdoptForwarded(bg, pool, logger, ForwardedAccount{Username: "henry", IsAdmin: &no})
	if err != nil || !kept.IsAdmin {
		t.Fatalf("last admin = %+v, %v", kept, err)
	}
	promoted, _, err := AdoptForwarded(bg, pool, logger, ForwardedAccount{Username: "mei", IsAdmin: &yes})
	if err != nil || !promoted.IsAdmin {
		t.Fatalf("promoted = %+v, %v", promoted, err)
	}
	demoted, _, err := AdoptForwarded(bg, pool, logger, ForwardedAccount{Username: "henry", IsAdmin: &no})
	if err != nil || demoted.IsAdmin {
		t.Fatalf("demoted = %+v, %v", demoted, err)
	}
}

func TestADisabledForwardedAccountGetsNoAccess(t *testing.T) {
	pool := dbtest.Open(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := pool.Exec(
		"INSERT INTO users (username, password_hash, created_at, disabled_at) VALUES ('mei', 'x', 0, 1)"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := AdoptForwarded(bg, pool, logger, ForwardedAccount{Username: "mei"}); found || err != nil {
		t.Fatalf("found = %v, err = %v", found, err)
	}
}

func TestLookupsAndFlags(t *testing.T) {
	pool := dbtest.Open(t)
	id := add(t, pool, "henry", false)

	record, found, err := FindForLogin(bg, pool, "henry")
	if err != nil || !found || record.ID != id || record.PasswordHash != "hash" || record.IsDisabled {
		t.Fatalf("record = %+v, %v, %v", record, found, err)
	}
	if _, found, _ := FindForLogin(bg, pool, "nobody"); found {
		t.Fatal("found a user that does not exist")
	}
	if name, found, err := UsernameOf(bg, pool, id); name != "henry" || !found || err != nil {
		t.Fatalf("username = %q, %v, %v", name, found, err)
	}

	if enabled, _ := LinkCheckEnabled(bg, pool, id); enabled {
		t.Fatal("link check is on by default")
	}
	if err := SetLinkCheckEnabled(bg, pool, id, true); err != nil {
		t.Fatal(err)
	}
	if wants, _ := WantsBackgroundCheck(bg, pool, id); !wants {
		t.Fatal("an opted-in active user does not want the check")
	}
	one := int64(1)
	other := add(t, pool, "mei", true)
	if change, err := SetDisabled(bg, pool, other, id, &one); err != nil || change != ChangeDone {
		t.Fatalf("disable: %d, %v", change, err)
	}
	if wants, _ := WantsBackgroundCheck(bg, pool, id); wants {
		t.Fatal("a disabled user wants the check")
	}
	if wants, _ := WantsBackgroundCheck(bg, pool, 999); wants {
		t.Fatal("a missing user wants the check")
	}
}
