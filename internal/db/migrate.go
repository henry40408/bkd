package db

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha512"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// createMigrationsTable is byte for byte the statement sqlx runs. SQLite keeps
// the statement text in sqlite_master, so the layout is part of compatibility.
const createMigrationsTable = `
CREATE TABLE IF NOT EXISTS _sqlx_migrations (
    version BIGINT PRIMARY KEY,
    description TEXT NOT NULL,
    installed_on TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    success BOOLEAN NOT NULL,
    checksum BLOB NOT NULL,
    execution_time BIGINT NOT NULL
);
`

// migrationFileName is `<version>_<description>.sql`, the sqlx convention.
var migrationFileName = regexp.MustCompile(`^(\d+)_(.+)\.sql$`)

type migration struct {
	version     int64
	description string
	sql         string
	// checksum is the SHA-384 of the file, the digest sqlx stores.
	checksum []byte
}

func migrate(ctx context.Context, db *sql.DB) error {
	files, err := fs.Sub(embeddedMigrations, "migrations")
	if err != nil {
		return err
	}
	migrations, err := loadMigrations(files)
	if err != nil {
		return err
	}
	return applyMigrations(ctx, db, migrations)
}

// loadMigrations reads every `.sql` file in the root of fsys, ordered by version.
func loadMigrations(fsys fs.FS) ([]migration, error) {
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return nil, err
	}
	migrations := make([]migration, 0, len(names))
	for _, name := range names {
		m, err := readMigration(fsys, name)
		if err != nil {
			return nil, err
		}
		migrations = append(migrations, m)
	}
	slices.SortFunc(migrations, func(a, b migration) int {
		return cmp.Compare(a.version, b.version)
	})
	for i := 1; i < len(migrations); i++ {
		if migrations[i].version == migrations[i-1].version {
			return nil, fmt.Errorf("migrations share version %d", migrations[i].version)
		}
	}
	return migrations, nil
}

func readMigration(fsys fs.FS, name string) (migration, error) {
	parts := migrationFileName.FindStringSubmatch(name)
	if parts == nil {
		return migration{}, fmt.Errorf("migration file %q is not named <version>_<description>.sql", name)
	}
	version, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return migration{}, fmt.Errorf("migration file %q: %w", name, err)
	}
	contents, err := fs.ReadFile(fsys, name)
	if err != nil {
		return migration{}, err
	}
	checksum := sha512.Sum384(contents)
	return migration{
		version:     version,
		description: strings.ReplaceAll(parts[2], "_", " "),
		sql:         string(contents),
		checksum:    checksum[:],
	}, nil
}

// applyMigrations refuses to touch a database whose history disagrees with
// migrations, then applies what is missing, one transaction per migration.
func applyMigrations(ctx context.Context, db *sql.DB, migrations []migration) error {
	if _, err := db.ExecContext(ctx, createMigrationsTable); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}
	applied, err := appliedChecksums(ctx, db)
	if err != nil {
		return err
	}
	if err := checkHistory(applied, migrations); err != nil {
		return err
	}
	for _, m := range migrations {
		if _, done := applied[m.version]; done {
			continue
		}
		if err := applyMigration(ctx, db, m); err != nil {
			return err
		}
	}
	return nil
}

// appliedChecksums maps each recorded version to its checksum. A row whose
// success flag is false means a migration died halfway: sqlx stops there too.
func appliedChecksums(ctx context.Context, db *sql.DB) (map[int64][]byte, error) {
	var dirty int64
	err := db.QueryRowContext(ctx,
		"SELECT version FROM _sqlx_migrations WHERE success = false ORDER BY version LIMIT 1").Scan(&dirty)
	if err == nil {
		return nil, fmt.Errorf("migration %d was partially applied; fix the database by hand", dirty)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("check for a partial migration: %w", err)
	}

	rows, err := db.QueryContext(ctx, "SELECT version, checksum FROM _sqlx_migrations ORDER BY version")
	if err != nil {
		return nil, fmt.Errorf("list applied migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	applied := make(map[int64][]byte)
	for rows.Next() {
		var version int64
		var checksum []byte
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, fmt.Errorf("list applied migrations: %w", err)
		}
		applied[version] = checksum
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list applied migrations: %w", err)
	}
	return applied, nil
}

func checkHistory(applied map[int64][]byte, migrations []migration) error {
	known := make(map[int64]migration, len(migrations))
	for _, m := range migrations {
		known[m.version] = m
	}
	for _, version := range sortedVersions(applied) {
		m, ok := known[version]
		if !ok {
			return fmt.Errorf("migration %d was previously applied but is missing in the resolved migrations", version)
		}
		if !bytes.Equal(applied[version], m.checksum) {
			return fmt.Errorf("migration %d was previously applied but has been modified", version)
		}
	}
	return nil
}

func sortedVersions(applied map[int64][]byte) []int64 {
	versions := make([]int64, 0, len(applied))
	for version := range applied {
		versions = append(versions, version)
	}
	slices.Sort(versions)
	return versions
}

// applyMigration runs the script and records it in one transaction, so a
// failure leaves neither half behind. Like sqlx, it records the run time in a
// second statement, because the transaction has to finish first.
func applyMigration(ctx context.Context, db *sql.DB, m migration) error {
	start := time.Now()
	err := WithImmediateTx(ctx, db, func(conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, m.sql); err != nil {
			return err
		}
		_, err := conn.ExecContext(ctx,
			`INSERT INTO _sqlx_migrations ( version, description, success, checksum, execution_time )
			 VALUES ( ?1, ?2, TRUE, ?3, -1 )`,
			m.version, m.description, m.checksum)
		return err
	})
	if err != nil {
		return fmt.Errorf("apply migration %d: %w", m.version, err)
	}
	_, err = db.ExecContext(ctx,
		"UPDATE _sqlx_migrations SET execution_time = ?1 WHERE version = ?2",
		time.Since(start).Nanoseconds(), m.version)
	if err != nil {
		return fmt.Errorf("record run time of migration %d: %w", m.version, err)
	}
	return nil
}
