package sqlite

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	applicationcatalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domaincatalog "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
)

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
	if snapshot.SchemaVersion != 1 || len(snapshot.SHA256) != 64 {
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
	if version, err := database.CurrentVersion(t.Context()); err != nil || version != 1 {
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
