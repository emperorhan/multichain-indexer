package postgres

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

const (
	dbStatementTimeoutDefaultMS = 30000
	dbStatementTimeoutMinMS     = 0
	dbStatementTimeoutMaxMS     = 3_600_000
)

type DB struct {
	*sql.DB
}

type Config struct {
	URL                string
	MaxOpenConns       int
	MaxIdleConns       int
	ConnMaxLifetime    time.Duration
	StatementTimeoutMS int
}

func New(cfg Config) (*DB, error) {
	statementTimeoutMS, err := resolveStatementTimeoutMS(cfg)
	if err != nil {
		return nil, fmt.Errorf("resolve statement timeout: %w", err)
	}

	db, err := sql.Open("postgres", cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	if statementTimeoutMS > 0 {
		if _, err := db.Exec("SET statement_timeout = $1", statementTimeoutMS); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("set statement_timeout: %w", err)
		}
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}

	return &DB{db}, nil
}

func (db *DB) Close() error {
	return db.DB.Close()
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
