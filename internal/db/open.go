// Package db opens the SQLite database and applies its migrations: the SQL files
// in migrations/, with the schema version kept in `PRAGMA user_version`.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// maxOpenConns matches the default pool size of the Rust version.
const maxOpenConns = 10

// Open opens the database file, creating it if needed, and applies pending
// migrations.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	pool, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("cannot open database %s: %w", path, err)
	}
	pool.SetMaxOpenConns(maxOpenConns)
	if err := prepare(ctx, pool); err != nil {
		_ = pool.Close()
		return nil, fmt.Errorf("cannot open database %s: %w", path, err)
	}
	return pool, nil
}

func prepare(ctx context.Context, pool *sql.DB) error {
	if err := pool.PingContext(ctx); err != nil {
		return err
	}
	return migrate(ctx, pool)
}

// dsn asks the driver to run the pragmas on every connection it opens: SQLite
// forgets them per connection, foreign keys included.
func dsn(path string) string {
	pragmas := url.Values{"_pragma": {
		// WAL lets readers run while one writer commits.
		"journal_mode(WAL)",
		// Safe with WAL: a power cut can lose the last commit but never corrupts the file.
		"synchronous(NORMAL)",
		"foreign_keys(1)",
		// Wait for a competing writer instead of failing with "database is locked".
		"busy_timeout(5000)",
	}}
	return "file:" + (&url.URL{Path: path}).EscapedPath() + "?" + pragmas.Encode()
}

// Close folds the write-ahead log into the database file, so the file is
// self-contained after shutdown, then closes the pool.
func Close(db *sql.DB) error {
	_, checkpointErr := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	if checkpointErr != nil {
		checkpointErr = fmt.Errorf("wal checkpoint: %w", checkpointErr)
	}
	return errors.Join(checkpointErr, db.Close())
}
