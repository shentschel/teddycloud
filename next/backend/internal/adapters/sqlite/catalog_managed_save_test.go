package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	applicationcatalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domaincatalog "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
	sqliteDriver "modernc.org/sqlite"
)

func TestCatalogManagedSaveGuard(t *testing.T) {
	for _, tc := range []struct {
		name       string
		protection int
	}{
		{name: "unmanaged", protection: -1},
		{name: "ordinary managed", protection: 0},
		{name: "protected custom", protection: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner := openLifecycleApplicationOwner(t)
			original := testContent(t, "Original", true)
			catalogGuardSave(t, owner, original)
			if tc.protection >= 0 {
				catalogSchemaExec(t, owner.database.db,
					`INSERT INTO tc_catalog_managed VALUES (?,7,?)`, original.ID().String(), tc.protection)
				// A managed Save must stop before even attempting an INSERT or
				// UPDATE, including an identical no-op save.
				for _, event := range []string{"INSERT", "UPDATE"} {
					catalogSchemaExec(t, owner.database.db,
						"CREATE TEMP TRIGGER guard_"+event+" BEFORE "+event+
							" ON tc_catalog_content BEGIN SELECT RAISE(ABORT,'private storage diagnostic'); END")
				}
			}
			before := catalogV4Rows(t, owner.database.db)
			model, err := domaincatalog.ParseModelNumber("02-0200")
			if err != nil {
				t.Fatal(err)
			}
			article, err := domaincatalog.ParseArticleNumber("22000390")
			if err != nil {
				t.Fatal(err)
			}
			replacement, err := domaincatalog.NewContent(original.ID(),
				domaincatalog.NewContentFacts("Changed", domaincatalog.NewProductIdentifiers(&model, &article)))
			if err != nil {
				t.Fatal(err)
			}
			for _, candidate := range []domaincatalog.Content{original, replacement, testContent(t, "Cleared identifiers", false)} {
				calls := 0
				err := owner.WithinTransaction(t.Context(), func(r applicationcatalog.ContentRepository) error {
					calls++
					return r.Save(t.Context(), candidate)
				})
				if calls != 1 {
					t.Fatalf("callback calls = %d", calls)
				}
				if tc.protection < 0 {
					if err != nil {
						t.Fatal(err)
					}
					catalogGuardAssertContent(t, owner, candidate)
				} else {
					catalogGuardAssertError(t, err, applicationcatalog.ErrUnauthorized)
					if !reflect.DeepEqual(catalogV4Rows(t, owner.database.db), before) {
						t.Fatal("denied Save mutated persisted data")
					}
				}
			}
			if tc.protection >= 0 {
				rows := catalogSchemaRows(t, owner.database.db,
					`SELECT aggregate_revision,protected_custom FROM tc_catalog_managed`)
				if !reflect.DeepEqual(rows, [][]any{{int64(7), int64(tc.protection)}}) {
					t.Fatal("denied Save changed managed state")
				}
			}
		})
	}
}

func TestCatalogManagedSaveGuardRollback(t *testing.T) {
	owner := openLifecycleApplicationOwner(t)
	original := testContent(t, "Original", true)
	catalogGuardSave(t, owner, original)
	catalogSchemaExec(t, owner.database.db, `INSERT INTO tc_catalog_managed VALUES (?,1,0)`, original.ID().String())
	before := catalogV4Rows(t, owner.database.db)
	err := owner.WithinTransaction(t.Context(), func(r applicationcatalog.ContentRepository) error {
		if err := r.Save(t.Context(), catalogGuardOtherContent(t)); err != nil {
			return err
		}
		return r.Save(t.Context(), testContent(t, "Attempted overwrite", false))
	})
	catalogGuardAssertError(t, err, applicationcatalog.ErrUnauthorized)
	if !reflect.DeepEqual(catalogV4Rows(t, owner.database.db), before) {
		t.Fatal("failed transaction retained a partial content write")
	}

	// A marker written earlier in this very transaction must also be visible.
	database := openApplicationDatabase(t)
	catalogGuardSave(t, database, original)
	err = database.withinSQLTransaction(t.Context(), func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO tc_catalog_managed VALUES (?,1,1)`, original.ID().String()); err != nil {
			return err
		}
		return (contentRepository{transaction: tx}).Save(t.Context(), testContent(t, "Attempted overwrite", false))
	})
	catalogGuardAssertError(t, err, applicationcatalog.ErrUnauthorized)
	if rows := catalogSchemaRows(t, database.db, `SELECT * FROM tc_catalog_managed`); len(rows) != 0 {
		t.Fatal("failed transaction retained an uncommitted managed marker")
	}
	catalogGuardAssertContent(t, database, original)
}

func TestCatalogManagedSaveGuardStaleSnapshot(t *testing.T) {
	writer, stale := openContentionPair(t, 40*time.Millisecond)
	original := testContent(t, "Original", true)
	catalogGuardSave(t, writer, original)
	calls := 0
	started := time.Now()
	err := stale.WithinTransaction(t.Context(), func(r applicationcatalog.ContentRepository) error {
		calls++
		// Force a real pre-adoption snapshot on a second connection.
		var managed bool
		if err := r.(contentRepository).transaction.QueryRowContext(t.Context(),
			`SELECT EXISTS(SELECT 1 FROM tc_catalog_managed WHERE content_id=?)`, original.ID().String()).Scan(&managed); err != nil {
			return err
		}
		if managed {
			t.Fatal("fixture already managed")
		}
		if _, err := writer.db.ExecContext(t.Context(),
			`INSERT INTO tc_catalog_managed VALUES (?,1,0)`, original.ID().String()); err != nil {
			return err
		}
		// Save sees the old snapshot, but SQLite must refuse its promotion to
		// a writer rather than permit an overwrite after committed adoption.
		return r.Save(t.Context(), testContent(t, "Stale overwrite", false))
	})
	catalogGuardAssertError(t, err, applicationcatalog.ErrRepositoryContention)
	if calls != 1 || time.Since(started) > time.Second {
		t.Fatalf("stale writer was replayed or unbounded: calls=%d", calls)
	}
	catalogGuardAssertContent(t, writer, original)
	err = stale.WithinTransaction(t.Context(), func(r applicationcatalog.ContentRepository) error {
		return r.Save(t.Context(), original)
	})
	catalogGuardAssertError(t, err, applicationcatalog.ErrUnauthorized)
}

func TestCatalogManagedSaveGuardExactV4Restore(t *testing.T) {
	dir := t.TempDir()
	migrations := SchemaMigrations()
	v4, err := Open(t.Context(), Config{Path: filepath.Join(dir, "v4.sqlite")}, migrations[:4])
	if err != nil {
		t.Fatal(err)
	}
	defer v4.Close()
	original := testContent(t, "V4 original", true)
	catalogGuardSave(t, v4, original)
	snapshot, err := v4.CreateBackup(t.Context(), filepath.Join(dir, "snapshot.sqlite"), migrations)
	if err != nil {
		t.Fatal(err)
	}
	schemaBefore := catalogSchemaRows(t, v4.db,
		`SELECT type,name,tbl_name,sql FROM sqlite_schema ORDER BY name`)
	ledgerBefore := catalogSchemaRows(t, v4.db, `SELECT * FROM tc_schema_migrations ORDER BY version`)

	owner := openLifecycleApplicationOwner(t)
	catalogGuardSave(t, owner, original)
	catalogSchemaExec(t, owner.database.db, `INSERT INTO tc_catalog_managed VALUES (?,1,1)`, original.ID().String())
	var escaped applicationcatalog.ContentRepository
	err = owner.WithinTransaction(t.Context(), func(r applicationcatalog.ContentRepository) error {
		escaped = r
		return r.Save(t.Context(), original)
	})
	catalogGuardAssertError(t, err, applicationcatalog.ErrUnauthorized)
	if err := owner.Restore(t.Context(), snapshot, filepath.Join(dir, "restored.sqlite")); err != nil {
		t.Fatal(err)
	}
	// The old transaction cannot cross the restore fence.
	catalogGuardAssertError(t, escaped.Save(t.Context(), original), applicationcatalog.ErrRepositoryUnavailable)
	updated := testContent(t, "V4 updated", false)
	catalogGuardSave(t, owner, updated)
	catalogGuardSave(t, owner, catalogGuardOtherContent(t))
	catalogGuardAssertContent(t, owner, updated)
	catalogGuardAssertContent(t, owner, catalogGuardOtherContent(t))
	if version, err := owner.CurrentVersion(t.Context()); err != nil || version != 4 {
		t.Fatalf("restored version = %d, %v", version, err)
	}
	if !reflect.DeepEqual(schemaBefore, catalogSchemaRows(t, owner.database.db,
		`SELECT type,name,tbl_name,sql FROM sqlite_schema ORDER BY name`)) ||
		!reflect.DeepEqual(ledgerBefore, catalogSchemaRows(t, owner.database.db,
			`SELECT * FROM tc_schema_migrations ORDER BY version`)) {
		t.Fatal("unmanaged operations implicitly changed the exact v4 schema")
	}
	for _, table := range catalogFoundationTables {
		var exists bool
		if err := owner.database.db.QueryRowContext(t.Context(),
			`SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE name=?)`, table).Scan(&exists); err != nil || exists {
			t.Fatalf("catalog schema became available after v4 restore: %s %v", table, err)
		}
	}
}

func TestCatalogManagedSaveGuardCancellation(t *testing.T) {
	for _, managed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unmanaged", true: "managed"}[managed], func(t *testing.T) {
			owner := openLifecycleApplicationOwner(t)
			original := testContent(t, "Original", true)
			catalogGuardSave(t, owner, original)
			if managed {
				catalogSchemaExec(t, owner.database.db, `INSERT INTO tc_catalog_managed VALUES (?,1,1)`, original.ID().String())
			}
			before := catalogV4Rows(t, owner.database.db)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			err := owner.WithinTransaction(ctx, func(r applicationcatalog.ContentRepository) error {
				calls++
				if err := r.Save(ctx, catalogGuardOtherContent(t)); err != nil {
					return err
				}
				cancel()
				return r.Save(ctx, testContent(t, "Canceled overwrite", false))
			})
			catalogGuardAssertError(t, err, context.Canceled)
			if calls != 1 || !reflect.DeepEqual(before, catalogV4Rows(t, owner.database.db)) {
				t.Fatal("cancellation replayed callback or retained partial writes")
			}
			catalogGuardAssertContent(t, owner, original)
		})
	}
}

func TestCatalogManagedSaveGuardSanitizedSchemaFailure(t *testing.T) {
	for _, damage := range []string{
		`DROP TABLE tc_catalog_managed`,
		`ALTER TABLE tc_catalog_managed RENAME COLUMN content_id TO private_identifier`,
		`UPDATE tc_schema_migrations SET dirty=1 WHERE version=5`,
	} {
		t.Run(damage, func(t *testing.T) {
			owner := openLifecycleApplicationOwner(t)
			original := testContent(t, "Original", true)
			catalogGuardSave(t, owner, original)
			catalogSchemaExec(t, owner.database.db, damage)
			before := catalogV4Rows(t, owner.database.db)
			err := owner.WithinTransaction(t.Context(), func(r applicationcatalog.ContentRepository) error {
				return r.Save(t.Context(), testContent(t, "Unsafe fallback", false))
			})
			catalogGuardAssertError(t, err, applicationcatalog.ErrRepositoryUnavailable)
			if !reflect.DeepEqual(before, catalogV4Rows(t, owner.database.db)) {
				t.Fatal("damaged v5 guard fell back to unmanaged write")
			}
		})
	}
}

func catalogGuardSave(t *testing.T, transactor applicationcatalog.Transactor, content domaincatalog.Content) {
	t.Helper()
	if err := transactor.WithinTransaction(t.Context(), func(r applicationcatalog.ContentRepository) error {
		return r.Save(t.Context(), content)
	}); err != nil {
		t.Fatal(err)
	}
}

func catalogGuardAssertContent(t *testing.T, transactor applicationcatalog.Transactor, want domaincatalog.Content) {
	t.Helper()
	if err := transactor.WithinTransaction(t.Context(), func(r applicationcatalog.ContentRepository) error {
		got, found, err := r.FindByID(t.Context(), want.ID())
		if err == nil && (!found || !reflect.DeepEqual(got, want)) {
			t.Fatal("persisted content differs from expected facts")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func catalogGuardOtherContent(t *testing.T) domaincatalog.Content {
	t.Helper()
	id, err := identity.ParseContentID(catalogSchemaID("cnt", 90))
	if err != nil {
		t.Fatal(err)
	}
	content, err := domaincatalog.NewContent(id, testContent(t, "Other content", false).Facts())
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func catalogGuardAssertError(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	var driverError *sqliteDriver.Error
	if errors.As(err, &driverError) {
		t.Fatal("SQLite error crossed application boundary")
	}
	for _, sentinel := range []error{sql.ErrNoRows, sql.ErrTxDone, sql.ErrConnDone} {
		if errors.Is(err, sentinel) {
			t.Fatal("database/sql error crossed application boundary")
		}
	}
	for _, detail := range []string{"tc_catalog", "tc_schema", "private", "no such", "SQLITE", contentIDText} {
		if strings.Contains(err.Error(), detail) {
			t.Fatalf("storage or identity detail leaked: %v", err)
		}
	}
}
