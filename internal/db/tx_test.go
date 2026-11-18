package db

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"
)

func countUsers(t *testing.T, pool *sql.DB) int {
	t.Helper()
	var n int
	if err := pool.QueryRow("SELECT count(*) FROM users").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func insertUserOn(ctx context.Context, conn *sql.Conn, username string) error {
	_, err := conn.ExecContext(ctx,
		"INSERT INTO users (username, password_hash, created_at) VALUES (?1, 'x', 0)", username)
	return err
}

func TestWithImmediateTxCommitsWhenTheFunctionSucceeds(t *testing.T) {
	pool := openTemp(t)
	ctx := context.Background()
	err := WithImmediateTx(ctx, pool, func(conn *sql.Conn) error {
		return insertUserOn(ctx, conn, "henry")
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := countUsers(t, pool); got != 1 {
		t.Fatalf("users = %d", got)
	}
}

func TestWithImmediateTxRollsBackWhenTheFunctionFails(t *testing.T) {
	pool := openTemp(t)
	ctx := context.Background()
	boom := errors.New("boom")
	err := WithImmediateTx(ctx, pool, func(conn *sql.Conn) error {
		if err := insertUserOn(ctx, conn, "henry"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if got := countUsers(t, pool); got != 0 {
		t.Fatalf("users = %d, want the insert rolled back", got)
	}
}

func TestWithImmediateTxRollsBackAndReleasesTheLockOnPanic(t *testing.T) {
	pool := openTemp(t)
	ctx := context.Background()
	func() {
		defer func() { _ = recover() }()
		_ = WithImmediateTx(ctx, pool, func(conn *sql.Conn) error {
			_ = insertUserOn(ctx, conn, "henry")
			panic("boom")
		})
	}()
	if got := countUsers(t, pool); got != 0 {
		t.Fatalf("users = %d, want the insert rolled back", got)
	}
	// The next writer must not wait on a lock the panic left behind.
	err := WithImmediateTx(ctx, pool, func(conn *sql.Conn) error {
		return insertUserOn(ctx, conn, "henry")
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWithImmediateTxRollsBackWhenTheContextIsCancelled(t *testing.T) {
	pool := openTemp(t)
	ctx, cancel := context.WithCancel(context.Background())
	err := WithImmediateTx(ctx, pool, func(conn *sql.Conn) error {
		if err := insertUserOn(ctx, conn, "henry"); err != nil {
			return err
		}
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if got := countUsers(t, pool); got != 0 {
		t.Fatalf("users = %d", got)
	}
}

func TestWithImmediateTxFailsFastOnACancelledContext(t *testing.T) {
	pool := openTemp(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := WithImmediateTx(ctx, pool, func(*sql.Conn) error {
		called = true
		return nil
	})
	if err == nil || called {
		t.Fatalf("err = %v, called = %v", err, called)
	}
}

func TestConcurrentImmediateTransactionsWaitInsteadOfFailingWithBusy(t *testing.T) {
	pool := openTemp(t)
	ctx := context.Background()
	const writers = 8
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- WithImmediateTx(ctx, pool, func(conn *sql.Conn) error {
				if err := insertUserOn(ctx, conn, "user"+string(rune('a'+i))+"x"); err != nil {
					return err
				}
				// Holds the write lock long enough for the others to queue on it.
				time.Sleep(30 * time.Millisecond)
				return nil
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := countUsers(t, pool); got != writers {
		t.Fatalf("users = %d, want %d", got, writers)
	}
}
