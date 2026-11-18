package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
)

// WithImmediateTx runs fn inside a BEGIN IMMEDIATE transaction and commits when
// fn returns nil. The write lock is taken up front, so two writers queue on
// busy_timeout instead of failing halfway when a read upgrades to a write.
//
// fn must run its statements on the given connection and must not start its
// own transaction.
func WithImmediateTx(ctx context.Context, db *sql.DB, fn func(conn *sql.Conn) error) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	// Runs for errors, early returns and panics alike.
	defer func() {
		if !committed {
			rollback(conn)
		}
	}()

	if err := fn(conn); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	committed = true
	return nil
}

// rollback must work after the request was cancelled, so it ignores ctx. A
// connection that cannot roll back still holds the write lock; the pool must
// drop it instead of handing it to the next caller.
func rollback(conn *sql.Conn) {
	if _, err := conn.ExecContext(context.Background(), "ROLLBACK"); err == nil {
		return
	}
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
}
