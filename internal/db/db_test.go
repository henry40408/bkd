package db

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// openTemp opens a fresh database in a directory that goes away with the test.
func openTemp(t *testing.T) *sql.DB {
	t.Helper()
	pool, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

func insertUser(pool *sql.DB, username string) (int64, error) {
	var id int64
	err := pool.QueryRow(
		"INSERT INTO users (username, password_hash, created_at) VALUES (?1, 'x', 0) RETURNING id",
		username).Scan(&id)
	return id, err
}

func TestMigrationsCreateEveryTable(t *testing.T) {
	pool := openTemp(t)
	rows, err := pool.Query(`SELECT name FROM sqlite_master WHERE type = 'table'
		AND name NOT LIKE '\_%' ESCAPE '\' AND name NOT LIKE 'sqlite\_%' ESCAPE '\' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	want := []string{"api_tokens", "bookmark_tags", "bookmarks", "sessions", "users"}
	if !slices.Equal(tables, want) {
		t.Fatalf("tables = %v, want %v", tables, want)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sqlite3")
	for range 2 {
		pool, err := Open(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		if err := Close(pool); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEveryConnectionEnforcesForeignKeys(t *testing.T) {
	pool := openTemp(t)
	ctx := context.Background()
	// Held at the same time, so the pool has to open more than one connection.
	var conns []*sql.Conn
	for range 3 {
		conn, err := pool.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		conns = append(conns, conn)
	}
	for _, conn := range conns {
		var enabled int
		if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled); err != nil {
			t.Fatal(err)
		}
		if enabled != 1 {
			t.Fatalf("foreign_keys = %d", enabled)
		}
	}
}

func TestEveryConnectionGetsTheServerPragmas(t *testing.T) {
	pool := openTemp(t)
	ctx := context.Background()
	want := map[string]string{"journal_mode": "wal", "synchronous": "1", "busy_timeout": "5000"}
	var conns []*sql.Conn
	for range 3 {
		conn, err := pool.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		conns = append(conns, conn)
	}
	for _, conn := range conns {
		for pragma, expected := range want {
			var got string
			if err := conn.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != expected {
				t.Fatalf("PRAGMA %s = %q, want %q", pragma, got, expected)
			}
		}
	}
}

func TestBookmarkOfAMissingUserIsRejected(t *testing.T) {
	pool := openTemp(t)
	_, err := pool.Exec(
		"INSERT INTO bookmarks (user_id, url, title, created_at, updated_at) VALUES (999, 'u', 't', 0, 0)")
	if err == nil {
		t.Fatal("orphan bookmark was accepted")
	}
}

func TestUsernamesAreUnique(t *testing.T) {
	pool := openTemp(t)
	if _, err := insertUser(pool, "henry"); err != nil {
		t.Fatal(err)
	}
	if _, err := insertUser(pool, "henry"); err == nil {
		t.Fatal("duplicate username was accepted")
	}
}

func TestUsernamesFollowTheAllowedPattern(t *testing.T) {
	pool := openTemp(t)
	for _, valid := range []string{"abc", "a.b-c_d", "user01", strings.Repeat("a", 32)} {
		if _, err := insertUser(pool, valid); err != nil {
			t.Errorf("%q: %v", valid, err)
		}
	}
	invalid := []string{"", "ab", "Mei", "mé", "É", "日本語", "a b", "-abc", "abc.", "ab/c", strings.Repeat("a", 33)}
	for _, name := range invalid {
		if _, err := insertUser(pool, name); err == nil {
			t.Errorf("%q should be rejected", name)
		}
	}
}

func TestDeletingAUserDeletesTheirData(t *testing.T) {
	pool := openTemp(t)
	userID, err := insertUser(pool, "henry")
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, pool, "INSERT INTO sessions (token_hash, user_id, created_at, expires_at) VALUES ('h', ?1, 0, 1)", userID)
	var bookmarkID int64
	err = pool.QueryRow(
		"INSERT INTO bookmarks (user_id, url, title, created_at, updated_at) VALUES (?1, 'u', 't', 0, 0) RETURNING id",
		userID).Scan(&bookmarkID)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, pool, "INSERT INTO bookmark_tags (bookmark_id, tag) VALUES (?1, 'rust')", bookmarkID)

	mustExec(t, pool, "DELETE FROM users WHERE id = ?1", userID)

	var remaining int
	err = pool.QueryRow(
		"SELECT (SELECT count(*) FROM sessions) + (SELECT count(*) FROM bookmarks) + (SELECT count(*) FROM bookmark_tags)").
		Scan(&remaining)
	if err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("%d rows survived the user", remaining)
	}
}

func TestEmptyTagsAndBookmarkFieldsAreRejected(t *testing.T) {
	pool := openTemp(t)
	userID, err := insertUser(pool, "henry")
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(
		"INSERT INTO bookmarks (user_id, url, title, created_at, updated_at) VALUES (?1, 'u', '', 0, 0)", userID)
	if err == nil {
		t.Fatal("blank title was accepted")
	}

	var bookmarkID int64
	err = pool.QueryRow(
		"INSERT INTO bookmarks (user_id, url, title, created_at, updated_at) VALUES (?1, 'u', 't', 0, 0) RETURNING id",
		userID).Scan(&bookmarkID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec("INSERT INTO bookmark_tags (bookmark_id, tag) VALUES (?1, '')", bookmarkID); err == nil {
		t.Fatal("blank tag was accepted")
	}
}

func TestColumnsRejectValuesOfTheWrongType(t *testing.T) {
	pool := openTemp(t)
	_, err := pool.Exec(
		"INSERT INTO users (username, password_hash, created_at) VALUES ('henry', 'x', 'yesterday')")
	if err == nil {
		t.Fatal("a text created_at was accepted")
	}
}

func mustExec(t *testing.T, pool *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func TestOpenReportsThePathOfAnUnopenableDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-dir", "test.sqlite3")
	_, err := Open(context.Background(), path)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v, want it to name %s", err, path)
	}
}

func TestOpenAcceptsPathsWithURLSpecialCharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a b?c#d%e.sqlite3")
	pool, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Close(pool); err != nil {
		t.Fatal(err)
	}
	// The file must exist under its literal name, not a truncated one.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Fatalf("directory entries = %v, want only %s", entries, filepath.Base(path))
	}
}

func TestCloseLeavesNoWriteAheadLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sqlite3")
	pool, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := insertUser(pool, "henry"); err != nil {
		t.Fatal(err)
	}
	if err := Close(pool); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(path + "-wal")
	if len(matches) != 0 {
		t.Fatalf("leftover files: %v", matches)
	}
}

func TestCloseReportsACheckpointFailure(t *testing.T) {
	pool := openTemp(t)
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	err := Close(pool)
	if err == nil || !strings.Contains(err.Error(), "wal checkpoint") {
		t.Fatalf("err = %v", err)
	}
}
