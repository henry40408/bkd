package users

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/henry40408/bkd/internal/dbtest"
)

func TestForwardedAccountsLogTheirCreationAndRoleChanges(t *testing.T) {
	pool := dbtest.Open(t)
	var log bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&log, nil))
	admin, plain := true, false

	if _, _, err := AdoptForwarded(bg, pool, logger, ForwardedAccount{Username: "alice", IsAdmin: &plain, Now: 1}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "account created from forward auth") {
		t.Fatalf("creation was not logged: %s", &log)
	}
	if strings.Contains(log.String(), "role set by forward auth") {
		t.Fatalf("a role that was already right was logged as changed: %s", &log)
	}

	adopted, _, err := AdoptForwarded(bg, pool, logger, ForwardedAccount{Username: "alice", IsAdmin: &admin, Now: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !adopted.IsAdmin || !strings.Contains(log.String(), "role set by forward auth") {
		t.Fatalf("promotion = %v, log: %s", adopted.IsAdmin, &log)
	}
}
