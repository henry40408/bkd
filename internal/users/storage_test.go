package users

import (
	"database/sql"
	"io"
	"log/slog"
	"testing"

	"github.com/henry40408/bkd/internal/dbtest"
)

// stored is the SQLite storage class and value of a column, so the test sees
// the bytes on disk rather than what the driver converts them to.
func stored(t *testing.T, pool *sql.DB, column string, id int64) string {
	t.Helper()
	var kind string
	var value sql.NullInt64
	err := pool.QueryRow("SELECT typeof("+column+"), "+column+" FROM users WHERE id = ?1", id).Scan(&kind, &value)
	if err != nil {
		t.Fatal(err)
	}
	if !value.Valid {
		return kind
	}
	return kind + ":" + string(rune('0'+value.Int64))
}

func TestFlagsAndTimesAreStoredAsIntegersOrNull(t *testing.T) {
	pool := dbtest.Open(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	yes, no := true, false

	// The proxy names no role: the first account becomes admin, the next does not.
	first, _, err := AdoptForwarded(bg, pool, logger, ForwardedAccount{Username: "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := AdoptForwarded(bg, pool, logger, ForwardedAccount{Username: "second"})
	if err != nil {
		t.Fatal(err)
	}
	// The proxy names a role.
	third, _, err := AdoptForwarded(bg, pool, logger, ForwardedAccount{Username: "third", IsAdmin: &yes})
	if err != nil {
		t.Fatal(err)
	}
	fourth, _, err := AdoptForwarded(bg, pool, logger, ForwardedAccount{Username: "fourth", IsAdmin: &no})
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]string{first.ID: "integer:1", second.ID: "integer:0", third.ID: "integer:1", fourth.ID: "integer:0"} {
		if got := stored(t, pool, "is_admin", id); got != want {
			t.Errorf("forwarded user %d is_admin = %s, want %s", id, got, want)
		}
		if got := stored(t, pool, "disabled_at", id); got != "null" {
			t.Errorf("forwarded user %d disabled_at = %s, want null", id, got)
		}
	}

	// Role changes and the link check setting.
	if _, err := SetAdmin(bg, pool, first.ID, fourth.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := stored(t, pool, "is_admin", fourth.ID); got != "integer:1" {
		t.Errorf("promoted is_admin = %s", got)
	}
	if _, err := SetAdmin(bg, pool, first.ID, fourth.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := stored(t, pool, "is_admin", fourth.ID); got != "integer:0" {
		t.Errorf("demoted is_admin = %s", got)
	}
	if err := SetLinkCheckEnabled(bg, pool, second.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := stored(t, pool, "link_check_enabled", second.ID); got != "integer:1" {
		t.Errorf("link_check_enabled on = %s", got)
	}
	if err := SetLinkCheckEnabled(bg, pool, second.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := stored(t, pool, "link_check_enabled", second.ID); got != "integer:0" {
		t.Errorf("link_check_enabled off = %s", got)
	}

	// Disabling stores the time, enabling stores NULL.
	disabledAt := int64(7)
	if _, err := SetDisabled(bg, pool, first.ID, fourth.ID, &disabledAt); err != nil {
		t.Fatal(err)
	}
	if got := stored(t, pool, "disabled_at", fourth.ID); got != "integer:7" {
		t.Errorf("disabled_at = %s", got)
	}
	if _, err := SetDisabled(bg, pool, first.ID, fourth.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got := stored(t, pool, "disabled_at", fourth.ID); got != "null" {
		t.Errorf("enabled disabled_at = %s", got)
	}

	// An account made by an admin.
	created, _, err := Create(bg, pool, first.ID, NewAccount{Username: "made", PasswordHash: "hash", IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := stored(t, pool, "is_admin", created.ID); got != "integer:1" {
		t.Errorf("created admin is_admin = %s", got)
	}
}
