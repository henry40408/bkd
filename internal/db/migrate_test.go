package db

import (
	"context"
	"database/sql"
	"io/fs"
	"maps"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// baselineVersion is the schema version the baseline file creates. It stays the
// same when later migrations are added.
const baselineVersion = 9

// sentinel starts the one row a new database holds in _sqlx_migrations: version,
// success and the type of the checksum. Older bkd releases and the Rust server
// refuse a database that records a migration they do not have, so it must be out
// of their range, successful and hold a blob.
var sentinel = []any{int64(99999999999999), int64(1), "blob"}

func recordRows(t *testing.T, pool *sql.DB) [][]any {
	t.Helper()
	rows, err := pool.Query(`SELECT version, success, typeof(checksum), description,
		CAST(installed_on AS TEXT), hex(checksum), execution_time FROM _sqlx_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var all [][]any
	for rows.Next() {
		var version, success, executionTime int64
		var kind, description, installedOn, checksum string
		if err := rows.Scan(&version, &success, &kind, &description, &installedOn, &checksum, &executionTime); err != nil {
			t.Fatal(err)
		}
		all = append(all, []any{version, success, kind, description, installedOn, checksum, executionTime})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return all
}

func schemaVersion(t *testing.T, pool *sql.DB) int64 {
	t.Helper()
	version, err := userVersion(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

var (
	sqlComment        = regexp.MustCompile(`--[^\n]*`)
	sqlSpace          = regexp.MustCompile(`\s+`)
	sqlSpaceAtBracket = regexp.MustCompile(` ?([(),]) ?`)
)

// schemaOf maps each object to its statement without comments and layout, so a
// schema written in one piece compares with one that ALTER TABLE grew. Column
// order, types and constraints all still count.
func schemaOf(t *testing.T, pool *sql.DB) map[string]string {
	t.Helper()
	rows, err := pool.Query("SELECT name, sql FROM sqlite_master WHERE sql IS NOT NULL ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	schema := map[string]string{}
	for rows.Next() {
		var name, statement string
		if err := rows.Scan(&name, &statement); err != nil {
			t.Fatal(err)
		}
		statement = sqlSpace.ReplaceAllString(sqlComment.ReplaceAllString(statement, ""), " ")
		schema[name] = sqlSpaceAtBracket.ReplaceAllString(statement, "$1")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return schema
}

// openRaw opens a database file without running any migration.
func openRaw(t *testing.T, path string) *sql.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	return raw
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
// the schema in rustSchema and the rows in rustRows.
func createRustDatabase(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rust-created.sqlite3")
	raw := openRaw(t, path)
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

// release05Changes is what bkd 0.5 did to a Rust database: migrations 6 to 9 as
// those releases ran them, and their records. Opening reads the description of
// version 9 but never the checksums, so placeholders stand in for those.
const release05Changes = `
ALTER TABLE users ADD COLUMN link_check_user_agent TEXT NOT NULL DEFAULT '';
ALTER TABLE bookmarks ADD COLUMN site_title TEXT;
ALTER TABLE bookmarks ADD COLUMN site_description TEXT;
CREATE TABLE api_tokens (
    token_hash   TEXT    PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT    NOT NULL CHECK (name <> ''),
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER
) STRICT;
CREATE INDEX api_tokens_user_id ON api_tokens (user_id);
CREATE INDEX bookmarks_user_url ON bookmarks (user_id, url);
INSERT INTO _sqlx_migrations VALUES(6,'link check user agent','2026-11-20 07:01:38',1,x'06',1);
INSERT INTO _sqlx_migrations VALUES(7,'site metadata','2026-11-20 07:01:38',1,x'07',1);
INSERT INTO _sqlx_migrations VALUES(8,'api tokens','2026-11-20 07:01:38',1,x'08',1);
INSERT INTO _sqlx_migrations VALUES(9,'bookmark url index','2026-11-20 07:01:38',1,x'09',1);
`

// createRelease05Database writes a database as bkd 0.5 left it.
func createRelease05Database(t *testing.T) string {
	t.Helper()
	path := createRustDatabase(t)
	mustExec(t, openRaw(t, path), release05Changes)
	return path
}

func createNewDatabase(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "new.sqlite3")
	pool, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Close(pool); err != nil {
		t.Fatal(err)
	}
	return path
}

func shippedMigrations(t *testing.T) []migration {
	t.Helper()
	files, err := fs.Sub(embeddedMigrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	shipped, err := loadMigrations(files)
	if err != nil {
		t.Fatal(err)
	}
	return shipped
}

// onlyBaseline keeps a test about the baseline true after later migrations ship.
func onlyBaseline(t *testing.T) []migration {
	t.Helper()
	return shippedMigrations(t)[:1]
}

func applyTo(t *testing.T, pool *sql.DB, migrations []migration) {
	t.Helper()
	if err := applyMigrations(context.Background(), pool, migrations); err != nil {
		t.Fatal(err)
	}
}

func newRawDatabase(t *testing.T) *sql.DB {
	t.Helper()
	return openRaw(t, filepath.Join(t.TempDir(), "raw.sqlite3"))
}

func TestNewDatabaseIsAtTheLatestVersionAndRecordsOnlyTheSentinel(t *testing.T) {
	pool := openTemp(t)
	shipped := shippedMigrations(t)
	if got, want := schemaVersion(t, pool), shipped[len(shipped)-1].version; got != want {
		t.Fatalf("user_version = %d, want %d", got, want)
	}
	rows := recordRows(t, pool)
	if len(rows) != 1 || !reflect.DeepEqual(rows[0][:3], sentinel) {
		t.Fatalf("_sqlx_migrations = %v, want only %v", rows, sentinel)
	}
}

func TestBaselineBuildsTheSchemaTheOldMigrationsBuilt(t *testing.T) {
	fresh := newRawDatabase(t)
	applyTo(t, fresh, onlyBaseline(t))
	got := schemaOf(t, fresh)
	want := schemaOf(t, openRaw(t, createRelease05Database(t)))
	if !maps.Equal(got, want) {
		t.Fatalf("schema differs\n got: %q\nwant: %q", got, want)
	}
}

// A database bkd 0.5 left has user_version 0. A stale number below the baseline,
// left by some other tool, must not matter either.
func TestOpenAdoptsABkd05Database(t *testing.T) {
	for _, stale := range []string{"0", "5", "8"} {
		t.Run("user_version "+stale, func(t *testing.T) {
			pool := openRaw(t, createRelease05Database(t))
			mustExec(t, pool, "PRAGMA user_version = "+stale)
			schema, records := schemaOf(t, pool), recordRows(t, pool)

			applyTo(t, pool, onlyBaseline(t))

			if got := schemaVersion(t, pool); got != baselineVersion {
				t.Fatalf("user_version = %d", got)
			}
			if got := schemaOf(t, pool); !maps.Equal(got, schema) {
				t.Fatalf("schema changed\n got: %q\nwant: %q", got, schema)
			}
			after := recordRows(t, pool)
			if len(after) != len(records)+1 || !reflect.DeepEqual(after[:len(records)], records) {
				t.Fatalf("old records changed\nbefore: %v\n after: %v", records, after)
			}
			if last := after[len(after)-1]; !reflect.DeepEqual(last[:3], sentinel) {
				t.Fatalf("last record = %v, want %v", last, sentinel)
			}
		})
	}
}

func TestAdoptedDatabaseKeepsItsDataAndIsWritable(t *testing.T) {
	pool, err := Open(context.Background(), createRelease05Database(t))
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

func TestBaselineReplaysOverADatabaseThatLostItsVersion(t *testing.T) {
	pool := openRaw(t, createRelease05Database(t))
	for range 2 {
		mustExec(t, pool, "PRAGMA user_version = 0")
		applyTo(t, pool, onlyBaseline(t))
		// The nine old records and one sentinel, however often it replays.
		if got := len(recordRows(t, pool)); got != 10 {
			t.Fatalf("%d records", got)
		}
	}
}

type snapshot struct {
	version int64
	schema  map[string]string
	records [][]any
}

func snapshotOf(t *testing.T, pool *sql.DB) snapshot {
	t.Helper()
	s := snapshot{version: schemaVersion(t, pool), schema: schemaOf(t, pool)}
	if _, ok := s.schema["_sqlx_migrations"]; ok {
		s.records = recordRows(t, pool)
	}
	return s
}

func TestOpenRefusesADatabaseItCannotUpgradeAndLeavesItAlone(t *testing.T) {
	changed := func(create func(*testing.T) string, change string) func(*testing.T) string {
		return func(t *testing.T) string {
			path := create(t)
			mustExec(t, openRaw(t, path), change)
			return path
		}
	}
	empty := func(t *testing.T) string { return filepath.Join(t.TempDir(), "other.sqlite3") }
	tests := map[string]struct {
		create func(*testing.T) string
		want   string
	}{
		"from the Rust server": {createRustDatabase, "schema version 9"},
		"one migration short": {
			changed(createRelease05Database, "DELETE FROM _sqlx_migrations WHERE version = 9"),
			"schema version 9"},
		"with a migration that failed halfway": {
			changed(createRelease05Database, "UPDATE _sqlx_migrations SET success = 0 WHERE version = 3"),
			"schema version 9"},
		"of another program": {changed(empty, "CREATE TABLE notes (body TEXT)"), "schema version 9"},
		"with only a view":   {changed(empty, "CREATE VIEW one AS SELECT 1"), "schema version 9"},
		// sqlx numbers the migrations of other programs the same way.
		"of another program that uses sqlx": {changed(empty, `
			CREATE TABLE _sqlx_migrations (version BIGINT PRIMARY KEY, description TEXT NOT NULL,
				installed_on TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, success BOOLEAN NOT NULL,
				checksum BLOB NOT NULL, execution_time BIGINT NOT NULL);
			INSERT INTO _sqlx_migrations VALUES (9, 'feed icons', '2026-11-20 07:01:38', 1, x'09', 1);
			CREATE TABLE feeds (url TEXT)`), "schema version 9"},
		// What a restored `.dump` of a new database looks like.
		"new, without its version": {changed(createNewDatabase, "PRAGMA user_version = 0"), "schema version 9"},
		"from a newer bkd":         {changed(createNewDatabase, "PRAGMA user_version = 1000"), "newer than"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			path := test.create(t)
			raw := openRaw(t, path)
			before := snapshotOf(t, raw)

			pool, err := Open(context.Background(), path)
			if err == nil {
				_ = pool.Close()
				t.Fatal("the database was opened")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %v, want it to say %q", err, test.want)
			}
			if after := snapshotOf(t, raw); !reflect.DeepEqual(after, before) {
				t.Fatalf("database changed\nbefore: %v\n after: %v", before, after)
			}
		})
	}
}

// A second bkd, or a long import, may hold the write lock while this one starts.
func TestAnUpToDateDatabaseNeedsNoWriteLock(t *testing.T) {
	pool := openTemp(t)
	ctx := context.Background()
	err := WithImmediateTx(ctx, pool, func(*sql.Conn) error {
		return migrate(ctx, pool)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The baseline file replaced nine migrations; a lower or higher number would
// skip or repeat it on the databases those left.
func TestTheBaselineComesFirst(t *testing.T) {
	if got := shippedMigrations(t)[0].version; got != baselineVersion {
		t.Fatalf("first migration = %d, want the baseline %d", got, baselineVersion)
	}
}

func TestLaterMigrationsApplyOnceAndInOrder(t *testing.T) {
	pool := newRawDatabase(t)
	later := append(onlyBaseline(t),
		migration{version: 10, sql: "CREATE TABLE later (id INTEGER)"},
		migration{version: 11, sql: "ALTER TABLE later ADD COLUMN name TEXT"})

	// Neither statement can run twice.
	for range 2 {
		applyTo(t, pool, later)
		if got := schemaVersion(t, pool); got != 11 {
			t.Fatalf("user_version = %d", got)
		}
	}
}

func TestApplyMigrationSkipsOneAlreadyApplied(t *testing.T) {
	pool := newRawDatabase(t)
	applyTo(t, pool, onlyBaseline(t))
	done := migration{version: baselineVersion, sql: "this is not SQL"}
	known := versionRange{baseline: baselineVersion, latest: baselineVersion}
	if err := applyMigration(context.Background(), pool, done, known); err != nil {
		t.Fatal(err)
	}
}

// A newer bkd may upgrade the database between the first read and the write lock.
func TestApplyMigrationRefusesADatabaseThatBecameNewer(t *testing.T) {
	pool := newRawDatabase(t)
	mustExec(t, pool, "PRAGMA user_version = 10")
	pending := migration{version: baselineVersion, sql: "this is not SQL"}
	known := versionRange{baseline: baselineVersion, latest: baselineVersion}
	err := applyMigration(context.Background(), pool, pending, known)
	if err == nil || !strings.Contains(err.Error(), "newer than") {
		t.Fatalf("err = %v", err)
	}
}

func TestFailedMigrationLeavesNothingBehind(t *testing.T) {
	pool := newRawDatabase(t)
	broken := append(onlyBaseline(t),
		migration{version: 10, sql: "CREATE TABLE done (id INTEGER)"},
		migration{version: 11, sql: "CREATE TABLE half_done (id INTEGER); INSERT INTO no_such_table VALUES (1);"})

	err := applyMigrations(context.Background(), pool, broken)
	if err == nil || !strings.Contains(err.Error(), "migration 11") {
		t.Fatalf("err = %v", err)
	}
	if _, ok := schemaOf(t, pool)["half_done"]; ok {
		t.Fatal("the half-applied table survived")
	}
	if got := schemaVersion(t, pool); got != 10 {
		t.Fatalf("user_version = %d, want the last migration that succeeded", got)
	}
}

func TestLoadMigrationsOrdersByVersion(t *testing.T) {
	fsys := fstest.MapFS{
		"0010_late_one.sql":  {Data: []byte("SELECT 10;")},
		"0002_early.sql":     {Data: []byte("SELECT 2;")},
		"2147483647_end.sql": {Data: []byte("SELECT 3;")},
		"notes.txt":          {Data: []byte("ignored")},
	}
	got, err := loadMigrations(fsys)
	if err != nil {
		t.Fatal(err)
	}
	want := []migration{{2, "SELECT 2;"}, {10, "SELECT 10;"}, {2147483647, "SELECT 3;"}}
	if !slices.Equal(got, want) {
		t.Fatalf("migrations = %+v, want %+v", got, want)
	}
}

func TestLoadMigrationsRejectsBadNames(t *testing.T) {
	// user_version holds 1 to 2147483647 here.
	names := []string{"init.sql", "x_init.sql", "0001.sql", "0000_zero.sql", "2147483648_big.sql"}
	for _, name := range names {
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

func TestLoadMigrationsRejectsAnEmptySet(t *testing.T) {
	if _, err := loadMigrations(fstest.MapFS{}); err == nil {
		t.Fatal("no files were accepted")
	}
}
