package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
	"github.com/uptrace/bun"
)

//go:embed migrations/sqlite/*.sql migrations/postgres/*.sql
var embedMigrations embed.FS

// Advisory lock key matching Python migrate.py (0x646E735A4D475221).
const advisoryLockKey int64 = 0x646E735A4D475221

// Upgrade runs goose migrations to head for the given backend.
// On PostgreSQL it takes pg_advisory_lock around the migration.
func Upgrade(ctx context.Context, db *bun.DB, backend string) error {
	sqldb := db.DB
	dir := "migrations/sqlite"
	dialect := goose.DialectSQLite3
	if backend == "postgres" {
		dir = "migrations/postgres"
		dialect = goose.DialectPostgres
	}

	sub, err := fs.Sub(embedMigrations, dir)
	if err != nil {
		return fmt.Errorf("store: migrations fs: %w", err)
	}

	if backend == "postgres" {
		return withAdvisoryLock(ctx, sqldb, func() error {
			return runGoose(ctx, sqldb, dialect, sub)
		})
	}
	return runGoose(ctx, sqldb, dialect, sub)
}

func runGoose(ctx context.Context, sqldb *sql.DB, dialect goose.Dialect, fsys fs.FS) error {
	provider, err := goose.NewProvider(dialect, sqldb, fsys)
	if err != nil {
		return fmt.Errorf("store: goose provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("store: goose up: %w", err)
	}
	return nil
}

func withAdvisoryLock(ctx context.Context, sqldb *sql.DB, fn func() error) error {
	conn, err := sqldb.Conn(ctx)
	if err != nil {
		return fmt.Errorf("store: advisory lock conn: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", advisoryLockKey); err != nil {
		return fmt.Errorf("store: pg_advisory_lock: %w", err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", advisoryLockKey)
	}()
	return fn()
}

// SchemaExists reports whether the scheduled_changes table is present.
func SchemaExists(ctx context.Context, db *bun.DB, backend string) (bool, error) {
	var exists bool
	var err error
	if backend == "postgres" {
		err = db.NewRaw(`
			SELECT EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_name = 'scheduled_changes'
			)`).Scan(ctx, &exists)
	} else {
		err = db.NewRaw(`
			SELECT COUNT(*) > 0 FROM sqlite_master
			WHERE type = 'table' AND name = 'scheduled_changes'`).Scan(ctx, &exists)
	}
	if err != nil {
		return false, fmt.Errorf("store: schema exists: %w", err)
	}
	return exists, nil
}
