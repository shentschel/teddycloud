package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	sqliteDriver "modernc.org/sqlite"

	applicationcatalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domaincatalog "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
)

func TestBackupRejectsForeignKeyViolationsBeforePublication(t *testing.T) {
	for _, count := range []int{1, 2048} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			snapshot, migrations := foreignKeyBackupFixture(t, count)
			assertInvalidForeignKeyBackup(t, VerifyBackup(t.Context(), snapshot, migrations))
			directory := filepath.Dir(snapshot.Path)
			target := filepath.Join(directory, "restored.sqlite")
			assertInvalidForeignKeyBackup(t, RestoreBackupToNew(t.Context(), snapshot, target, migrations))
			if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid restore destination was published: %v", err)
			}

			database, err := Open(t.Context(), Config{Path: snapshot.Path}, migrations)
			if err != nil {
				t.Fatal(err)
			}
			backupTarget := filepath.Join(directory, "rejected.sqlite")
			result, err := database.CreateBackup(t.Context(), backupTarget, migrations)
			assertInvalidForeignKeyBackup(t, err)
			if result != (BackupFile{}) {
				t.Fatalf("invalid snapshot has verified metadata: %+v", result)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(backupTarget); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid backup destination was published: %v", err)
			}
			entries, err := os.ReadDir(directory)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".tc-sqlite-") {
					t.Fatalf("staging directory retained: %s", entry.Name())
				}
			}
			if digest, err := fileDigest(snapshot.Path); err != nil || digest != snapshot.SHA256 {
				t.Fatalf("invalid source changed: digest %q, error %v", digest, err)
			}
		})
	}
}

func TestBackupRestoresValidForeignKeySnapshot(t *testing.T) {
	snapshot, migrations := foreignKeyBackupFixture(t, 0)
	database, err := Open(t.Context(), Config{Path: snapshot.Path}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	backup, err := database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "backup.sqlite"), migrations)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyBackup(t.Context(), backup, migrations); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "restored.sqlite")
	if err := RestoreBackupToNew(t.Context(), backup, target, migrations); err != nil {
		t.Fatal(err)
	}
	raw := openRawDatabase(t, target)
	var count int
	if err := raw.QueryRowContext(t.Context(), `SELECT count(*) FROM child JOIN parent ON child.parent_id = parent.id`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("restored FK graph count = %d, error %v", count, err)
	}
	if err := verifyForeignKeys(t.Context(), raw); err != nil {
		t.Fatalf("restored FK graph: %v", err)
	}
}

func TestBackupForeignKeyQueryFailureDoesNotExposeDriverError(t *testing.T) {
	snapshot, migrations := foreignKeyBackupFixture(t, 0)
	raw := openRawDatabase(t, snapshot.Path)
	for _, statement := range []string{
		`CREATE TABLE nonunique_parent (id INTEGER)`,
		`CREATE TABLE malformed_child (parent_id INTEGER REFERENCES nonunique_parent(id))`,
		`INSERT INTO malformed_child VALUES (1)`,
	} {
		if _, err := raw.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	var integrity string
	if err := raw.QueryRowContext(t.Context(), `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("malformed FK fixture integrity = %q, %v", integrity, err)
	}
	var violation int
	err := raw.QueryRowContext(t.Context(), `SELECT 1 FROM pragma_foreign_key_check LIMIT 1`).Scan(&violation)
	var driverErr *sqliteDriver.Error
	if !errors.As(err, &driverErr) {
		t.Fatalf("fixture did not produce a real FK query driver error: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot.SHA256, err = fileDigest(snapshot.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{
		VerifyBackup(t.Context(), snapshot, migrations),
		RestoreBackupToNew(t.Context(), snapshot, filepath.Join(t.TempDir(), "rejected.sqlite"), migrations),
	} {
		assertInvalidForeignKeyBackup(t, err)
		if err.Error() != ErrBackupInvalid.Error()+": foreign key check could not complete" {
			t.Fatalf("SQL diagnostics escaped verification: %v", err)
		}
	}
}

func TestBackupVerificationPreservesContextErrors(t *testing.T) {
	snapshot, migrations := foreignKeyBackupFixture(t, 0)
	for _, want := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(want.Error(), func(t *testing.T) {
			var ctx context.Context
			var cancel context.CancelFunc
			if want == context.Canceled {
				ctx, cancel = context.WithCancel(t.Context())
				cancel()
			} else {
				ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			}
			defer cancel()
			if err := VerifyBackup(ctx, snapshot, migrations); !errors.Is(err, want) {
				t.Fatalf("verify context error = %v, want %v", err, want)
			}
			target := filepath.Join(t.TempDir(), "restored.sqlite")
			if err := RestoreBackupToNew(ctx, snapshot, target, migrations); !errors.Is(err, want) {
				t.Fatalf("restore context error = %v, want %v", err, want)
			}
			if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("canceled restore was published: %v", err)
			}
		})
	}
}

func TestForeignKeyCheckBoundsWaitAndReleasesResources(t *testing.T) {
	for _, test := range []struct {
		name               string
		callerTimeout      time.Duration
		cancelWhileWaiting bool
	}{
		{name: "caller deadline", callerTimeout: 20 * time.Millisecond},
		{name: "caller cancellation", cancelWhileWaiting: true},
		{name: "internal deadline"},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot, _ := foreignKeyBackupFixture(t, 0)
			raw := openRawDatabase(t, snapshot.Path)
			connection, err := raw.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			ctx := t.Context()
			want := error(context.DeadlineExceeded)
			bound := backupForeignKeyCheckTimeout + 2*time.Second
			if test.callerTimeout != 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, test.callerTimeout)
				defer cancel()
				bound = test.callerTimeout + 2*time.Second
			}
			if test.cancelWhileWaiting {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				timer := time.AfterFunc(20*time.Millisecond, cancel)
				defer timer.Stop()
				want = context.Canceled
				bound = 2 * time.Second
			}
			started := time.Now()
			err = verifyForeignKeys(ctx, raw)
			assertInvalidForeignKeyBackup(t, err)
			if !errors.Is(err, want) {
				t.Fatalf("blocked FK check error = %v", err)
			}
			if elapsed := time.Since(started); elapsed > bound {
				t.Fatalf("FK check exceeded time bound: %v", elapsed)
			}
			if err := connection.Close(); err != nil {
				t.Fatal(err)
			}
			if err := verifyForeignKeys(t.Context(), raw); err != nil {
				t.Fatalf("FK check after timeout: %v", err)
			}
		})
	}
}

func foreignKeyBackupFixture(t *testing.T, orphanCount int) (BackupFile, []Migration) {
	t.Helper()
	migrations := []Migration{{Version: 1, ID: "0001-fk-fixture", Statements: []string{
		`CREATE TABLE parent (id INTEGER PRIMARY KEY)`,
		`CREATE TABLE child (id INTEGER PRIMARY KEY, parent_id INTEGER NOT NULL REFERENCES parent(id)) WITHOUT ROWID`,
		`INSERT INTO parent VALUES (1)`,
		`INSERT INTO child VALUES (0, 1)`,
	}}}
	path := filepath.Join(t.TempDir(), "fixture.sqlite")
	database, err := Open(t.Context(), Config{Path: path}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	raw := openRawDatabase(t, path)
	if _, err := raw.ExecContext(t.Context(), `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if orphanCount > 0 {
		if _, err := raw.ExecContext(t.Context(), `WITH RECURSIVE ids(id) AS (
			SELECT 1 UNION ALL SELECT id + 1 FROM ids WHERE id < ?
		) INSERT INTO child SELECT id, 999 FROM ids`, orphanCount); err != nil {
			t.Fatal(err)
		}
	}
	var integrity string
	if err := raw.QueryRowContext(t.Context(), `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("fixture integrity = %q, %v", integrity, err)
	}
	var violations int
	if err := raw.QueryRowContext(t.Context(), `SELECT count(*) FROM pragma_foreign_key_check`).Scan(&violations); err != nil || violations != orphanCount {
		t.Fatalf("fixture FK violations = %d, want %d, error %v", violations, orphanCount, err)
	}
	applied, err := readAppliedMigrations(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateAppliedMigrations(applied, migrations); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	digest, err := fileDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	return BackupFile{Path: path, SHA256: digest, SchemaVersion: len(migrations)}, migrations
}

func assertInvalidForeignKeyBackup(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrBackupInvalid) {
		t.Fatalf("FK backup error = %v, want ErrBackupInvalid", err)
	}
	var driverErr *sqliteDriver.Error
	if errors.As(err, &driverErr) || errors.Is(err, sql.ErrNoRows) || errors.Is(err, sql.ErrConnDone) || errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("SQL/driver error escaped FK verification: %v", err)
	}
}

func TestBackupRestoresCommittedSnapshotWithoutOverwritingSource(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "database with spaces")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(directory, "source.sqlite")
	backupPath := filepath.Join(directory, "snapshot.sqlite")
	restoredPath := filepath.Join(directory, "restored.sqlite")
	migrations := SchemaMigrations()
	database, err := Open(t.Context(), Config{Path: source}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	original := testContent(t, "Original title", true)
	if err := database.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		return repository.Save(t.Context(), original)
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := database.CreateBackup(t.Context(), backupPath, migrations)
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}
	if snapshot.SchemaVersion != len(migrations) || len(snapshot.SHA256) != 64 {
		t.Fatalf("backup metadata = %+v", snapshot)
	}
	if err := VerifyBackup(t.Context(), snapshot, migrations); err != nil {
		t.Fatalf("verify backup: %v", err)
	}

	newer := testContent(t, "Newer title", true)
	if err := database.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		return repository.Save(t.Context(), newer)
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RestoreBackupToNew(t.Context(), snapshot, restoredPath, migrations); err != nil {
		t.Fatalf("restore backup: %v", err)
	}

	assertStoredTitle(t, source, migrations, "Newer title")
	assertStoredTitle(t, restoredPath, migrations, "Original title")
	if _, err := database.CreateBackup(t.Context(), backupPath, migrations); !errors.Is(err, ErrBackupExists) {
		t.Fatalf("duplicate backup error = %v, want %v", err, ErrBackupExists)
	}
	if err := RestoreBackupToNew(t.Context(), snapshot, restoredPath, migrations); !errors.Is(err, ErrRestoreExists) {
		t.Fatalf("duplicate restore error = %v, want %v", err, ErrRestoreExists)
	}
}

func TestRestoreRejectsTamperedBackupAndKeepsExistingTarget(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source.sqlite")
	backupPath := filepath.Join(directory, "backup.sqlite")
	target := filepath.Join(directory, "existing.sqlite")
	migrations := SchemaMigrations()
	database, err := Open(t.Context(), Config{Path: source}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := database.CreateBackup(t.Context(), backupPath, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backupPath, []byte("corrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyBackup(t.Context(), snapshot, migrations); !errors.Is(err, ErrBackupInvalid) {
		t.Fatalf("tampered backup verification error = %v", err)
	}
	if err := RestoreBackupToNew(t.Context(), snapshot, filepath.Join(directory, "new.sqlite"), migrations); !errors.Is(err, ErrBackupInvalid) {
		t.Fatalf("tampered backup restore error = %v", err)
	}
	if err := RestoreBackupToNew(t.Context(), snapshot, target, migrations); !errors.Is(err, ErrRestoreExists) {
		t.Fatalf("existing destination error = %v", err)
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "untouched" {
		t.Fatalf("existing destination changed: %q, %v", content, err)
	}
}

func assertStoredTitle(t *testing.T, path string, migrations []Migration, title string) {
	t.Helper()
	database, err := Open(t.Context(), Config{Path: path}, migrations)
	if err != nil {
		t.Fatalf("reopen %s: %v", path, err)
	}
	defer database.Close()
	if version, err := database.CurrentVersion(t.Context()); err != nil || version != len(migrations) {
		t.Fatalf("restored version = %d, %v", version, err)
	}
	id := testContent(t, title, true).ID()
	var stored domaincatalog.Content
	var found bool
	if err := database.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		stored, found, err = repository.FindByID(t.Context(), id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !found || stored.Facts().Title() != title {
		t.Fatalf("stored title = %q, found %t; want %q", stored.Facts().Title(), found, title)
	}
}
