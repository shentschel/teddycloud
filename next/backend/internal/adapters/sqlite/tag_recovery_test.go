package sqlite

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	applicationcatalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	applicationtag "github.com/shentschel/teddycloud/next/backend/internal/application/tagregistry"
	domaincatalog "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	domaintag "github.com/shentschel/teddycloud/next/backend/internal/domain/tag"
)

func TestTagSchemaRecovery(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "content-v1.sqlite")
	migrations := SchemaMigrations()
	content := testContent(t, "Content retained across Tag upgrade", true)

	v1, err := Open(t.Context(), Config{Path: path}, migrations[:1])
	if err != nil {
		t.Fatal(err)
	}
	if err := v1.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		return repository.Save(t.Context(), content)
	}); err != nil {
		t.Fatal(err)
	}
	if err := v1.Close(); err != nil {
		t.Fatal(err)
	}

	owner, err := OpenLifecycleOwner(t.Context(), Config{
		Path:              path,
		UpgradeBackupPath: filepath.Join(directory, "before-tags.sqlite"),
	}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(t.Context())
	preTag, found, err := owner.UpgradeBackup(t.Context())
	if err != nil || !found || preTag.SchemaVersion != 1 {
		t.Fatalf("pre-Tag backup = %+v, found %t, error %v", preTag, found, err)
	}
	assertRecoveryContent(t, owner, content)

	service := applicationtag.New(owner)
	value := registerMetadataTag(t, service)
	observation := tagMetadataObservation(t, value, 1, domaintag.CloudAuth, "true")
	value, err = service.UpdateMetadata(t.Context(), applicationtag.MetadataCommand{
		TagID: value.ID(),
		Change: domaintag.MetadataChange{
			ExpectedRevision: value.Revision(),
			Key:              domaintag.CloudAuth,
			Observations:     []evidence.Observation{observation},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := domaintag.ParseDecisionID("dec_00000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	value, err = service.UpdateMetadata(t.Context(), applicationtag.MetadataCommand{
		TagID: value.ID(),
		Change: domaintag.MetadataChange{
			ExpectedRevision: value.Revision(),
			Key:              domaintag.CloudAuth,
			Resolution: &domaintag.Resolution{
				ID:      decision,
				Support: []evidence.ID{observation.ID()},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	current, err := owner.database.CreateBackup(t.Context(), filepath.Join(directory, "tag-snapshot.sqlite"), migrations)
	if err != nil {
		t.Fatal(err)
	}
	newer := testRegistryTag(t, '1', domaintag.UIDFromBytes([8]byte{1}))
	if err := owner.WithinTagTransaction(t.Context(), func(repository applicationtag.TagRepository) error {
		return repository.Insert(t.Context(), newer)
	}); err != nil {
		t.Fatal(err)
	}
	if err := owner.Restore(t.Context(), current, filepath.Join(directory, "tag-restored.sqlite")); err != nil {
		t.Fatal(err)
	}
	if version, err := owner.CurrentVersion(t.Context()); err != nil || version != len(migrations) {
		t.Fatalf("restored Tag schema version = %d, %v", version, err)
	}
	assertRecoveryContent(t, owner, content)
	got, err := applicationtag.New(owner).FindByID(t.Context(), value.ID().String())
	if err != nil || !got.Equal(value) {
		t.Fatalf("restored Tag graph differs: %v", err)
	}
	if _, err := applicationtag.New(owner).FindByID(t.Context(), newer.ID().String()); !errors.Is(err, applicationtag.ErrTagNotFound) {
		t.Fatalf("post-snapshot Tag survived restore: %v", err)
	}

	invalid, err := owner.database.CreateBackup(t.Context(), filepath.Join(directory, "fk-invalid.sqlite"), migrations)
	if err != nil {
		t.Fatal(err)
	}
	raw := openRawDatabase(t, invalid.Path)
	if _, err := raw.ExecContext(t.Context(), `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(t.Context(), `DELETE FROM tc_tag_observations WHERE observation_id=?`, observation.ID().String()); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	invalid.SHA256, err = fileDigest(invalid.Path)
	if err != nil {
		t.Fatal(err)
	}
	invalidTarget := filepath.Join(directory, "must-not-select.sqlite")
	if err := owner.Restore(t.Context(), invalid, invalidTarget); !errors.Is(err, ErrBackupInvalid) {
		t.Fatalf("FK-invalid restore = %v", err)
	}
	if _, err := os.Lstat(invalidTarget); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("FK-invalid destination published: %v", err)
	}
	got, err = applicationtag.New(owner).FindByID(t.Context(), value.ID().String())
	if err != nil || !got.Equal(value) {
		t.Fatalf("FK-invalid restore changed selected handle: %v", err)
	}

	if err := owner.Restore(t.Context(), preTag, filepath.Join(directory, "pre-tag-restored.sqlite")); err != nil {
		t.Fatal(err)
	}
	if version, err := owner.CurrentVersion(t.Context()); err != nil || version != 1 {
		t.Fatalf("pre-Tag restore silently upgraded: %d, %v", version, err)
	}
	assertRecoveryContent(t, owner, content)
	if _, err := applicationtag.New(owner).FindByID(t.Context(), value.ID().String()); !errors.Is(err, applicationtag.ErrRepositoryUnavailable) {
		t.Fatalf("Tag on pre-Tag schema = %v", err)
	}
}

func assertRecoveryContent(t *testing.T, owner *LifecycleOwner, want domaincatalog.Content) {
	t.Helper()
	if err := owner.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		got, found, err := repository.FindByID(t.Context(), want.ID())
		if err != nil {
			return err
		}
		if !found || got.Facts().Title() != want.Facts().Title() {
			return fmt.Errorf("retained Content = %q, found %t", got.Facts().Title(), found)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
