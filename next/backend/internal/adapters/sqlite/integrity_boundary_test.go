package sqlite

import (
	"bytes"
	"errors"
	applicationcatalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenRejectsDamagedContentPageWithoutMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "damaged-page.sqlite")
	database, err := Open(t.Context(), Config{Path: path}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	if err := database.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		return repository.Save(t.Context(), testContent(t, "Retained", true))
	}); err != nil {
		t.Fatal(err)
	}
	var pageSize, rootPage int
	if err := database.db.QueryRowContext(t.Context(), "PRAGMA page_size").Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(t.Context(), "SELECT rootpage FROM sqlite_schema WHERE name='tc_catalog_content'").Scan(&rootPage); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	offset := (rootPage - 1) * pageSize
	if rootPage <= 1 || offset >= len(before) {
		t.Fatal("invalid corruption fixture page")
	}
	before[offset] = 0xff
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = Open(t.Context(), Config{Path: path}, SchemaMigrations())
	if !errors.Is(err, ErrDatabaseInvalid) {
		t.Fatalf("damagedpage error = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("damaged source bytes changed")
	}
	// A read-only SQLite connection may create WAL coordination sidecars.
	// The damaged main file must remain byte-identical; existing sidecars are
	// never removed by the preflight.
}

func TestOpenRejectsUnownedSQLiteLikeTableName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unowned.sqlite")
	raw := openRawDatabase(t, path)
	if _, err := raw.ExecContext(t.Context(), "CREATE TABLE sqliteXforeign (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := Open(t.Context(), Config{Path: path}, SchemaMigrations())
	if !errors.Is(err, ErrDatabaseUnowned) {
		t.Fatalf("ownership prefix error = %v", err)
	}
	assertNoDatabaseSidecars(t, path)
}

func TestNilLifecycleOwnerUsesApplicationFailure(t *testing.T) {
	var owner *LifecycleOwner
	err := owner.WithinTransaction(t.Context(), func(applicationcatalog.ContentRepository) error { t.Fatal("nilowner callback"); return nil })
	assertApplicationRepositoryFailure(t, err)
}
