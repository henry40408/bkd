package auth

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/henry40408/bkd/internal/dbtest"
)

func TestARefreshFailureIsLoggedAndASuccessIsNot(t *testing.T) {
	pool := dbtest.Open(t)
	var log bytes.Buffer
	sessions := NewSessions(pool, slog.New(slog.NewTextHandler(&log, nil)))

	sessions.refreshIdleClock(bg, "no such session", testNow)
	if log.Len() != 0 {
		t.Fatalf("a refresh that worked was logged: %s", &log)
	}

	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	sessions.refreshIdleClock(bg, "no such session", testNow)
	if !strings.Contains(log.String(), "cannot refresh the session idle clock") {
		t.Fatalf("a failed refresh was not logged: %q", log.String())
	}
}
