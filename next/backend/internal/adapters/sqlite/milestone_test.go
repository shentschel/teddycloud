package sqlite

import (
	"path/filepath"
	"testing"

	applicationcatalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
)

func TestMilestoneVersionedContentUpgradeAndVerifiedRollback(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source.sqlite")
	backupPath := filepath.Join(directory, "pre-upgrade.sqlite")
	restoredPath := filepath.Join(directory, "restored.sqlite")
	baseline := SchemaMigrations()
	original, err := Open(t.Context(), Config{Path: source}, baseline)
	if err != nil {
		t.Fatal(err)
	}
	originalContent := testContent(t, "Before upgrade", true)
	if err := original.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		return repository.Save(t.Context(), originalContent)
	}); err != nil {
		t.Fatal(err)
	}
	if err := original.Close(); err != nil {
		t.Fatal(err)
	}

	migrations := append(baseline, Migration{
		Version: 2, ID: "0002-milestone-content-description",
		Statements: []string{"ALTER TABLE tc_catalog_content ADD COLUMN description TEXT NOT NULL DEFAULT ''"},
	})
	owner, err := OpenLifecycleOwner(t.Context(), Config{Path: source, UpgradeBackupPath: backupPath}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(t.Context())
	if version, err := owner.CurrentVersion(t.Context()); err != nil || version != 2 {
		t.Fatalf("upgrade version = %d, %v; want 2", version, err)
	}
	snapshot, found, err := owner.UpgradeBackup(t.Context())
	if err != nil || !found || snapshot.SchemaVersion != 1 {
		t.Fatalf("pre-upgrade snapshot = %+v, %t, %v", snapshot, found, err)
	}
	if err := VerifyBackup(t.Context(), snapshot, migrations); err != nil {
		t.Fatal(err)
	}
	newerContent := testContent(t, "After upgrade", true)
	if err := owner.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		return repository.Save(t.Context(), newerContent)
	}); err != nil {
		t.Fatal(err)
	}

	if err := owner.Restore(t.Context(), snapshot, restoredPath); err != nil {
		t.Fatal(err)
	}
	if version, err := owner.CurrentVersion(t.Context()); err != nil || version != 1 {
		t.Fatalf("rollback version = %d, %v; want 1", version, err)
	}
	assertOwnerTitle(t, owner, "Before upgrade")

	preserved, err := Open(t.Context(), Config{Path: source}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	defer preserved.Close()
	if version, err := preserved.CurrentVersion(t.Context()); err != nil || version != 2 {
		t.Fatalf("preserved source version = %d, %v; want 2", version, err)
	}
	if err := preserved.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		got, found, err := repository.FindByID(t.Context(), newerContent.ID())
		if err != nil {
			return err
		}
		if !found || got.Facts().Title() != "After upgrade" {
			t.Fatalf("preserved source = %q, found %t", got.Facts().Title(), found)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
