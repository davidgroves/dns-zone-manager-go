package store

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/dialect/sqlitedialect"

	"github.com/davidgroves/dns-zone-manager-go/internal/config"

	_ "modernc.org/sqlite"
)

// Ensure pgx stdlib driver is linked.
var _ = stdlib.GetDefaultDriver

// Open opens a bun.DB for the configured backend.
// For SQLite it applies foreign_keys=ON, journal_mode=WAL, auto_vacuum=INCREMENTAL,
// and chmods the database file to 0600.
func Open(settings config.DatabaseSettings) (*bun.DB, string, error) {
	backend := settings.Backend
	if backend == "" {
		backend = "sqlite"
	}

	switch backend {
	case "sqlite":
		db, err := openSQLite(settings)
		if err != nil {
			return nil, "", err
		}
		return db, backend, nil
	case "postgres":
		db, err := openPostgres(settings)
		if err != nil {
			return nil, "", err
		}
		return db, backend, nil
	default:
		return nil, "", fmt.Errorf("store: unsupported database backend %q", backend)
	}
}

func openSQLite(settings config.DatabaseSettings) (*bun.DB, error) {
	path := settings.Path
	if path == "" {
		return nil, fmt.Errorf("store: sqlite backend requires database.path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return nil, fmt.Errorf("store: create sqlite parent dir: %w", err)
	}

	// modernc.org/sqlite registers as "sqlite".
	sqldb, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open sqlite: %w", err)
	}
	sqldb.SetMaxOpenConns(1)
	sqldb.SetMaxIdleConns(1)
	sqldb.SetConnMaxLifetime(0)

	if err := applySQLitePragmas(sqldb); err != nil {
		_ = sqldb.Close()
		return nil, err
	}
	restrictSQLitePermissions(path)

	return bun.NewDB(sqldb, sqlitedialect.New()), nil
}

func applySQLitePragmas(sqldb *sql.DB) error {
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA auto_vacuum = INCREMENTAL",
	}
	for _, p := range pragmas {
		if _, err := sqldb.Exec(p); err != nil {
			return fmt.Errorf("store: %s: %w", p, err)
		}
	}
	return nil
}

func restrictSQLitePermissions(path string) {
	if _, err := os.Stat(path); err != nil {
		return
	}
	if err := os.Chmod(path, 0o600); err != nil {
		slog.Warn("could not chmod sqlite database", "path", path, "error", err)
	}
}

func openPostgres(settings config.DatabaseSettings) (*bun.DB, error) {
	if settings.Postgres == nil {
		return nil, fmt.Errorf("store: postgres backend requires database.postgres")
	}
	dsn, err := postgresDSN(settings.Postgres)
	if err != nil {
		return nil, err
	}
	sqldb, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open postgres: %w", err)
	}
	pool := settings.Postgres.PoolSize
	if pool <= 0 {
		pool = 5
	}
	sqldb.SetMaxOpenConns(pool)
	sqldb.SetMaxIdleConns(pool)
	sqldb.SetConnMaxLifetime(time.Hour)

	if err := sqldb.Ping(); err != nil {
		_ = sqldb.Close()
		return nil, fmt.Errorf("store: ping postgres: %w", err)
	}
	return bun.NewDB(sqldb, pgdialect.New()), nil
}

func postgresDSN(p *config.PostgresSettings) (string, error) {
	raw := p.URL()
	u, err := url.Parse(raw)
	if err != nil {
		// Non-URL / libpq-keyword DSNs are valid; fall through without failing.
		return appendLibpqOptions(raw, p), nil //nolint:nilerr // intentional keyword-DSN fallback
	}
	q := u.Query()
	if p.DSN.IsZero() {
		if p.SSLMode != "" && q.Get("sslmode") == "" {
			q.Set("sslmode", p.SSLMode)
		}
		if p.ConnectTimeout > 0 && q.Get("connect_timeout") == "" {
			q.Set("connect_timeout", strconv.Itoa(int(p.ConnectTimeout.Seconds())))
		}
	}
	var options []string
	// Prefer -cname=value (no space). url.Values.Encode turns spaces into '+',
	// and some parsers pass that through so Postgres sees GUC "+search_path".
	if p.Schema != "" {
		options = append(options, "-csearch_path="+p.Schema)
	}
	if p.StatementTimeoutMS > 0 {
		options = append(options, "-cstatement_timeout="+strconv.Itoa(p.StatementTimeoutMS))
	}
	if len(options) > 0 {
		existing := q.Get("options")
		joined := strings.Join(options, " ")
		if existing != "" {
			joined = existing + " " + joined
		}
		q.Set("options", joined)
	}
	// Encode spaces as %20, not '+', so options survive DSN parsing.
	u.RawQuery = strings.ReplaceAll(q.Encode(), "+", "%20")
	return u.String(), nil
}

func appendLibpqOptions(dsn string, p *config.PostgresSettings) string {
	parts := []string{dsn}
	if p.Schema != "" {
		parts = append(parts, "options='-csearch_path="+p.Schema+"'")
	}
	return strings.Join(parts, " ")
}
