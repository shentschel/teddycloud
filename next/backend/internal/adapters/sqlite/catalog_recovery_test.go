package sqlite

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
)

// This is the schema-upgrade slice only; snapshot restore and process recovery
// deliberately remain separate acceptance work.
func TestCatalogSchemaRecovery(t *testing.T) {
	migrations := SchemaMigrations()
	if len(migrations) != 6 || migrations[5].ID != "0006-catalog-aggregate-state" {
		t.Fatal("expected the admitted v6 schema")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "v4.sqlite")
	owner := catalogRecoveryV4(t, path)
	before := catalogV4Rows(t, owner.database.db)
	for table, rows := range before {
		if len(rows) == 0 {
			t.Fatalf("v4 fixture does not exercise %s", table)
		}
	}
	objects := catalogSchemaRows(t, owner.database.db, catalogRecoveryObjects)
	if err := verifyForeignKeys(t.Context(), owner.database.db); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	// Compare the complete upgraded object definitions (including triggers and
	// indexes) with a fresh schema built by the same real ordered migrations.
	fresh, err := Open(t.Context(), Config{Path: filepath.Join(dir, "fresh-v6.sqlite")}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	wantObjects := catalogSchemaRows(t, fresh.db, catalogRecoveryObjects)
	if err := fresh.Close(); err != nil {
		t.Fatal(err)
	}

	var ledger [][]any
	for attempt := 0; attempt < 3; attempt++ {
		config := Config{Path: path}
		if attempt == 0 {
			config.UpgradeBackupPath = filepath.Join(dir, "required-pre-upgrade.sqlite")
		}
		upgraded, err := OpenLifecycleOwner(t.Context(), config, migrations)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = upgraded.Close(context.Background()) })
		if version, err := upgraded.CurrentVersion(t.Context()); err != nil || version != 6 {
			t.Fatalf("open %d version: %d %v", attempt, version, err)
		}
		if got := catalogV4Rows(t, upgraded.database.db); !reflect.DeepEqual(got, before) {
			t.Fatalf("open %d changed v4 values, storage types or ledger bytes", attempt)
		}
		gotObjects := catalogSchemaRows(t, upgraded.database.db, catalogRecoveryObjects)
		if !reflect.DeepEqual(gotObjects, wantObjects) {
			t.Fatal("upgraded tables, indexes or triggers differ from fresh v6")
		}
		for _, object := range objects {
			if !catalogSchemaContains(gotObjects, object) {
				t.Fatal("preexisting schema object changed")
			}
		}
		applied, err := readAppliedMigrations(t.Context(), upgraded.database.db)
		if err != nil || len(applied) != 6 {
			t.Fatalf("migration ledger: %+v %v", applied, err)
		}
		for i, row := range applied {
			if row.version != i+1 || row.id != migrations[i].ID || row.dirty || row.checksum != migrationChecksum(migrations[i]) {
				t.Fatalf("migration %d identity, checksum or clean state changed: %+v", i+1, row)
			}
		}
		gotLedger := catalogSchemaRows(t, upgraded.database.db, "SELECT * FROM tc_schema_migrations ORDER BY version")
		if attempt == 0 {
			ledger = gotLedger
		} else if !reflect.DeepEqual(gotLedger, ledger) {
			t.Fatal("same-version open rewrote the migration ledger")
		}
		if err := verifyForeignKeys(t.Context(), upgraded.database.db); err != nil {
			t.Fatal(err)
		}
		for n := 1; n <= 2; n++ {
			command := blobCommand(t, n, n == 1)
			if result, found, err := blobLookup(t, upgraded, blobKey(t, n), command); err != nil || !found || result != importResult(command) {
				t.Fatalf("retained receipt %d: %v %v", n, found, err)
			}
		}
		content := testContent(t, "", false).ID()
		result, err := catalogRead(t, upgraded, app.Query{Kind: app.GetContent, Content: content, Page: app.PageRequest{Limit: 1}})
		if err != nil || result.Kind != app.Found || len(result.Entries) != 1 || result.Entries[0].Review != app.CatalogUnmanaged || result.Entries[0].Title.Status != catalog.CatalogFactUnknown || result.Entries[0].ProtectedCustom {
			t.Fatalf("upgrade invented review authority: %+v %v", result, err)
		}
		err = upgraded.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
			subject, err := catalog.ContentSubject(content)
			if err != nil {
				return err
			}
			if _, found, err := r.LoadReview(t.Context(), subject); err != nil || found {
				t.Fatalf("migration invented review state: %t %v", found, err)
			}
			if _, found, err := r.LoadOrder(t.Context(), content); err != nil || found {
				t.Fatalf("migration invented order state: %t %v", found, err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, table := range append(append([]string{}, catalogFoundationTables...), "tc_catalog_review_states", "tc_catalog_order_states", "tc_catalog_order_state_versions") {
			var count int
			if err := upgraded.database.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
				t.Fatalf("migration/read fabricated %s: %d %v", table, count, err)
			}
		}
		if got := catalogV4Rows(t, upgraded.database.db); !reflect.DeepEqual(got, before) {
			t.Fatal("readback changed retained v4 rows")
		}
		if err := upgraded.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCatalogSchemaRecoveryRejectsInvalidV4(t *testing.T) {
	for _, tc := range []struct {
		name, mutation string
		want           error
	}{
		{"dirty", "UPDATE tc_schema_migrations SET dirty=1 WHERE version=4", ErrDirtyMigration},
		{"checksum", "UPDATE tc_schema_migrations SET checksum='wrong' WHERE version=3", ErrChecksumDrift},
		{"corrupt", "", ErrDatabaseInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "original-v4.sqlite")
			owner := catalogRecoveryV4(t, path)
			if tc.mutation != "" {
				catalogSchemaExec(t, owner.database.db, tc.mutation)
			}
			// Lifecycle owners preserve WAL sidecars. Materialize the v4 fixture
			// before corrupting its header, so a valid WAL cannot repair it.
			catalogSchemaExec(t, owner.database.db, "PRAGMA wal_checkpoint(TRUNCATE)")
			if err := owner.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "corrupt" {
				before = bytes.Clone(before)
				copy(before[:16], "not a sqlite db!")
				if err := os.WriteFile(path, before, 0600); err != nil {
					t.Fatal(err)
				}
			}
			backup := filepath.Join(dir, "must-not-publish.sqlite")
			for attempt := 0; attempt < 2; attempt++ {
				config := Config{Path: path, UpgradeBackupPath: backup}
				want := tc.want
				if attempt == 0 {
					opened, openErr := Open(t.Context(), config, SchemaMigrations())
					err = openErr
					if opened != nil {
						_ = opened.Close()
						t.Fatal("invalid v4 fixture returned a usable database")
					}
				} else {
					opened, openErr := OpenLifecycleOwner(t.Context(), config, SchemaMigrations())
					err = openErr
					want = ErrLifecycleOpen // Public owner deliberately sanitizes errors.
					if opened != nil {
						_ = opened.Close(t.Context())
						t.Fatal("invalid v4 fixture returned a usable owner")
					}
				}
				if !errors.Is(err, want) {
					t.Fatalf("open %d error: %v, want %v", attempt, err, want)
				}
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(after, before) {
					t.Fatalf("rejected upgrade changed original bytes: %v", err)
				}
				if _, err := os.Stat(backup); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("invalid fixture published an upgrade backup: %v", err)
				}
			}
		})
	}
}

const catalogRecoveryObjects = "SELECT type,name,tbl_name,sql FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' ORDER BY name"

func catalogRecoveryV4(t *testing.T, path string) *LifecycleOwner {
	t.Helper()
	owner, err := OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations()[:4])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	if err := owner.WithinTransaction(t.Context(), func(r app.ContentRepository) error {
		return r.Save(t.Context(), testContent(t, "Retained Ω title", true))
	}); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 2; n++ {
		if _, err := blobRecord(t, owner, blobKey(t, n), blobCommand(t, n, n == 1)); err != nil {
			t.Fatal(err)
		}
	}
	db := owner.database.db
	catalogSchemaExec(t, db, "INSERT INTO tc_catalog_content VALUES (?, ?, '', NULL)", catalogSchemaID("cnt", 2), "Baseline\x00bytes")
	catalogSchemaExec(t, db, "INSERT INTO tc_content_versions VALUES (?,?,4294967295,?,0,NULL,NULL)", catalogSchemaID("ver", 99), catalogSchemaID("cnt", 2), bytes.Repeat([]byte{255}, 20))
	tag, observation, decision := catalogSchemaID("tag", 1), catalogSchemaID("obs", 1), catalogSchemaID("dec", 1)
	catalogSchemaExec(t, db, "INSERT INTO tc_tags VALUES (?,?,3)", tag, []byte{0, 255, 1, 2, 3, 4, 5, 6})
	catalogSchemaExec(t, db, "INSERT INTO tc_tag_observations VALUES (?,?,2,0,1,'source Ω','opaque-r1','record:1','2000-01-01T00:00:00.000000001Z',0,1)", observation, tag)
	catalogSchemaExec(t, db, "INSERT INTO tc_tag_decisions VALUES (?,?,0,2,3)", decision, tag)
	catalogSchemaExec(t, db, "INSERT INTO tc_tag_decision_support VALUES (?,?,?)", tag, decision, observation)
	return owner
}
