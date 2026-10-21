package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
)

func TestCatalogMigrationAudit(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "legacy.sqlite")
	owner := catalogRecoveryV4(t, original)
	before := catalogRecoveryRows(t, owner.database.db)
	legacy, err := owner.database.CreateBackup(t.Context(), filepath.Join(dir, "legacy-backup.sqlite"), SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	owner, err = OpenLifecycleOwner(t.Context(), Config{Path: original, UpgradeBackupPath: filepath.Join(dir, "upgrade-backup.sqlite")}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Close(context.Background()) }()
	upgraded := catalogRecoveryRows(t, owner.database.db)
	for table, rows := range before {
		if table == "sqlite_schema" || table == "tc_schema_migrations" {
			continue
		}
		if !reflect.DeepEqual(rows, upgraded[table]) {
			t.Fatal("migration changed original data", table)
		}
	}
	ledger, err := readAppliedMigrations(t.Context(), owner.database.db)
	if err != nil {
		t.Fatal(err)
	}
	migrations := SchemaMigrations()
	if len(ledger) != 7 {
		t.Fatal("unexpected migration count")
	}
	for i, row := range ledger {
		if row.id != migrations[i].ID || row.checksum != migrationChecksum(migrations[i]) || row.dirty {
			t.Fatal("identity/checksum drift")
		}
	}
	for i := 0; i < 4; i++ {
		if !reflect.DeepEqual(before["tc_schema_migrations"][i], upgraded["tc_schema_migrations"][i]) {
			t.Fatal("old migration ledger changed")
		}
	}
	current, err := owner.database.CreateBackup(t.Context(), filepath.Join(dir, "current.sqlite"), migrations)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Restore(t.Context(), legacy, filepath.Join(dir, "restored-legacy.sqlite")); err != nil {
		t.Fatal(err)
	}
	called := false
	err = owner.WithinCatalogTransaction(t.Context(), func(app.CatalogRepository) error { called = true; return nil })
	if !errors.Is(err, app.ErrUnsupported) || called {
		t.Fatal("legacy restore silently enabled catalog")
	}
	if !reflect.DeepEqual(before, catalogRecoveryRows(t, owner.database.db)) {
		t.Fatal("legacy restore changed schema or rows")
	}
	if err := owner.Restore(t.Context(), current, filepath.Join(dir, "restored-current.sqlite")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(upgraded, catalogRecoveryRows(t, owner.database.db)) {
		t.Fatal("current restore changed schema or rows")
	}
	if err := verifyForeignKeys(t.Context(), owner.database.db); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 2; n++ {
		command := blobCommand(t, n, n == 1)
		result, found, err := blobLookup(t, owner, blobKey(t, n), command)
		if err != nil || !found || result != importResult(command) {
			t.Fatal("receipt changed across migrations/restores")
		}
	}
}

// This is query/recovery evidence for existing rows, NOT full milestone
// acceptance. PI08-T-F01 blocks the required guarded creation scenario:
// production has no RegisterContent/RegisterCatalogVersion operation.
func TestCatalogMilestoneQueryable(t *testing.T) {
	f := catalogRecoveryCurrent(t)
	before := catalogRecoveryRows(t, f.owner.database.db)
	query := catalogFindQuery(t, app.FindVersionsByPair, 1)
	// The recovery fixture also intentionally contains malformed legacy text;
	// select this valid aggregate rather than claim global malformed rows work.
	query.Content = f.subject.ContentID()
	first, err := catalogRead(t, f.owner, query)
	if err != nil || first.Kind != app.Incomplete || first.Complete || first.Next == "" {
		t.Fatal("partial candidates presented as unique", first, err)
	}
	query.Page.Limit = 128
	full, err := catalogRead(t, f.owner, query)
	if err != nil || full.Kind != app.Ambiguous || !full.Complete || len(full.Entries) < 2 {
		t.Fatal("equal fingerprints merged", full, err)
	}
	for _, entry := range full.Entries {
		if entry.Media != app.CatalogOnly {
			t.Fatal("catalog-only row invented media")
		}
	}
	config := f.owner.config
	snapshot, err := f.owner.database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "query-snapshot.sqlite"), SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.owner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.owner, err = OpenLifecycleOwner(t.Context(), config, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.owner.Close(context.Background()) }()
	for phase := 0; phase < 2; phase++ {
		result, err := catalogRead(t, f.owner, query)
		if err != nil || result.Kind != full.Kind || !reflect.DeepEqual(result.Entries, full.Entries) {
			t.Fatal("query changed after reopen/restore", err)
		}
		if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
			t.Fatal("read/recovery mutated receipts or catalog history")
		}
		catalogRecoveryReplays(t, f)
		if phase == 0 {
			if err := f.owner.Restore(t.Context(), snapshot, filepath.Join(t.TempDir(), "selected.sqlite")); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Log("PI08-T-F01 remains open: SQL-seeded query fixture is not guarded catalog registration evidence")
}
