package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	applicationcatalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/application/contentstore"
)

func TestBlobSchemaRecovery(t *testing.T) {
	for _, version := range []int{1, 2, 3} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "db.sqlite")
			migrations := SchemaMigrations()
			if len(migrations) != 4 || migrations[3].Version != 4 || migrations[3].ID != "0004-content-blobs" {
				t.Fatal("unexpected migration identity")
			}
			db, err := Open(t.Context(), Config{Path: path}, migrations[:version])
			if err != nil {
				t.Fatal(err)
			}
			if err := db.WithinTransaction(t.Context(), func(r applicationcatalog.ContentRepository) error {
				return r.Save(t.Context(), testContent(t, "Pre-v4", false))
			}); err != nil {
				t.Fatal(err)
			}
			before := make([]string, version)
			for i := range before {
				before[i] = tagSchemaLedgerChecksum(t, db, i+1)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			owner, err := OpenLifecycleOwner(t.Context(), Config{Path: path, UpgradeBackupPath: filepath.Join(directory, "pre.sqlite")}, migrations)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = owner.Close(context.Background()) })
			for i, want := range before {
				if got := tagSchemaLedgerChecksum(t, owner.database, i+1); got != want {
					t.Fatal("old checksum changed")
				}
			}
			c := blobCommand(t, 1, false)
			key := blobKey(t, 1)
			if _, err := blobRecord(t, owner, key, c); err != nil {
				t.Fatal(err)
			}
			current, err := owner.database.CreateBackup(t.Context(), filepath.Join(directory, "current.sqlite"), migrations)
			if err != nil {
				t.Fatal(err)
			}
			old, found, err := owner.UpgradeBackup(t.Context())
			if err != nil || !found || old.SchemaVersion != version {
				t.Fatalf("pre-upgrade: %v %v", found, err)
			}
			if err := owner.Restore(t.Context(), old, filepath.Join(directory, "restored-old.sqlite")); err != nil {
				t.Fatal(err)
			}
			entered := false
			err = owner.WithinContentOperation(t.Context(), func(context.Context, contentstore.Session) error { entered = true; return nil })
			if !errors.Is(err, contentstore.ErrSchemaUnavailable) || entered {
				t.Fatalf("older schema admitted: %v", err)
			}
			if got, err := owner.CurrentVersion(t.Context()); err != nil || got != version {
				t.Fatalf("implicit migration: %d %v", got, err)
			}
			assertOwnerTitle(t, owner, "Pre-v4")
			if err := owner.Restore(t.Context(), current, filepath.Join(directory, "restored-current.sqlite")); err != nil {
				t.Fatal(err)
			}
			result, found, err := blobLookup(t, owner, key, c)
			if err != nil || !found || result != importResult(c) {
				t.Fatalf("current restore: %v %v", found, err)
			}
			if err := VerifyBackup(t.Context(), current, migrations); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBlobSchemaConstraints(t *testing.T) {
	owner := blobOwner(t)
	c := blobCommand(t, 1, true)
	if _, err := blobRecord(t, owner, blobKey(t, 1), c); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"tc_blobs", "tc_content_versions", "tc_version_blobs", "tc_blob_imports"} {
		var strict int
		if err := owner.database.db.QueryRowContext(t.Context(), `SELECT strict FROM pragma_table_list WHERE name=?`, table).Scan(&strict); err != nil || strict != 1 {
			t.Fatalf("not STRICT: %s %v", table, err)
		}
		rows, err := owner.database.db.QueryContext(t.Context(), `SELECT on_delete FROM pragma_foreign_key_list(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var action string
			if err := rows.Scan(&action); err != nil {
				t.Fatal(err)
			}
			if action != "RESTRICT" {
				t.Fatalf("FK %s: %s", table, action)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	for _, q := range []string{
		`UPDATE tc_blobs SET algorithm=2`, `UPDATE tc_blobs SET size=4096`, `UPDATE tc_blobs SET size=1073741825`, `UPDATE tc_blobs SET profile=2`, `UPDATE tc_blobs SET digest=zeroblob(31)`, `UPDATE tc_blobs SET digest='text'`,
		`UPDATE tc_content_versions SET audio_id=0`, `UPDATE tc_content_versions SET audio_id=4294967296`, `UPDATE tc_content_versions SET audio_sha1=zeroblob(19)`, `UPDATE tc_content_versions SET order_known=2`,
		`UPDATE tc_content_versions SET order_namespace=''`, `UPDATE tc_content_versions SET order_namespace=char(0)`, `UPDATE tc_content_versions SET order_namespace=char(32)`, `UPDATE tc_content_versions SET order_position=zeroblob(7)`, `UPDATE tc_content_versions SET order_position=NULL`, `UPDATE tc_content_versions SET order_known=0`,
		`UPDATE tc_content_versions SET version_id='ver_0000000000000000000000000u'`, `UPDATE tc_content_versions SET version_id='VER_00000000000000000000000001'`, `UPDATE tc_content_versions SET content_id='cnt_00000000000000000000000099'`,
		`UPDATE tc_version_blobs SET digest=zeroblob(32)`, `UPDATE tc_blob_imports SET encoding=2`, `UPDATE tc_blob_imports SET command=zeroblob(136)`, `UPDATE tc_blob_imports SET command=zeroblob(276)`, `UPDATE tc_blob_imports SET fingerprint=zeroblob(31)`, `UPDATE tc_blob_imports SET import_key='imp_0000000000000000000000000u'`,
		`UPDATE tc_blob_imports SET import_key='imp_'||char(0)||'0000000000000000000000000'`,
		`DELETE FROM tc_catalog_content`, `DELETE FROM tc_content_versions`, `DELETE FROM tc_version_blobs`, `DELETE FROM tc_blobs`,
	} {
		t.Run(q, func(t *testing.T) {
			if _, err := owner.database.db.ExecContext(t.Context(), q); err == nil {
				t.Fatal("invalid state accepted")
			}
		})
	}
	assertBlobCounts(t, owner, 1, 1, 1, 1)
}
