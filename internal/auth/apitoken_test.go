package auth

import (
	"bytes"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/henry40408/bkd/internal/dbtest"
)

func newAPITokens(t *testing.T) (*APITokens, *sql.DB) {
	t.Helper()
	pool := dbtest.Open(t)
	return NewAPITokens(pool, slog.New(slog.NewTextHandler(io.Discard, nil))), pool
}

func issue(t *testing.T, tokens *APITokens, userID int64, name string) IssuedAPIToken {
	t.Helper()
	issued, ok, err := tokens.Create(bg, NewAPIToken{UserID: userID, Name: name, Now: testNow})
	if err != nil || !ok {
		t.Fatalf("Create = %v, %v", ok, err)
	}
	return issued
}

func lastUsedOf(t *testing.T, pool *sql.DB, token string) sql.NullInt64 {
	t.Helper()
	var lastUsed sql.NullInt64
	err := pool.QueryRow("SELECT last_used_at FROM api_tokens WHERE token_hash = ?1", sha256Hex(token)).Scan(&lastUsed)
	if err != nil {
		t.Fatal(err)
	}
	return lastUsed
}

func TestCreatedTokenWorksAndOnlyItsHashIsStored(t *testing.T) {
	tokens, pool := newAPITokens(t)
	userID := dbtest.InsertUser(t, pool, "henry")
	issued := issue(t, tokens, userID, "extension")
	if len(issued.Token) != 64 {
		t.Fatalf("token = %q, want 64 hex digits", issued.Token)
	}
	if issued.Name != "extension" || issued.CreatedAt != testNow || issued.LastUsedAt != nil {
		t.Fatalf("issued = %+v", issued)
	}
	if issued.ID != sha256Hex(issued.Token)[:16] {
		t.Fatalf("id = %q, want the start of the token's hash", issued.ID)
	}
	if got := countRows(t, pool, "SELECT count(*) FROM api_tokens WHERE token_hash = ?1", issued.Token); got != 0 {
		t.Fatal("the token itself is stored")
	}
	user, ok, err := tokens.Lookup(bg, issued.Token, testNow)
	if err != nil || !ok || user.UserID != userID || user.Username != "henry" || user.IsAdmin || user.ViaProxy {
		t.Fatalf("Lookup = %+v, %v, %v", user, ok, err)
	}
}

func TestTwoTokensDiffer(t *testing.T) {
	tokens, pool := newAPITokens(t)
	userID := dbtest.InsertUser(t, pool, "henry")
	if first, second := issue(t, tokens, userID, "a"), issue(t, tokens, userID, "b"); first.Token == second.Token {
		t.Fatal("two tokens are the same")
	}
}

func TestLookupReportsTheAdminFlag(t *testing.T) {
	tokens, pool := newAPITokens(t)
	userID := dbtest.InsertUser(t, pool, "root")
	mustExec(t, pool, "UPDATE users SET is_admin = 1 WHERE id = ?1", userID)
	user, ok, err := tokens.Lookup(bg, issue(t, tokens, userID, "a").Token, testNow)
	if err != nil || !ok || !user.IsAdmin {
		t.Fatalf("Lookup = %+v, %v, %v", user, ok, err)
	}
}

func TestUnknownOrEmptyTokenIsNotFound(t *testing.T) {
	tokens, pool := newAPITokens(t)
	issue(t, tokens, dbtest.InsertUser(t, pool, "henry"), "a")
	for _, token := range []string{"", "nope", strings.Repeat("0", 64)} {
		if _, ok, err := tokens.Lookup(bg, token, testNow); err != nil || ok {
			t.Errorf("Lookup(%q) = %v, %v", token, ok, err)
		}
	}
}

func TestTokenOfADisabledAccountIsNotFoundUntilEnabledAgain(t *testing.T) {
	tokens, pool := newAPITokens(t)
	userID := dbtest.InsertUser(t, pool, "henry")
	token := issue(t, tokens, userID, "a").Token
	mustExec(t, pool, "UPDATE users SET disabled_at = 1 WHERE id = ?1", userID)
	if _, ok, _ := tokens.Lookup(bg, token, testNow); ok {
		t.Fatal("a disabled account's token works")
	}
	mustExec(t, pool, "UPDATE users SET disabled_at = NULL WHERE id = ?1", userID)
	if _, ok, _ := tokens.Lookup(bg, token, testNow); !ok {
		t.Fatal("the token does not work once the account is enabled again")
	}
}

func TestDeletingTheUserDeletesItsTokens(t *testing.T) {
	tokens, pool := newAPITokens(t)
	userID := dbtest.InsertUser(t, pool, "henry")
	token := issue(t, tokens, userID, "a").Token
	mustExec(t, pool, "DELETE FROM users WHERE id = ?1", userID)
	if _, ok, _ := tokens.Lookup(bg, token, testNow); ok {
		t.Fatal("the token of a deleted user works")
	}
}

func TestFirstUseIsRecordedAndLaterUsesWaitAnHour(t *testing.T) {
	tokens, pool := newAPITokens(t)
	token := issue(t, tokens, dbtest.InsertUser(t, pool, "henry"), "a").Token
	if lastUsed := lastUsedOf(t, pool, token); lastUsed.Valid {
		t.Fatalf("last_used_at = %v before any use", lastUsed)
	}
	use := func(now int64) {
		t.Helper()
		if _, ok, err := tokens.Lookup(bg, token, now); err != nil || !ok {
			t.Fatalf("Lookup = %v, %v", ok, err)
		}
	}
	use(testNow)
	if got := lastUsedOf(t, pool, token); got.Int64 != testNow {
		t.Fatalf("last_used_at = %v after first use, want %d", got, testNow)
	}
	use(testNow + tokenUseRefreshSeconds - 1)
	if got := lastUsedOf(t, pool, token); got.Int64 != testNow {
		t.Fatalf("last_used_at = %v one second short of an hour, want %d", got, testNow)
	}
	use(testNow + tokenUseRefreshSeconds)
	if got := lastUsedOf(t, pool, token); got.Int64 != testNow+tokenUseRefreshSeconds {
		t.Fatalf("last_used_at = %v after an hour, want %d", got, testNow+tokenUseRefreshSeconds)
	}
}

// Recording a use is a courtesy: the token is proven, so a database that refuses
// the write must not turn a good request into a failure.
func TestTokenStillWorksWhenItsUseCannotBeRecorded(t *testing.T) {
	tokens, pool := newAPITokens(t)
	token := issue(t, tokens, dbtest.InsertUser(t, pool, "henry"), "a").Token
	mustExec(t, pool, "CREATE TRIGGER refuse_update BEFORE UPDATE ON api_tokens BEGIN SELECT RAISE(ABORT, 'refused'); END")
	if _, ok, err := tokens.Lookup(bg, token, testNow); err != nil || !ok {
		t.Fatalf("Lookup = %v, %v", ok, err)
	}
}

// The refused write is worth a warning, and a good one is not.
func TestOnlyAFailedUseRecordIsLogged(t *testing.T) {
	pool := dbtest.Open(t)
	var log bytes.Buffer
	tokens := NewAPITokens(pool, slog.New(slog.NewTextHandler(&log, nil)))
	token := issue(t, tokens, dbtest.InsertUser(t, pool, "henry"), "a").Token
	if _, ok, _ := tokens.Lookup(bg, token, testNow); !ok || log.Len() != 0 {
		t.Fatalf("a recorded use logged %q", log.String())
	}
	mustExec(t, pool, "CREATE TRIGGER refuse_update BEFORE UPDATE ON api_tokens BEGIN SELECT RAISE(ABORT, 'refused'); END")
	if _, ok, _ := tokens.Lookup(bg, token, testNow+tokenUseRefreshSeconds); !ok {
		t.Fatal("the token stopped working")
	}
	if !strings.Contains(log.String(), "cannot record the use of an api token") {
		t.Fatalf("log = %q", log.String())
	}
	if strings.Contains(log.String(), token) {
		t.Fatal("the token is in the log")
	}
}

func TestAUserMayHoldExactlyTheMaximumNumberOfTokens(t *testing.T) {
	tokens, pool := newAPITokens(t)
	userID := dbtest.InsertUser(t, pool, "henry")
	other := dbtest.InsertUser(t, pool, "other")
	for range MaxAPITokensPerUser {
		issue(t, tokens, userID, "a")
	}
	if _, ok, err := tokens.Create(bg, NewAPIToken{UserID: userID, Name: "one too many", Now: testNow}); err != nil || ok {
		t.Fatalf("Create past the limit = %v, %v", ok, err)
	}
	if got := countRows(t, pool, "SELECT count(*) FROM api_tokens WHERE user_id = ?1", userID); got != MaxAPITokensPerUser {
		t.Fatalf("%d tokens, want %d", got, MaxAPITokensPerUser)
	}
	issue(t, tokens, other, "someone else's own")
}

func createWithSession(t *testing.T, tokens *APITokens, userID int64, session string) (bool, error) {
	t.Helper()
	_, ok, err := tokens.Create(bg, NewAPIToken{UserID: userID, Name: "a", Now: testNow, SessionToken: session})
	return ok, err
}

// A request accepted before a reset or a disable may reach the insert after it.
func TestNoTokenIsMadeForASessionThatWasEnded(t *testing.T) {
	tokens, pool := newAPITokens(t)
	sessions := NewSessions(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	userID := dbtest.InsertUser(t, pool, "henry")
	session := createToken(t, sessions, startAt(userID, testNow))
	if ok, err := createWithSession(t, tokens, userID, session); err != nil || !ok {
		t.Fatalf("Create with a live session = %v, %v", ok, err)
	}
	mustExec(t, pool, "DELETE FROM sessions WHERE user_id = ?1", userID)
	if _, err := createWithSession(t, tokens, userID, session); !errors.Is(err, ErrCallerGone) {
		t.Fatalf("Create after the session ended: err = %v", err)
	}
	if got := countRows(t, pool, "SELECT count(*) FROM api_tokens WHERE user_id = ?1", userID); got != 1 {
		t.Fatalf("%d tokens, want 1", got)
	}
}

func TestSomeoneElsesSessionDoesNotAllowATokenForTheUser(t *testing.T) {
	tokens, pool := newAPITokens(t)
	sessions := NewSessions(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	userID := dbtest.InsertUser(t, pool, "henry")
	other := dbtest.InsertUser(t, pool, "other")
	session := createToken(t, sessions, startAt(other, testNow))
	if _, err := createWithSession(t, tokens, userID, session); !errors.Is(err, ErrCallerGone) {
		t.Fatalf("err = %v, want ErrCallerGone", err)
	}
}

func TestNoTokenIsMadeForADisabledOrDeletedAccount(t *testing.T) {
	tokens, pool := newAPITokens(t)
	userID := dbtest.InsertUser(t, pool, "henry")
	mustExec(t, pool, "UPDATE users SET disabled_at = 1 WHERE id = ?1", userID)
	if _, err := createWithSession(t, tokens, userID, ""); !errors.Is(err, ErrCallerGone) {
		t.Fatalf("disabled: err = %v", err)
	}
	mustExec(t, pool, "UPDATE users SET disabled_at = NULL WHERE id = ?1", userID)
	if ok, err := createWithSession(t, tokens, userID, ""); err != nil || !ok {
		t.Fatalf("enabled again = %v, %v", ok, err)
	}
	mustExec(t, pool, "DELETE FROM users WHERE id = ?1", userID)
	if _, err := createWithSession(t, tokens, userID, ""); !errors.Is(err, ErrCallerGone) {
		t.Fatalf("deleted: err = %v", err)
	}
}

// A full list is the user's to fix; a gone caller is not, so they must not be mixed up.
func TestAFullListIsNotTakenForAGoneCaller(t *testing.T) {
	tokens, pool := newAPITokens(t)
	sessions := NewSessions(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	userID := dbtest.InsertUser(t, pool, "henry")
	session := createToken(t, sessions, startAt(userID, testNow))
	for range MaxAPITokensPerUser {
		issue(t, tokens, userID, "a")
	}
	if ok, err := createWithSession(t, tokens, userID, session); err != nil || ok {
		t.Fatalf("Create on a full list = %v, %v", ok, err)
	}
}

func TestListShowsOnlyTheUsersTokensNewestFirst(t *testing.T) {
	tokens, pool := newAPITokens(t)
	userID := dbtest.InsertUser(t, pool, "henry")
	other := dbtest.InsertUser(t, pool, "other")
	issue(t, tokens, other, "not mine")
	_, ok, err := tokens.Create(bg, NewAPIToken{UserID: userID, Name: "older", Now: testNow})
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	newer, ok, err := tokens.Create(bg, NewAPIToken{UserID: userID, Name: "newer", Now: testNow + 10})
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, ok, _ := tokens.Lookup(bg, newer.Token, testNow+20); !ok {
		t.Fatal("lookup failed")
	}
	list, err := tokens.ListForUser(bg, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "newer" || list[1].Name != "older" {
		t.Fatalf("list = %+v", list)
	}
	if list[0].ID != newer.ID || list[0].CreatedAt != testNow+10 {
		t.Fatalf("first = %+v", list[0])
	}
	if list[0].LastUsedAt == nil || *list[0].LastUsedAt != testNow+20 || list[1].LastUsedAt != nil {
		t.Fatalf("last used = %v and %v", list[0].LastUsedAt, list[1].LastUsedAt)
	}
}

func TestListOfAUserWithoutTokensIsEmptyNotNil(t *testing.T) {
	tokens, pool := newAPITokens(t)
	list, err := tokens.ListForUser(bg, dbtest.InsertUser(t, pool, "henry"))
	if err != nil || list == nil || len(list) != 0 {
		t.Fatalf("list = %#v, %v", list, err)
	}
}

func TestOnlyTheOwnerEndsAToken(t *testing.T) {
	tokens, pool := newAPITokens(t)
	owner := dbtest.InsertUser(t, pool, "henry")
	stranger := dbtest.InsertUser(t, pool, "other")
	issued := issue(t, tokens, owner, "a")
	if found, err := tokens.RevokeByID(bg, stranger, issued.ID); err != nil || found {
		t.Fatalf("a stranger ended it: %v, %v", found, err)
	}
	if _, ok, _ := tokens.Lookup(bg, issued.Token, testNow); !ok {
		t.Fatal("the token stopped working")
	}
	if found, err := tokens.RevokeByID(bg, owner, issued.ID); err != nil || !found {
		t.Fatalf("the owner could not end it: %v, %v", found, err)
	}
	if _, ok, _ := tokens.Lookup(bg, issued.Token, testNow); ok {
		t.Fatal("an ended token works")
	}
	if found, _ := tokens.RevokeByID(bg, owner, issued.ID); found {
		t.Fatal("an ended token was found again")
	}
}

func TestRevokingAllTokensLeavesOtherUsersAlone(t *testing.T) {
	tokens, pool := newAPITokens(t)
	userID := dbtest.InsertUser(t, pool, "henry")
	other := dbtest.InsertUser(t, pool, "other")
	mine, theirs := issue(t, tokens, userID, "a").Token, issue(t, tokens, other, "b").Token
	issue(t, tokens, userID, "c")
	if err := RevokeAllTokensForUser(bg, pool, userID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := tokens.Lookup(bg, mine, testNow); ok {
		t.Fatal("a token survived")
	}
	if _, ok, _ := tokens.Lookup(bg, theirs, testNow); !ok {
		t.Fatal("another user's token was ended")
	}
}

func TestEveryTokenOperationReportsAClosedDatabase(t *testing.T) {
	tokens, pool := newAPITokens(t)
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		run  func() error
	}{
		{"Create", func() error {
			issued, ok, err := tokens.Create(bg, NewAPIToken{UserID: 1, Name: "a", Now: testNow})
			if ok || issued.Token != "" {
				t.Error("Create made a token on a closed database")
			}
			return err
		}},
		{"Lookup", func() error {
			_, ok, err := tokens.Lookup(bg, "token", testNow)
			if ok {
				t.Error("Lookup accepted a token on a closed database")
			}
			return err
		}},
		{"ListForUser", func() error { _, err := tokens.ListForUser(bg, 1); return err }},
		{"RevokeByID", func() error { _, err := tokens.RevokeByID(bg, 1, "id"); return err }},
		{"RevokeAllTokensForUser", func() error { return RevokeAllTokensForUser(bg, pool, 1) }},
	}
	for _, c := range cases {
		if err := c.run(); err == nil {
			t.Errorf("%s: no error from a closed database", c.name)
		}
	}
}
