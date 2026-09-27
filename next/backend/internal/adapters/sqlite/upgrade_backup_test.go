package sqlite

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	sqliteDriver "modernc.org/sqlite"
)

func TestUpgradeCreatesVerifiedPreUpgradeBackup(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "source.sqlite")
	backupPath := filepath.Join(directory, "pre-upgrade.sqlite")
	migrations := testMigrations()
	createVersionOneDatabase(t, path)
	before := readUpgradeState(t, path)

	database, err := Open(t.Context(), Config{
		Path:              path,
		UpgradeBackupPath: backupPath,
	}, migrations)
	if err != nil {
		t.Fatalf("upgrade database: %v", err)
	}
	defer database.Close()
	if got := currentVersion(t, database); got != 2 {
		t.Fatalf("upgraded version = %d, want 2", got)
	}
	snapshot, ok := database.UpgradeBackup()
	if !ok {
		t.Fatal("upgrade did not retain backup metadata")
	}
	if snapshot.Path != backupPath || snapshot.SchemaVersion != 1 {
		t.Fatalf("upgrade backup = %+v", snapshot)
	}
	if err := VerifyBackup(t.Context(), snapshot, migrations); err != nil {
		t.Fatalf("verify upgrade backup: %v", err)
	}
	if got := readUpgradeState(t, backupPath); !reflect.DeepEqual(got, before) {
		t.Fatalf("backup state = %#v, want pre-upgrade %#v", got, before)
	}
}

func TestUpgradeBackupFailureLeavesDatabaseUnchanged(t *testing.T) {
	for _, test := range []struct {
		name        string
		destination func(*testing.T, string) string
		want        error
	}{
		{
			name: "missing destination",
			destination: func(*testing.T, string) string {
				return ""
			},
			want: ErrUpgradeBackupRequired,
		},
		{
			name: "existing destination",
			destination: func(t *testing.T, directory string) string {
				path := filepath.Join(directory, "existing.sqlite")
				if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
				return path
			},
			want: ErrBackupExists,
		},
		{
			name: "unusable destination parent",
			destination: func(t *testing.T, directory string) string {
				blocker := filepath.Join(directory, "not-a-directory")
				if err := os.WriteFile(blocker, []byte("block"), 0600); err != nil {
					t.Fatal(err)
				}
				return filepath.Join(blocker, "backup.sqlite")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "source.sqlite")
			createVersionOneDatabase(t, path)
			before := readUpgradeState(t, path)

			_, err := Open(t.Context(), Config{
				Path:              path,
				UpgradeBackupPath: test.destination(t, directory),
			}, testMigrations())
			if err == nil {
				t.Fatal("upgrade unexpectedly succeeded")
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("upgrade error = %v, want %v", err, test.want)
			}
			after := readUpgradeState(t, path)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("state after failed backup = %#v, want %#v", after, before)
			}
		})
	}
}

func TestInvalidLedgerRefusesBeforeCreatingUpgradeBackup(t *testing.T) {
	for _, test := range []struct {
		name      string
		prepare   func(*testing.T, string)
		supported func() []Migration
		want      error
	}{
		{
			name: "dirty",
			prepare: func(t *testing.T, path string) {
				createVersionOneDatabase(t, path)
				setLedgerValue(t, path, `UPDATE tc_schema_migrations SET dirty = 1 WHERE version = 1`)
			},
			supported: testMigrations,
			want:      ErrDirtyMigration,
		},
		{
			name: "newer",
			prepare: func(t *testing.T, path string) {
				database, err := Open(t.Context(), Config{Path: path}, testMigrations())
				if err != nil {
					t.Fatal(err)
				}
				if err := database.Close(); err != nil {
					t.Fatal(err)
				}
			},
			supported: func() []Migration { return testMigrations()[:1] },
			want:      ErrSchemaNewer,
		},
		{
			name:    "checksum drift",
			prepare: createVersionOneDatabase,
			supported: func() []Migration {
				migrations := testMigrations()
				migrations[0].Statements = []string{`CREATE TABLE example (id INTEGER PRIMARY KEY, changed TEXT)`}
				return migrations
			},
			want: ErrChecksumDrift,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "source.sqlite")
			backupPath := filepath.Join(directory, "must-not-exist.sqlite")
			test.prepare(t, path)

			_, err := Open(t.Context(), Config{
				Path:              path,
				UpgradeBackupPath: backupPath,
			}, test.supported())
			if !errors.Is(err, test.want) {
				t.Fatalf("open error = %v, want %v", err, test.want)
			}
			if _, err := os.Lstat(backupPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("backup path was created before validation: %v", err)
			}
		})
	}
}

func TestFailedUpgradeRetainsVerifiedPreUpgradeBackup(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "source.sqlite")
	backupPath := filepath.Join(directory, "pre-upgrade.sqlite")
	createVersionOneDatabase(t, path)
	before := readUpgradeState(t, path)
	migrations := append(testMigrations()[:1], Migration{
		Version:    2,
		ID:         "0002-broken",
		Statements: []string{`THIS IS NOT SQL`},
	})

	_, err := Open(t.Context(), Config{
		Path:              path,
		UpgradeBackupPath: backupPath,
	}, migrations)
	var upgradeFailure *UpgradeError
	if !errors.As(err, &upgradeFailure) {
		t.Fatalf("upgrade error = %v, want UpgradeError", err)
	}
	if err := VerifyBackup(t.Context(), upgradeFailure.Backup, migrations); err != nil {
		t.Fatalf("verify retained pre-upgrade backup: %v", err)
	}
	if got := readUpgradeState(t, backupPath); !reflect.DeepEqual(got, before) {
		t.Fatalf("retained backup state = %#v, want %#v", got, before)
	}
}

func TestFreshAndSameVersionOpenDoNotRequireUpgradeBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.sqlite")
	migrations := testMigrations()
	database, err := Open(t.Context(), Config{Path: path}, migrations)
	if err != nil {
		t.Fatalf("open fresh database: %v", err)
	}
	if _, ok := database.UpgradeBackup(); ok {
		t.Fatal("fresh open unexpectedly created an upgrade backup")
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(t.Context(), Config{Path: path}, migrations)
	if err != nil {
		t.Fatalf("reopen same version: %v", err)
	}
	defer database.Close()
	if _, ok := database.UpgradeBackup(); ok {
		t.Fatal("same-version reopen unexpectedly created an upgrade backup")
	}
}

type upgradeLedgerRow struct {
	version   int
	id        string
	checksum  string
	dirty     bool
	appliedAt sql.NullString
}

type upgradeState struct {
	ledger []upgradeLedgerRow
	ids    []int
	schema string
}

func createVersionOneDatabase(t *testing.T, path string) {
	t.Helper()
	database, err := Open(t.Context(), Config{Path: path}, testMigrations()[:1])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(t.Context(), `INSERT INTO example (id) VALUES (41)`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
}

func setLedgerValue(t *testing.T, path, statement string) {
	t.Helper()
	db := openRawDatabase(t, path)
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), statement); err != nil {
		t.Fatal(err)
	}
}

func readUpgradeState(t *testing.T, path string) upgradeState {
	t.Helper()
	db := openRawDatabase(t, path)
	defer db.Close()
	state := upgradeState{}
	rows, err := db.QueryContext(t.Context(), `
		SELECT version, migration_id, checksum, dirty, applied_at
		FROM tc_schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var row upgradeLedgerRow
		if err := rows.Scan(&row.version, &row.id, &row.checksum, &row.dirty, &row.appliedAt); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		state.ledger = append(state.ledger, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	rows, err = db.QueryContext(t.Context(), `SELECT id FROM example ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		state.ids = append(state.ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(
		t.Context(),
		`SELECT sql FROM sqlite_schema WHERE type = 'table' AND name = 'example'`,
	).Scan(&state.schema); err != nil {
		t.Fatal(err)
	}
	return state
}

func openRawDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	connector, err := sqliteDriver.NewConnector(fileURI(path, false))
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}
