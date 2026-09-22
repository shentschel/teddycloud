package sqlite

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenCreatesAndReopensSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.sqlite")
	migrations := testMigrations()[:1]

	database, err := Open(t.Context(), Config{Path: path}, migrations)
	if err != nil {
		t.Fatalf("open fresh database: %v", err)
	}
	if got := currentVersion(t, database); got != 1 {
		t.Fatalf("fresh version = %d, want 1", got)
	}
	assertConnectionInvariants(t, database)
	if err := database.Close(); err != nil {
		t.Fatalf("close fresh database: %v", err)
	}

	reopened, err := Open(t.Context(), Config{Path: path}, migrations)
	if err != nil {
		t.Fatalf("reopen migrated database: %v", err)
	}
	defer reopened.Close()
	if got := currentVersion(t, reopened); got != 1 {
		t.Fatalf("reopened version = %d, want 1", got)
	}
}

func TestOpenUpgradesSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.sqlite")
	migrations := testMigrations()

	first, err := Open(t.Context(), Config{Path: path}, migrations[:1])
	if err != nil {
		t.Fatalf("open version 1: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close version 1: %v", err)
	}

	upgraded, err := Open(t.Context(), Config{Path: path}, migrations)
	if err != nil {
		t.Fatalf("upgrade to version 2: %v", err)
	}
	defer upgraded.Close()
	if got := currentVersion(t, upgraded); got != 2 {
		t.Fatalf("upgraded version = %d, want 2", got)
	}

	if _, err := upgraded.db.ExecContext(
		t.Context(),
		`INSERT INTO example (id, label) VALUES (1, 'ready')`,
	); err != nil {
		t.Fatalf("use upgraded schema: %v", err)
	}
}

func TestOpenRejectsDirtyMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dirty.sqlite")
	migrations := testMigrations()[:1]
	database, err := Open(t.Context(), Config{Path: path}, migrations)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if _, err := database.db.ExecContext(
		t.Context(),
		`UPDATE tc_schema_migrations SET dirty = 1 WHERE version = 1`,
	); err != nil {
		t.Fatalf("mark migration dirty: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	_, err = Open(t.Context(), Config{Path: path}, migrations)
	if !errors.Is(err, ErrDirtyMigration) {
		t.Fatalf("reopen dirty database error = %v, want %v", err, ErrDirtyMigration)
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "newer.sqlite")
	migrations := testMigrations()
	database, err := Open(t.Context(), Config{Path: path}, migrations)
	if err != nil {
		t.Fatalf("open version 2: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close version 2: %v", err)
	}

	_, err = Open(t.Context(), Config{Path: path}, migrations[:1])
	if !errors.Is(err, ErrSchemaNewer) {
		t.Fatalf("open newer database error = %v, want %v", err, ErrSchemaNewer)
	}
}

func TestOpenRejectsChecksumDrift(t *testing.T) {
	path := filepath.Join(t.TempDir(), "drift.sqlite")
	migrations := testMigrations()[:1]
	database, err := Open(t.Context(), Config{Path: path}, migrations)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	drifted := []Migration{{
		Version: 1,
		ID:      migrations[0].ID,
		Statements: []string{
			`CREATE TABLE example (id INTEGER PRIMARY KEY, changed TEXT)`,
		},
	}}
	_, err = Open(t.Context(), Config{Path: path}, drifted)
	if !errors.Is(err, ErrChecksumDrift) {
		t.Fatalf("open drifted database error = %v, want %v", err, ErrChecksumDrift)
	}
}

func TestFailedMigrationLeavesDirtyLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "partial.sqlite")
	migrations := append(testMigrations()[:1], Migration{
		Version:    2,
		ID:         "0002-broken",
		Statements: []string{`THIS IS NOT SQL`},
	})

	if _, err := Open(t.Context(), Config{Path: path}, migrations); err == nil {
		t.Fatal("broken migration unexpectedly succeeded")
	}
	_, err := Open(t.Context(), Config{Path: path}, migrations)
	if !errors.Is(err, ErrDirtyMigration) {
		t.Fatalf("reopen after broken migration error = %v, want %v", err, ErrDirtyMigration)
	}
}

func TestValidateMigrationsRejectsGaps(t *testing.T) {
	err := validateMigrations([]Migration{{
		Version:    2,
		ID:         "0002-gap",
		Statements: []string{`SELECT 1`},
	}})
	if !errors.Is(err, ErrInvalidMigration) {
		t.Fatalf("validate gap error = %v, want %v", err, ErrInvalidMigration)
	}
}

func TestConnectionDSNRejectsInvalidTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timeout.sqlite")
	for _, timeout := range []time.Duration{
		time.Nanosecond,
		maxBusyTimeout + time.Millisecond,
	} {
		if _, _, err := connectionDSN(Config{Path: path, BusyTimeout: timeout}); err == nil {
			t.Fatalf("busy timeout %s unexpectedly accepted", timeout)
		}
	}
}

func TestDriverEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.sqlite")
	database, err := Open(t.Context(), Config{Path: path}, nil)
	if err != nil {
		t.Fatalf("open metadata database: %v", err)
	}
	defer database.Close()

	var version string
	if err := database.db.QueryRowContext(t.Context(), `SELECT sqlite_version()`).Scan(&version); err != nil {
		t.Fatalf("read SQLite version: %v", err)
	}
	if strings.TrimSpace(version) == "" {
		t.Fatal("SQLite version is empty")
	}

	rows, err := database.db.QueryContext(t.Context(), `PRAGMA compile_options`)
	if err != nil {
		t.Fatalf("read compile options: %v", err)
	}
	defer rows.Close()
	var options []string
	for rows.Next() {
		var option string
		if err := rows.Scan(&option); err != nil {
			t.Fatalf("scan compile option: %v", err)
		}
		options = append(options, option)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate compile options: %v", err)
	}
	if len(options) == 0 {
		t.Fatal("SQLite compile options are empty")
	}
	t.Logf("SQLite version: %s", version)
	t.Logf("SQLite compile options: %s", strings.Join(options, ","))
}

func testMigrations() []Migration {
	return []Migration{
		{
			Version: 1,
			ID:      "0001-example",
			Statements: []string{
				`CREATE TABLE example (id INTEGER PRIMARY KEY)`,
			},
		},
		{
			Version: 2,
			ID:      "0002-example-label",
			Statements: []string{
				`ALTER TABLE example ADD COLUMN label TEXT NOT NULL DEFAULT ''`,
			},
		},
	}
}

func currentVersion(t *testing.T, database *Database) int {
	t.Helper()
	version, err := database.CurrentVersion(t.Context())
	if err != nil {
		t.Fatalf("read current version: %v", err)
	}
	return version
}

func assertConnectionInvariants(t *testing.T, database *Database) {
	t.Helper()
	checks := map[string]string{
		"journal_mode": "wal",
		"synchronous":  "2",
		"foreign_keys": "1",
		"busy_timeout": "5000",
	}
	for pragma, want := range checks {
		var got string
		if err := database.db.QueryRowContext(
			t.Context(),
			"PRAGMA "+pragma,
		).Scan(&got); err != nil {
			t.Fatalf("read %s: %v", pragma, err)
		}
		if !strings.EqualFold(got, want) {
			t.Fatalf("%s = %q, want %q", pragma, got, want)
		}
	}
}
