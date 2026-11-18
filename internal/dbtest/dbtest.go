// Package dbtest gives tests a throwaway database that has every migration applied.
package dbtest

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/henry40408/bkd/internal/db"
)

// Open opens a fresh database in a directory that goes away with the test.
func Open(t testing.TB) *sql.DB {
	t.Helper()
	pool, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "test.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

// InsertUser adds an active, non-admin user whose password hash is a placeholder.
func InsertUser(t testing.TB, pool *sql.DB, username string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(
		"INSERT INTO users (username, password_hash, created_at) VALUES (?1, 'x', 0) RETURNING id",
		username).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
