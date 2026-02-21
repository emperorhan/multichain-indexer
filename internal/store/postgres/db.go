package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

const (
	dbStatementTimeoutDefaultMS = 30000
	dbStatementTimeoutMinMS     = 0
	dbStatementTimeoutMaxMS     = 3_600_000

	// DefaultQueryTimeout is applied to individual non-transactional queries
	// to prevent runaway SQL from holding connections indefinitely.
	DefaultQueryTimeout = 30 * time.Second

	// LongQueryTimeout is used for heavier operations such as migrations,
	// reconciliation snapshots, or bulk purges.
	LongQueryTimeout = 5 * time.Minute
)

// withTimeout returns a child context that will be cancelled after d.
// Callers must defer the returned CancelFunc.
func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d)
}

type DB struct {
	*sql.DB
}

type Config struct {
	URL                string
	MaxOpenConns       int
	MaxIdleConns       int
	ConnMaxLifetime    time.Duration
	ConnMaxIdleTime    time.Duration
	StatementTimeoutMS int
}

func New(cfg Config) (*DB, error) {
	statementTimeoutMS, err := resolveStatementTimeoutMS(cfg)
	if err != nil {
		return nil, fmt.Errorf("resolve statement timeout: %w", err)
	}

	connURL := cfg.URL
	if statementTimeoutMS > 0 {
		connURL = appendStatementTimeout(connURL, statementTimeoutMS)
	}

	db, err := sql.Open("postgres", connURL)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	if cfg.ConnMaxIdleTime > 0 {
		db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)
	} else {
		db.SetConnMaxIdleTime(2 * time.Minute)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}

	return &DB{db}, nil
}

// appendStatementTimeout appends statement_timeout to the connection URL
// so it applies to all connections in the pool, not just one session.
func appendStatementTimeout(url string, timeoutMS int) string {
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	return url + sep + "options=-c%20statement_timeout%3D" + strconv.Itoa(timeoutMS)
}

func (db *DB) Close() error {
	return db.DB.Close()
}

// RunMigrations reads *.up.sql files from dir and executes them in sorted order.
// It uses a schema_migrations table to track which migrations have been applied,
// ensuring each migration runs at most once.
func (db *DB) RunMigrations(dir string) error {
	if _, err := db.ExecContext(context.Background(), `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version VARCHAR(255) PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if err != nil {
		return fmt.Errorf("glob migrations: %w", err)
	}
	sort.Strings(files)

	for _, f := range files {
		version := filepath.Base(f)

		var exists bool
		if err := db.QueryRowContext(context.Background(),
			"SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)", version,
		).Scan(&exists); err != nil {
			return fmt.Errorf("check migration %s: %w", version, err)
		}
		if exists {
			continue
		}

		content, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", version, err)
		}

		slog.Info("migration starting", "version", version)
		migrationStart := time.Now()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)

		// Set lock_timeout to prevent migrations from waiting indefinitely on locks.
		if _, err := db.ExecContext(ctx, "SET lock_timeout = '10s'"); err != nil {
			cancel()
			return fmt.Errorf("set lock_timeout for migration %s: %w", version, err)
		}

		if _, err := db.ExecContext(ctx, string(content)); err != nil {
			cancel()
			return fmt.Errorf("exec migration %s: %w", version, err)
		}
		cancel()

		if _, err := db.ExecContext(context.Background(),
			"INSERT INTO schema_migrations (version) VALUES ($1)", version,
		); err != nil {
			return fmt.Errorf("record migration %s: %w", version, err)
		}

		slog.Info("migration completed", "version", version, "elapsed", time.Since(migrationStart).String())
	}
	return nil
}

func resolveStatementTimeoutMS(cfg Config) (int, error) {
	if cfg.StatementTimeoutMS != 0 {
		if cfg.StatementTimeoutMS < dbStatementTimeoutMinMS || cfg.StatementTimeoutMS > dbStatementTimeoutMaxMS {
			return 0, fmt.Errorf("statement timeout %d out of allowed range [%d, %d]", cfg.StatementTimeoutMS, dbStatementTimeoutMinMS, dbStatementTimeoutMaxMS)
		}

		return cfg.StatementTimeoutMS, nil
	}

	statementTimeoutMS, err := getEnvIntBounded("DB_STATEMENT_TIMEOUT_MS", dbStatementTimeoutDefaultMS, dbStatementTimeoutMinMS, dbStatementTimeoutMaxMS)
	if err != nil {
		return 0, fmt.Errorf("DB_STATEMENT_TIMEOUT_MS: %w", err)
	}

	return statementTimeoutMS, nil
}

func getEnvIntBounded(key string, fallback int, min int, max int) (int, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback, nil
	}

	value, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("must be an integer")
	}

	if value < min || value > max {
		return 0, fmt.Errorf("must be within [%d, %d]", min, max)
	}

	return value, nil
}
