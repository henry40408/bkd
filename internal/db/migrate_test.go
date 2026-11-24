package db

import (
	"context"
	"database/sql"
	"io/fs"
	"maps"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

// rustMigration is one row of `_sqlx_migrations` as the Rust version wrote it.
type rustMigration struct {
	Version     int64
	Description string
	Success     int
	Checksum    string
}

// rustMigrations are the rows sqlx wrote for the five migrations the Rust
// server knew. They are never regenerated from Go output.
var rustMigrations = []rustMigration{
	{1, "init", 1, "56F9699F6985AA320F9F8B245DDF72D6B08F0C2E97CD74F293732E958A0FD37A1A7C176D16A664DB3696D8820B6175B4"},
	{2, "link check", 1, "3203BAAF27F593753D9E1C7938330AB4AE71183D45038856816E721490A909BF60731D1A0AC5A9F2E1D75C1DAC1DF6E1"},
	{3, "session idle", 1, "71187FF06C55D4E2C78E6EB5349E93890701A6D0738A1203BE131CC08D213042F0605ABB80CE4B0998A2E0058D214FD3"},
	{4, "session details", 1, "56B3BA3B553F0C30F78F72EED3A7615D601B77517F9C55E161549094CDB46230E7FBE570ABBF2F4746C9B133CF6FDC60"},
	{5, "link check setting", 1, "9AF56C58B0229FC62786BF2995CC07D3BCE7CDC278EAB5AE12EC7CEDC30F32E0522A420920CE05C32F831E032DB0E95F"},
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
// server knew. rustMigrations and rustSchema stay as Rust wrote them, so add one
// here, and its schema change to withGoOnlyChanges, with each new migration.
const goOnlyMigrations = 2

func TestNewDatabaseRecordsTheRustMigrationsThenTheGoOnlyOnes(t *testing.T) {
	got := goMigrations(t, openTemp(t))
	want := rustMigrations
	if len(got) != len(want)+goOnlyMigrations {
		t.Fatalf("%d migrations recorded, want %d from Rust and %d for Go only", len(got), len(want), goOnlyMigrations)
	}
	if !reflect.DeepEqual(got[:len(want)], want) {
		t.Fatalf("_sqlx_migrations differs from the Rust records\n got: %+v\nwant: %+v", got[:len(want)], want)
	}
}

// withGoOnlyChanges applies what the Go-only migrations do to the schema the
// Rust server made. SQLite writes an added column before the closing bracket
// of the table.
func withGoOnlyChanges(rust map[string]string) map[string]string {
	changed := maps.Clone(rust)
	changed["users"] = strings.TrimSuffix(changed["users"], ") STRICT") +
		", link_check_user_agent TEXT NOT NULL DEFAULT '') STRICT"
	changed["bookmarks"] = strings.TrimSuffix(changed["bookmarks"], ") STRICT") +
		", site_title TEXT, site_description TEXT) STRICT"
	return changed
}

func TestNewDatabaseHasTheRustSchemaAndTheGoOnlyChanges(t *testing.T) {
	pool := openTemp(t)
	want := withGoOnlyChanges(rustSchema)
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
		t.Fatalf("schema differs from the Rust schema\n got: %q\nwant: %q", got, want)
	}
}

// rustSchema is what the Rust server's five migrations left in sqlite_master,
// byte for byte: SQLite writes a column added later before the closing bracket
// of the table. It is never regenerated from Go output.
var rustSchema = map[string]string{
	"_sqlx_migrations": `CREATE TABLE _sqlx_migrations (
    version BIGINT PRIMARY KEY,
    description TEXT NOT NULL,
    installed_on TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    success BOOLEAN NOT NULL,
    checksum BLOB NOT NULL,
    execution_time BIGINT NOT NULL
)`,
	"bookmark_tags": `CREATE TABLE bookmark_tags (
    bookmark_id INTEGER NOT NULL REFERENCES bookmarks (id) ON DELETE CASCADE,
    -- The application trims and lowercases tags; SQLite cannot fold non-ASCII case.
    tag         TEXT    NOT NULL CHECK (tag <> ''),
    PRIMARY KEY (bookmark_id, tag)
) STRICT`,
	"bookmarks": `CREATE TABLE bookmarks (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    url        TEXT    NOT NULL CHECK (url <> ''),
    title      TEXT    NOT NULL CHECK (title <> ''),
    note       TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
, link_status TEXT
    CHECK (link_status IN ('ok', 'redirected', 'broken', 'unknown')), link_checked_at INTEGER, link_detail TEXT) STRICT`,
	"bookmarks_user_created": `CREATE INDEX bookmarks_user_created ON bookmarks (user_id, created_at DESC)`,
	"sessions": `CREATE TABLE sessions (
    token_hash TEXT    PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
, last_used_at INTEGER NOT NULL DEFAULT 0, ip TEXT NOT NULL DEFAULT '', user_agent TEXT NOT NULL DEFAULT '', confirmed_at INTEGER NOT NULL DEFAULT 0) STRICT`,
	"sessions_user_id": `CREATE INDEX sessions_user_id ON sessions (user_id)`,
	"users": `CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    -- ASCII only and lowercase: SQLite folds case for ASCII alone, so this keeps the
    -- UNIQUE constraint case-insensitive on every database.
    username      TEXT    NOT NULL UNIQUE CHECK (
        length(username) BETWEEN 3 AND 32
        AND username GLOB '[a-z0-9]*[a-z0-9]'
        AND username NOT GLOB '*[^a-z0-9._-]*'
    ),
    password_hash TEXT    NOT NULL,
    is_admin      INTEGER NOT NULL DEFAULT 0 CHECK (is_admin IN (0, 1)),
    created_at    INTEGER NOT NULL,
    -- NULL means active.
    disabled_at   INTEGER
, link_check_enabled INTEGER NOT NULL DEFAULT 0
    CHECK (link_check_enabled IN (0, 1))) STRICT`,
}

func TestOpenKeepsTheMigrationRowsOfADatabaseCreatedByRust(t *testing.T) {
	path := createRustDatabase(t)
	before := rawMigrationRows(t, path)

	pool, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool.Close() }()

	after := rawMigrationRows(t, path)
	if got := len(before); got != len(rustMigrations) {
		t.Fatalf("the Rust database has %d migration rows", got)
	}
	if len(after) != len(before)+goOnlyMigrations {
		t.Fatalf("%d migration rows after opening, want the %d from Rust and %d for Go only", len(after), len(before), goOnlyMigrations)
	}
	if !reflect.DeepEqual(before, after[:len(before)]) {
		t.Fatalf("migration rows changed\nbefore: %v\n after: %v", before, after[:len(before)])
	}
}

func TestDatabaseCreatedByRustIsReadableAndWritable(t *testing.T) {
	pool, err := Open(context.Background(), createRustDatabase(t))
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

// rustRows is what a database the Rust server wrote held: its five migration
// records, with the installed_on and execution_time values sqlx recorded, and a
// little data in every table.
const rustRows = `
INSERT INTO _sqlx_migrations VALUES(1,'init','2026-11-17 07:01:38',1,x'56f9699f6985aa320f9f8b245ddf72d6b08f0c2e97cd74f293732e958a0fd37a1a7c176d16a664db3696d8820b6175b4',399209);
INSERT INTO _sqlx_migrations VALUES(2,'link check','2026-11-17 07:01:38',1,x'3203baaf27f593753d9e1c7938330ab4ae71183d45038856816e721490a909bf60731d1a0ac5a9f2e1d75c1dac1df6e1',437542);
INSERT INTO _sqlx_migrations VALUES(3,'session idle','2026-11-17 07:01:38',1,x'71187ff06c55d4e2c78e6eb5349e93890701a6d0738a1203be131cc08d213042f0605abb80ce4b0998a2e0058d214fd3',232833);
INSERT INTO _sqlx_migrations VALUES(4,'session details','2026-11-17 07:01:38',1,x'56b3ba3b553f0c30f78f72eed3a7615d601b77517f9c55e161549094cdb46230e7fbe570abbf2f4746c9b133cf6fdc60',393542);
INSERT INTO _sqlx_migrations VALUES(5,'link check setting','2026-11-17 07:01:38',1,x'9af56c58b0229fc62786bf2995cc07d3bce7cdc278eab5ae12ec7cedc30f32e0522a420920ce05c32f831e032db0e95f',171375);
INSERT INTO users VALUES(1,'admin','hash-admin',1,1577836801,NULL,0);
INSERT INTO users VALUES(2,'member','hash-member',0,1577836802,NULL,0);
INSERT INTO users VALUES(3,'gone','hash-gone',0,1577836803,1794898899,0);
INSERT INTO sessions VALUES('e6b7a37d818a29173129528fad16cc366390377797e7ed3417e4b655f5b9c33c',1,1794898898,1797490898,1794898898,'127.0.0.1','bkd-fixture',1794898898);
INSERT INTO bookmarks VALUES(1,1,'https://example.com/one','One','note One',1577836801,1577836801,'ok',1577900000,NULL);
INSERT INTO bookmarks VALUES(2,1,'https://example.org/two','Two','note Two',1577836802,1577836802,'broken',1577900000,'HTTP 404');
INSERT INTO bookmarks VALUES(3,1,'https://example.net/three','Three','note Three',1577836803,1577836803,NULL,NULL,NULL);
INSERT INTO bookmark_tags VALUES(1,'alpha');
INSERT INTO bookmark_tags VALUES(1,'beta');
INSERT INTO bookmark_tags VALUES(2,'beta');
`

// createRustDatabase writes a database in the state the Rust server left it:
// the schema in rustSchema and the rows in rustRows, and nothing a Go migration
// adds.
func createRustDatabase(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rust-created.sqlite3")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	creationOrder := []string{
		"_sqlx_migrations", "users", "sessions", "bookmarks", "bookmark_tags",
		"sessions_user_id", "bookmarks_user_created",
	}
	for _, name := range creationOrder {
		mustExec(t, raw, rustSchema[name])
	}
	mustExec(t, raw, rustRows)
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
	if want := len(rustMigrations) + goOnlyMigrations; count != want {
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
