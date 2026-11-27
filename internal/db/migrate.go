package db

import (
	"cmp"
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// migrationFileName is `<version>_<description>.sql`.
var migrationFileName = regexp.MustCompile(`^(\d+)_.+\.sql$`)

// baselineRecord is the description releases up to 0.5 gave the baseline's
// version in _sqlx_migrations. The number alone would also match another
// program that uses sqlx.
const baselineRecord = "bookmark url index"

// migration brings the schema to version, which `PRAGMA user_version` records.
type migration struct {
	version int64
	sql     string
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
// The first one is the baseline: it creates the whole schema of its version.
func loadMigrations(fsys fs.FS) ([]migration, error) {
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, errors.New("no migration files")
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
	// user_version is a signed 32-bit integer, and 0 means no migration yet.
	version, err := strconv.ParseInt(parts[1], 10, 32)
	if err != nil {
		return migration{}, fmt.Errorf("migration file %q: %w", name, err)
	}
	if version == 0 {
		return migration{}, fmt.Errorf("migration file %q: versions start at 1", name)
	}
	contents, err := fs.ReadFile(fsys, name)
	if err != nil {
		return migration{}, err
	}
	return migration{version: version, sql: string(contents)}, nil
}

// versionRange holds the first and the latest schema version this build has.
type versionRange struct {
	baseline, latest int64
}

func (r versionRange) refuseNewer(current int64) error {
	if current > r.latest {
		return fmt.Errorf("database schema version %d is newer than %d, the latest this bkd knows; run a newer bkd",
			current, r.latest)
	}
	return nil
}

// applyMigrations applies the migrations newer than the database, one transaction
// each. migrations is ordered and starts with the baseline.
func applyMigrations(ctx context.Context, db *sql.DB, migrations []migration) error {
	known := versionRange{baseline: migrations[0].version, latest: migrations[len(migrations)-1].version}
	current, err := userVersion(ctx, db)
	if err != nil {
		return err
	}
	if err := known.refuseNewer(current); err != nil {
		return err
	}
	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := applyMigration(ctx, db, m, known); err != nil {
			return fmt.Errorf("apply migration %d: %w", m.version, err)
		}
	}
	return nil
}

// applyMigration runs the script and moves user_version in one transaction, so a
// failure leaves neither half behind.
func applyMigration(ctx context.Context, db *sql.DB, m migration, known versionRange) error {
	return WithImmediateTx(ctx, db, func(conn *sql.Conn) error {
		// Read again under the write lock: another process, maybe a newer bkd,
		// may have got here first.
		current, err := userVersion(ctx, conn)
		if err != nil {
			return err
		}
		if err := known.refuseNewer(current); err != nil {
			return err
		}
		if current >= m.version {
			return nil
		}
		if current < known.baseline {
			if err := checkBaselineApplies(ctx, conn, known.baseline); err != nil {
				return err
			}
		}
		if _, err := conn.ExecContext(ctx, m.sql); err != nil {
			return err
		}
		// A PRAGMA takes no bound parameters.
		_, err = conn.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.version))
		return err
	})
}

type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func userVersion(ctx context.Context, q rowQuerier) (int64, error) {
	var version int64
	if err := q.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

// checkBaselineApplies accepts an empty database, or one that the migrations of
// an older release brought all the way to the baseline. The baseline only creates
// what is missing, so on an older schema it would leave old tables as they are.
func checkBaselineApplies(ctx context.Context, q rowQuerier, baseline int64) error {
	var objects, records int
	err := q.QueryRowContext(ctx, `SELECT count(*), count(*) FILTER (WHERE name = '_sqlx_migrations')
		FROM sqlite_master WHERE name NOT LIKE 'sqlite\_%' ESCAPE '\'`).Scan(&objects, &records)
	if err != nil {
		return fmt.Errorf("inspect database: %w", err)
	}
	if objects == 0 {
		return nil
	}
	reached := false
	if records > 0 {
		err := q.QueryRowContext(ctx, `SELECT
			EXISTS (SELECT 1 FROM _sqlx_migrations WHERE version = ?1 AND description = ?2)
			AND NOT EXISTS (SELECT 1 FROM _sqlx_migrations WHERE success = FALSE)`,
			baseline, baselineRecord).Scan(&reached)
		if err != nil {
			return fmt.Errorf("read old migration records: %w", err)
		}
	}
	if !reached {
		return fmt.Errorf("the database is not empty and has no complete record of schema version %d; "+
			"if it is from an older bkd, open it with bkd 0.5 once, then upgrade", baseline)
	}
	return nil
}
