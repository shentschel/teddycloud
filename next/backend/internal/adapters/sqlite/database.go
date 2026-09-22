// Package sqlite owns TeddyCloud Next's SQLite connection and schema lifecycle.
// SQL and driver types intentionally remain inside this adapter.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	sqliteDriver "modernc.org/sqlite"
)

const (
	defaultBusyTimeout = 5 * time.Second
	maxBusyTimeout     = time.Duration(1<<31-1) * time.Millisecond
)

var ErrConnectionInvariant = errors.New("sqlite connection invariant not satisfied")

// Config contains the connection settings owned by this adapter.
type Config struct {
	Path        string
	BusyTimeout time.Duration
}

// Database is the adapter-owned database handle. It deliberately does not
// expose database/sql or driver values.
type Database struct {
	db *sql.DB
}

// Open establishes the single-writer pool, verifies its connection invariants
// and applies the ordered migrations.
func Open(ctx context.Context, config Config, migrations []Migration) (*Database, error) {
	if err := validateMigrations(migrations); err != nil {
		return nil, err
	}

	dsn, busyTimeoutMillis, err := connectionDSN(config)
	if err != nil {
		return nil, err
	}
	connector, err := sqliteDriver.NewConnector(dsn)
	if err != nil {
		return nil, fmt.Errorf("create sqlite connector: %w", err)
	}

	db := sql.OpenDB(connector)
	// PI-05/A has no reader port. A one-connection pool is therefore the
	// explicit serialized writer boundary until PI-05/B introduces one.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(0)
	db.SetConnMaxLifetime(0)

	closeOnError := true
	defer func() {
		if closeOnError {
			_ = db.Close()
		}
	}()

	if err := verifyConnection(ctx, db, busyTimeoutMillis); err != nil {
		return nil, err
	}
	if err := migrate(ctx, db, migrations); err != nil {
		return nil, err
	}

	closeOnError = false
	return &Database{db: db}, nil
}

// Close releases the adapter-owned connection.
func (database *Database) Close() error {
	if database == nil || database.db == nil {
		return nil
	}
	return database.db.Close()
}

// CurrentVersion reports the latest clean schema version without exposing SQL.
func (database *Database) CurrentVersion(ctx context.Context) (int, error) {
	var version int
	if err := database.db.QueryRowContext(
		ctx,
		`SELECT COALESCE(MAX(version), 0) FROM tc_schema_migrations WHERE dirty = 0`,
	).Scan(&version); err != nil {
		return 0, fmt.Errorf("read current schema version: %w", err)
	}
	return version, nil
}

func connectionDSN(config Config) (string, int, error) {
	if strings.TrimSpace(config.Path) == "" {
		return "", 0, fmt.Errorf("sqlite path must not be empty")
	}

	timeout := config.BusyTimeout
	if timeout == 0 {
		timeout = defaultBusyTimeout
	}
	if timeout < time.Millisecond {
		return "", 0, fmt.Errorf("sqlite busy timeout must be at least one millisecond")
	}
	if timeout > maxBusyTimeout {
		return "", 0, fmt.Errorf("sqlite busy timeout must not exceed %s", maxBusyTimeout)
	}
	timeoutMillis := int(timeout / time.Millisecond)

	absolutePath, err := filepath.Abs(config.Path)
	if err != nil {
		return "", 0, fmt.Errorf("resolve sqlite path: %w", err)
	}
	location := &url.URL{Scheme: "file", Path: filepath.ToSlash(absolutePath)}
	query := location.Query()
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "synchronous(FULL)")
	query.Add("_pragma", "foreign_keys(ON)")
	query.Add("_pragma", "busy_timeout("+strconv.Itoa(timeoutMillis)+")")
	location.RawQuery = query.Encode()

	return location.String(), timeoutMillis, nil
}

func verifyConnection(ctx context.Context, db *sql.DB, expectedBusyTimeout int) error {
	connection, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("%w: acquire connection: %v", ErrConnectionInvariant, err)
	}
	defer connection.Close()

	checks := []struct {
		name  string
		query string
		want  string
	}{
		{name: "journal_mode", query: "PRAGMA journal_mode", want: "wal"},
		{name: "synchronous", query: "PRAGMA synchronous", want: "2"},
		{name: "foreign_keys", query: "PRAGMA foreign_keys", want: "1"},
		{name: "busy_timeout", query: "PRAGMA busy_timeout", want: strconv.Itoa(expectedBusyTimeout)},
	}
	for _, check := range checks {
		var got string
		if err := connection.QueryRowContext(ctx, check.query).Scan(&got); err != nil {
			return fmt.Errorf("%w: read %s: %v", ErrConnectionInvariant, check.name, err)
		}
		if !strings.EqualFold(got, check.want) {
			return fmt.Errorf("%w: %s=%q, want %q", ErrConnectionInvariant, check.name, got, check.want)
		}
	}
	return nil
}
