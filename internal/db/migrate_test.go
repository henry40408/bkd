package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

// rustMigration is one row of `_sqlx_migrations` as the Rust version wrote it.
type rustMigration struct {
	Version     int64  `json:"version"`
	Description string `json:"description"`
	Success     int    `json:"success"`
	Checksum    string `json:"checksum"`
}

func readRustMigrations(t *testing.T) []rustMigration {
	t.Helper()
	raw, err := os.ReadFile("testdata/rust-migrations.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []rustMigration
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func goMigrations(t *testing.T, pool *sql.DB) []rustMigration {
	t.Helper()
	rows, err := pool.Query("SELECT version, description, success, hex(checksum) FROM _sqlx_migrations ORDER BY version")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []rustMigration
	for rows.Next() {
		var m rustMigration
		if err := rows.Scan(&m.Version, &m.Description, &m.Success, &m.Checksum); err != nil {
			t.Fatal(err)
		}
		got = append(got, m)
	}
	return got
}

// goOnlyMigrations is how many migrations come after the last one the Rust
// server knew. The fixtures in testdata stay as Rust wrote them, so add one
// here, and its schema change to withGoOnlyChanges, with each new migration.
const goOnlyMigrations = 2

func TestNewDatabaseRecordsTheRustMigrationsThenTheGoOnlyOnes(t *testing.T) {
	got := goMigrations(t, openTemp(t))
	want := readRustMigrations(t)
	if len(got) != len(want)+goOnlyMigrations {
		t.Fatalf("%d migrations recorded, want %d from Rust and %d for Go only", len(got), len(want), goOnlyMigrations)
	}
	if !reflect.DeepEqual(got[:len(want)], want) {
		t.Fatalf("_sqlx_migrations differs from the Rust fixture\n got: %+v\nwant: %+v", got[:len(want)], want)
	}
}

// withGoOnlyChanges applies what the Go-only migrations do to the schema the
// Rust server made. SQLite writes an added column before the closing bracket
// of the table.
func withGoOnlyChanges(rust map[string]string) map[string]string {
	rust["users"] = strings.TrimSuffix(rust["users"], ") STRICT") +
		", link_check_user_agent TEXT NOT NULL DEFAULT '') STRICT"
	rust["bookmarks"] = strings.TrimSuffix(rust["bookmarks"], ") STRICT") +
		", site_title TEXT, site_description TEXT) STRICT"
	return rust
}

func TestNewDatabaseHasTheRustSchemaAndTheGoOnlyChanges(t *testing.T) {
	pool := openTemp(t)
	want := withGoOnlyChanges(readRustSchema(t))
	rows, err := pool.Query("SELECT name, sql FROM sqlite_master WHERE sql IS NOT NULL ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	got := map[string]string{}
	for rows.Next() {
		var name, statement string
		if err := rows.Scan(&name, &statement); err != nil {
			t.Fatal(err)
		}
		got[name] = statement
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("schema differs from the Rust fixture\n got: %q\nwant: %q", got, want)
	}
}

// readRustSchema parses rust-schema.txt: entries are `name: <sql>`, where the
// sql runs until the next line that starts a new entry.
func readRustSchema(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile("testdata/rust-schema.txt")
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"_sqlx_migrations", "bookmark_tags", "bookmarks", "bookmarks_user_created", "sessions", "sessions_user_id", "users"}
	schema := map[string]string{}
	var current string
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		if name, rest, ok := entryStart(line, names); ok {
			current = name
			schema[name] = rest
			continue
		}
		schema[current] += "\n" + line
	}
	return schema
}

func entryStart(line string, names []string) (name, rest string, ok bool) {
	for _, candidate := range names {
		if rest, found := strings.CutPrefix(line, candidate+": "); found {
			return candidate, rest, true
		}
	}
	return "", "", false
}

func TestOpenKeepsTheMigrationRowsOfADatabaseCreatedByRust(t *testing.T) {
	path := copyRustDatabase(t)
	before := rawMigrationRows(t, path)

	pool, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool.Close() }()

	after := rawMigrationRows(t, path)
	if got := len(before); got != len(readRustMigrations(t)) {
		t.Fatalf("fixture has %d migration rows", got)
	}
	if len(after) != len(before)+goOnlyMigrations {
		t.Fatalf("%d migration rows after opening, want the %d from Rust and %d for Go only", len(after), len(before), goOnlyMigrations)
	}
	if !reflect.DeepEqual(before, after[:len(before)]) {
		t.Fatalf("migration rows changed\nbefore: %v\n after: %v", before, after[:len(before)])
	}
}

func TestDatabaseCreatedByRustIsReadableAndWritable(t *testing.T) {
	pool, err := Open(context.Background(), copyRustDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool.Close() }()

	var admin string
	if err := pool.QueryRow("SELECT username FROM users WHERE is_admin = 1").Scan(&admin); err != nil {
		t.Fatal(err)
	}
	if admin != "admin" {
		t.Fatalf("admin = %q", admin)
	}
	var bookmarks, tags, sessions int
	err = pool.QueryRow(`SELECT (SELECT count(*) FROM bookmarks), (SELECT count(*) FROM bookmark_tags),
		(SELECT count(*) FROM sessions)`).Scan(&bookmarks, &tags, &sessions)
	if err != nil {
		t.Fatal(err)
	}
	if bookmarks != 3 || tags != 3 || sessions != 1 {
		t.Fatalf("bookmarks=%d tags=%d sessions=%d", bookmarks, tags, sessions)
	}
	if _, err := insertUser(pool, "newcomer"); err != nil {
		t.Fatal(err)
	}
}

func copyRustDatabase(t *testing.T) string {
	t.Helper()
	source, err := os.Open("testdata/rust-created.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Close() }()
	path := filepath.Join(t.TempDir(), "rust-created.sqlite3")
	target, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = target.Close() }()
	if _, err := io.Copy(target, source); err != nil {
		t.Fatal(err)
	}
	return path
}

// rawMigrationRows reads every column, installed_on and execution_time
// included, through a plain connection that runs no migration.
func rawMigrationRows(t *testing.T, path string) [][]any {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	rows, err := raw.Query("SELECT version, description, CAST(installed_on AS TEXT), success, hex(checksum), execution_time FROM _sqlx_migrations ORDER BY version")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var all [][]any
	for rows.Next() {
		var version, success, executionTime int64
		var description, installedOn, checksum string
		if err := rows.Scan(&version, &description, &installedOn, &success, &checksum, &executionTime); err != nil {
			t.Fatal(err)
		}
		all = append(all, []any{version, description, installedOn, success, checksum, executionTime})
	}
	return all
}

func TestNewDatabaseRecordsInstalledOnAndExecutionTime(t *testing.T) {
	pool := openTemp(t)
	rows, err := pool.Query("SELECT CAST(installed_on AS TEXT), typeof(installed_on), execution_time FROM _sqlx_migrations")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	count := 0
	for rows.Next() {
		var installedOn, kind string
		var executionTime int64
		if err := rows.Scan(&installedOn, &kind, &executionTime); err != nil {
			t.Fatal(err)
		}
		// sqlx stores CURRENT_TIMESTAMP text, "YYYY-MM-DD HH:MM:SS".
		if kind != "text" || len(installedOn) != len("2006-01-02 15:04:05") {
			t.Errorf("installed_on = %q (%s)", installedOn, kind)
		}
		if executionTime <= 0 {
			t.Errorf("execution_time = %d, want the measured nanoseconds", executionTime)
		}
		count++
	}
	if want := len(readRustMigrations(t)) + goOnlyMigrations; count != want {
		t.Fatalf("%d migration rows, want %d", count, want)
	}
}

func TestOpenRefusesATamperedChecksum(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sqlite3")
	pool, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, pool, "UPDATE _sqlx_migrations SET checksum = x'00' WHERE version = 2")
	_ = pool.Close()

	_, err = Open(context.Background(), path)
	if err == nil || !strings.Contains(err.Error(), "migration 2") || !strings.Contains(err.Error(), "modified") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenRefusesAMigrationThatFailedHalfway(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sqlite3")
	pool, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, pool, "UPDATE _sqlx_migrations SET success = 0 WHERE version = 3")
	_ = pool.Close()

	_, err = Open(context.Background(), path)
	if err == nil || !strings.Contains(err.Error(), "migration 3") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenRefusesAMigrationThisVersionDoesNotKnow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sqlite3")
	pool, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, pool,
		"INSERT INTO _sqlx_migrations (version, description, success, checksum, execution_time) VALUES (99, 'newer', 1, x'01', 1)")
	_ = pool.Close()

	_, err = Open(context.Background(), path)
	if err == nil || !strings.Contains(err.Error(), "migration 99") || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenIgnoresADifferentDescription(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sqlite3")
	pool, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, pool, "UPDATE _sqlx_migrations SET description = 'renamed' WHERE version = 1")
	_ = pool.Close()

	pool, err = Open(context.Background(), path)
	if err != nil {
		t.Fatalf("sqlx never compares descriptions: %v", err)
	}
	_ = pool.Close()
}

func mustSub(t *testing.T) fs.FS {
	t.Helper()
	files, err := fs.Sub(embeddedMigrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestFailedMigrationLeavesNothingBehind(t *testing.T) {
	pool := openTemp(t)
	shipped, err := loadMigrations(mustSub(t))
	if err != nil {
		t.Fatal(err)
	}
	broken := append(shipped, migration{
		version:     100,
		description: "broken",
		sql:         "CREATE TABLE half_done (id INTEGER); INSERT INTO no_such_table VALUES (1);",
		checksum:    []byte{1},
	})

	err = applyMigrations(context.Background(), pool, broken)
	if err == nil || !strings.Contains(err.Error(), "migration 100") {
		t.Fatalf("err = %v", err)
	}
	var tables, rows int
	err = pool.QueryRow(`SELECT (SELECT count(*) FROM sqlite_master WHERE name = 'half_done'),
		(SELECT count(*) FROM _sqlx_migrations WHERE version = 100)`).Scan(&tables, &rows)
	if err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatal("the half-applied table survived")
	}
	if rows != 0 {
		t.Fatal("a failed migration was recorded")
	}
}

func TestLoadMigrationsNamesAndOrdersByVersion(t *testing.T) {
	fsys := fstest.MapFS{
		"0010_late_one.sql": {Data: []byte("SELECT 10;")},
		"0002_early.sql":    {Data: []byte("SELECT 2;")},
		"notes.txt":         {Data: []byte("ignored")},
	}
	got, err := loadMigrations(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].version != 2 || got[1].version != 10 {
		t.Fatalf("migrations = %+v", got)
	}
	if got[1].description != "late one" {
		t.Fatalf("description = %q", got[1].description)
	}
}

func TestLoadMigrationsRejectsBadNames(t *testing.T) {
	for _, name := range []string{"init.sql", "x_init.sql", "0001.sql"} {
		fsys := fstest.MapFS{name: {Data: []byte("SELECT 1;")}}
		if _, err := loadMigrations(fsys); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestLoadMigrationsRejectsDuplicateVersions(t *testing.T) {
	fsys := fstest.MapFS{
		"0001_a.sql":  {Data: []byte("SELECT 1;")},
		"00001_b.sql": {Data: []byte("SELECT 1;")},
	}
	if _, err := loadMigrations(fsys); err == nil {
		t.Fatal("two files with version 1 were accepted")
	}
}
