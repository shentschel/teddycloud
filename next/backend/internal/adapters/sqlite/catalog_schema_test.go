package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	applicationcatalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

func TestCatalogSchemaV4Upgrade(t *testing.T) {
	migrations := SchemaMigrations()[:5]
	if len(migrations) != 5 || migrations[4].Version != 5 || migrations[4].ID != "0005-catalog-provenance" {
		t.Fatal("unexpected catalog migration identity")
	}
	// Frozen before PI-08: changing even whitespace in an applied statement fails.
	checksums := []string{
		"23f6dda1108501084bff616ef2709e9596fe2bf684fd60e1b727ba698ba44d0b",
		"f36f56149dc69c4b0be984967cb568d57f9330d4ed275f59afbd1f673a3d3294",
		"104ff5813e462d75c6f0359c32030336d1397f8f16184e99e016762a25dfd571",
		"cd838271e3a1890210571d6b3634ca8067bcff6f3cd3b40937a0c2330b0c88ed",
	}
	for i, want := range checksums {
		if got := migrationChecksum(migrations[i]); got != want {
			t.Fatalf("v%d checksum drift: %s", i+1, got)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "v4.sqlite")
	owner, err := OpenLifecycleOwner(t.Context(), Config{Path: path}, migrations[:4])
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.WithinTransaction(t.Context(), func(r applicationcatalog.ContentRepository) error {
		return r.Save(t.Context(), testContent(t, "Original Ω title", true))
	}); err != nil {
		t.Fatal(err)
	}
	command := blobCommand(t, 1, true)
	if _, err := blobRecord(t, owner, blobKey(t, 1), command); err != nil {
		t.Fatal(err)
	}
	catalogSchemaExec(t, owner.database.db, `INSERT INTO tc_tags(tag_id,uid,revision) VALUES (?, ?, 7)`,
		catalogSchemaID("tag", 1), []byte{0, 255, 1, 2, 3, 4, 5, 6})
	catalogSchemaExec(t, owner.database.db, `INSERT INTO tc_catalog_content VALUES (?, ?, ?, ?)`,
		catalogSchemaID("cnt", 2), "Baseline\x00bytes", "", nil)
	catalogSchemaExec(t, owner.database.db, `INSERT INTO tc_content_versions SELECT ?,content_id,audio_id,audio_sha1,0,NULL,NULL FROM tc_content_versions`, catalogSchemaID("ver", 99))
	before := catalogV4Rows(t, owner.database.db)
	oldObjects := catalogSchemaRows(t, owner.database.db, `SELECT type,name,tbl_name,sql FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err := owner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	upgraded, err := OpenLifecycleOwner(t.Context(), Config{Path: path, UpgradeBackupPath: filepath.Join(dir, "pre-v5.sqlite")}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close(context.Background())
	if got, err := upgraded.CurrentVersion(t.Context()); err != nil || got != 5 {
		t.Fatalf("upgrade version: %d %v", got, err)
	}
	if got := catalogV4Rows(t, upgraded.database.db); !reflect.DeepEqual(got, before) {
		t.Fatal("v4 row types or exact bytes changed")
	}
	for _, object := range oldObjects {
		if !catalogSchemaContains(catalogSchemaRows(t, upgraded.database.db,
			`SELECT type,name,tbl_name,sql FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' ORDER BY name`), object) {
			t.Fatal("preexisting schema object changed")
		}
	}
	for _, table := range catalogFoundationTables {
		var count int
		if err := upgraded.database.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("migration fabricated catalog state in %s: %d %v", table, count, err)
		}
	}
	if result, found, err := blobLookup(t, upgraded, blobKey(t, 1), command); err != nil || !found || result != importResult(command) {
		t.Fatalf("original PI-07 replay: %v %v", found, err)
	}
	snapshot, found, err := upgraded.UpgradeBackup(t.Context())
	if err != nil || !found || snapshot.SchemaVersion != 4 {
		t.Fatalf("verified pre-v5 snapshot: %v %v", found, err)
	}
	if err := VerifyBackup(t.Context(), snapshot, migrations); err != nil {
		t.Fatal(err)
	}
	backup := openRawDatabase(t, snapshot.Path)
	if got := catalogV4Rows(t, backup); !reflect.DeepEqual(got, before) {
		t.Fatal("v4 snapshot rows changed")
	}
	if err := backup.Close(); err != nil {
		t.Fatal(err)
	}
	current, err := upgraded.database.CreateBackup(t.Context(), filepath.Join(dir, "current.sqlite"), migrations)
	if err != nil {
		t.Fatal(err)
	}
	if err := upgraded.Restore(t.Context(), snapshot, filepath.Join(dir, "restored-v4.sqlite")); err != nil {
		t.Fatal(err)
	}
	var catalogTables int
	if err := upgraded.database.db.QueryRowContext(t.Context(),
		`SELECT count(*) FROM sqlite_schema WHERE name='tc_catalog_managed'`).Scan(&catalogTables); err != nil || catalogTables != 0 {
		t.Fatalf("restore implicitly upgraded: %d %v", catalogTables, err)
	}
	if got, err := upgraded.CurrentVersion(t.Context()); err != nil || got != 4 {
		t.Fatalf("exact v4 restore: %d %v", got, err)
	}
	if _, found, err := blobLookup(t, upgraded, blobKey(t, 1), command); err != nil || !found {
		t.Fatalf("PI-07 unavailable after v4 restore: %v %v", found, err)
	}
	if err := upgraded.Restore(t.Context(), current, filepath.Join(dir, "restored-v5.sqlite")); err != nil {
		t.Fatal(err)
	}
	if got := catalogV4Rows(t, upgraded.database.db); !reflect.DeepEqual(got, before) {
		t.Fatal("v5 restore changed original rows")
	}
}

var catalogFoundationTables = []string{
	"tc_catalog_sources", "tc_catalog_source_claims", "tc_catalog_batches",
	"tc_catalog_records", "tc_catalog_managed", "tc_catalog_subjects",
	"tc_catalog_observations", "tc_catalog_decisions", "tc_catalog_decision_support",
}

func TestCatalogOnlyVersionSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog-only.sqlite")
	db, err := Open(t.Context(), Config{Path: path}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	cid, vid := catalogSchemaID("cnt", 1), catalogSchemaID("ver", 1)
	catalogSchemaExec(t, db.db, `INSERT INTO tc_catalog_content VALUES (?, 'Catalog only', NULL, NULL)`, cid)
	catalogSchemaExec(t, db.db, `INSERT INTO tc_content_versions VALUES (?, ?, 4294967295, ?, 0, NULL, NULL)`, vid, cid, bytes.Repeat([]byte{0xab}, 20))
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(t.Context(), Config{Path: path}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Schema-level proof, deliberately independent of the future catalog query adapter.
	var storedVersion, storedContent string
	var audio uint64
	var hash []byte
	if err := db.db.QueryRowContext(t.Context(), `SELECT version_id,content_id,audio_id,audio_sha1 FROM tc_content_versions WHERE version_id=?`, vid).
		Scan(&storedVersion, &storedContent, &audio, &hash); err != nil {
		t.Fatal(err)
	}
	contentID, err := identity.ParseContentID(storedContent)
	if err != nil {
		t.Fatal(err)
	}
	versionID, err := identity.ParseContentVersionID(storedVersion)
	if err != nil {
		t.Fatal(err)
	}
	audioID, err := catalog.NewAudioID(audio)
	if err != nil {
		t.Fatal(err)
	}
	audioHash, err := catalog.ParseAudioHash(hex.EncodeToString(hash))
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := catalog.NewAudioFingerprint(audioID, audioHash)
	if err != nil {
		t.Fatal(err)
	}
	version, err := catalog.NewContentVersion(versionID, contentID, fingerprint, catalog.UnknownVersionOrderEvidence())
	if err != nil || version.ID().String() != vid {
		t.Fatalf("catalog-only domain value: %v", err)
	}
	for _, table := range []string{"tc_blobs", "tc_version_blobs", "tc_blob_imports", "tc_catalog_decisions", "tc_catalog_managed"} {
		var count int
		if err := db.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("fabricated %s: %d %v", table, count, err)
		}
	}
	if err := verifyForeignKeys(t.Context(), db.db); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogSchemaRelations(t *testing.T) {
	db := catalogSchemaFixture(t)
	for _, table := range catalogFoundationTables {
		var strict int
		if err := db.db.QueryRowContext(t.Context(), `SELECT strict FROM pragma_table_list WHERE name=?`, table).Scan(&strict); err != nil || strict != 1 {
			t.Fatalf("not STRICT: %s %v", table, err)
		}
		rows, err := db.db.QueryContext(t.Context(), `SELECT on_delete FROM pragma_foreign_key_list(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var action string
			if err := rows.Scan(&action); err != nil {
				t.Fatal(err)
			}
			if action != "RESTRICT" {
				t.Fatalf("%s delete action: %s", table, action)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	before := catalogSchemaRows(t, db.db, `SELECT * FROM tc_catalog_decision_support ORDER BY command_id,observation_id`)
	for _, tc := range []struct {
		name  string
		query string
		args  []any
	}{
		{"missing managed content", `INSERT INTO tc_catalog_managed VALUES (?,1,0)`, []any{catalogSchemaID("cnt", 99)}},
		{"zero aggregate revision", `INSERT INTO tc_catalog_managed VALUES (?,0,0)`, []any{catalogSchemaID("cnt", 2)}},
		{"missing subject version", `INSERT INTO tc_catalog_subjects VALUES (?,?,?,1)`, []any{catalogSchemaID("ver", 99), catalogSchemaID("cnt", 1), catalogSchemaID("ver", 99)}},
		{"cross-content version", `INSERT INTO tc_catalog_subjects VALUES (?,?,?,1)`, []any{catalogSchemaID("ver", 2), catalogSchemaID("cnt", 1), catalogSchemaID("ver", 2)}},
		{"outside record count", `INSERT INTO tc_catalog_records VALUES (?,2,'outside',?)`, []any{strings.Repeat("4", 32), []byte("{}")}},
		{"missing batch", `INSERT INTO tc_catalog_records VALUES (?,0,'missing',?)`, []any{strings.Repeat("9", 32), []byte("{}")}},
		{"oversized snapshot", `INSERT INTO tc_catalog_records VALUES (?,1,'large',zeroblob(1025))`, []any{strings.Repeat("4", 32)}},
		{"text snapshot", `INSERT INTO tc_catalog_records VALUES (?,1,'text','{}')`, []any{strings.Repeat("4", 32)}},
		{"missing support", `INSERT INTO tc_catalog_decision_support VALUES (?,?,?,?,?,?)`, []any{catalogSchemaID("cnt", 1), strings.Repeat("1", 32), strings.Repeat("2", 32), 1, strings.Repeat("5", 32), catalogSchemaID("obs", 99)}},
		{"wrong support subject", `INSERT INTO tc_catalog_decision_support VALUES (?,?,?,?,?,?)`, []any{catalogSchemaID("cnt", 2), strings.Repeat("1", 32), strings.Repeat("2", 32), 1, strings.Repeat("5", 32), catalogSchemaID("obs", 2)}},
		{"wrong support field", `INSERT INTO tc_catalog_decision_support VALUES (?,?,?,?,?,?)`, []any{catalogSchemaID("cnt", 1), strings.Repeat("1", 32), strings.Repeat("2", 32), 2, strings.Repeat("5", 32), catalogSchemaID("obs", 1)}},
		{"wrong source revision", `INSERT INTO tc_catalog_decision_support VALUES (?,?,?,?,?,?)`, []any{catalogSchemaID("cnt", 1), strings.Repeat("1", 32), strings.Repeat("8", 32), 1, strings.Repeat("5", 32), catalogSchemaID("obs", 1)}},
		{"receipt without binding", `INSERT INTO tc_blob_imports VALUES (?,1,zeroblob(137),zeroblob(32),?)`, []any{catalogSchemaID("imp", 1), catalogSchemaID("ver", 1)}},
		{"binding without blob", `INSERT INTO tc_version_blobs VALUES (?,zeroblob(32))`, []any{catalogSchemaID("ver", 1)}},
		{"delete content", `DELETE FROM tc_catalog_content`, nil},
		{"unprotect", `UPDATE tc_catalog_managed SET protected_custom=0`, nil},
		{"rebind source", `UPDATE tc_catalog_sources SET issuer_principal=?`, []any{strings.Repeat("9", 32)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.db.ExecContext(t.Context(), tc.query, tc.args...); err == nil {
				t.Fatal("invalid relation accepted")
			}
		})
	}
	if got := catalogSchemaRows(t, db.db, `SELECT * FROM tc_catalog_decision_support ORDER BY command_id,observation_id`); !reflect.DeepEqual(got, before) {
		t.Fatal("failed relations changed support")
	}
	catalogSchemaExec(t, db.db, `UPDATE tc_catalog_sources SET revoked=1`)
	if _, err := db.db.ExecContext(t.Context(), `UPDATE tc_catalog_sources SET revoked=0`); err == nil {
		t.Fatal("revoked source resurrected")
	}
	for _, table := range []string{"tc_catalog_batches", "tc_catalog_records", "tc_catalog_observations", "tc_catalog_decisions", "tc_catalog_decision_support"} {
		if _, err := db.db.ExecContext(t.Context(), "DELETE FROM "+table); err == nil {
			t.Fatalf("retained history deleted: %s", table)
		}
	}
	if err := verifyForeignKeys(t.Context(), db.db); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogSchemaTypedClaims(t *testing.T) {
	db := catalogSchemaFixture(t)
	// Retain a wider uint64 fingerprint candidate without narrowing it to the
	// existing uint32 ContentVersion column.
	catalogSchemaExec(t, db.db, `INSERT INTO tc_catalog_observations VALUES (?,?,?,4,NULL,?, ?,NULL,?,0,?,?,'','2026-10-14T00:00:00Z',0)`,
		catalogSchemaID("obs", 3), catalogSchemaID("ver", 1), catalogSchemaID("cnt", 1),
		bytes.Repeat([]byte{255}, 8), bytes.Repeat([]byte{1}, 20),
		strings.Repeat("4", 32), strings.Repeat("1", 32), strings.Repeat("2", 32))
	catalogSchemaExec(t, db.db, `INSERT INTO tc_catalog_observations SELECT ?,subject_id,content_id,2,'01-0100',NULL,NULL,NULL,batch_id,record_index,source_id,registration_revision,asserted_origin,observed_at,confidence FROM tc_catalog_observations WHERE observation_id=?`,
		catalogSchemaID("obs", 4), catalogSchemaID("obs", 1))
	columns := []string{"observation_id", "subject_id", "content_id", "claim_key", "text_value", "audio_id", "audio_sha1", "owner_content_id", "batch_id", "record_index", "source_id", "registration_revision", "asserted_origin", "observed_at", "confidence"}
	for _, tc := range []struct {
		name   string
		base   int
		column string
		value  string
	}{
		{"unknown claim", 1, "claim_key", "6"},
		{"invalid model alphabet", 4, "text_value", "'invalid model'"},
		{"absent title", 1, "text_value", "NULL"},
		{"empty title", 1, "text_value", "''"},
		{"oversized title", 1, "text_value", "printf('%4097s','x')"},
		{"title with NUL", 1, "text_value", "'x'||char(0)"},
		{"title plus fingerprint", 1, "audio_id", "zeroblob(8)"},
		{"invalid confidence", 1, "confidence", "3"},
		{"missing coordinate", 1, "record_index", "1"},
		{"wrong batch source", 1, "source_id", "'99999999999999999999999999999999'"},
		{"text fingerprint", 3, "audio_id", "'12345678'"},
		{"zero fingerprint", 3, "audio_id", "zeroblob(8)"},
		{"short fingerprint", 3, "audio_id", "zeroblob(7)"},
		{"unpaired fingerprint", 3, "audio_sha1", "NULL"},
		{"malformed SHA1", 3, "audio_sha1", "zeroblob(19)"},
		{"fingerprint plus editorial", 3, "text_value", "'Title'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected := append([]string(nil), columns...)
			selected[0] = "'" + catalogSchemaID("obs", 99) + "'"
			for i, column := range columns {
				if column == tc.column {
					selected[i] = tc.value
				}
			}
			query := "INSERT INTO tc_catalog_observations SELECT " + strings.Join(selected, ",") + " FROM tc_catalog_observations WHERE observation_id=?"
			if _, err := db.db.ExecContext(t.Context(), query, catalogSchemaID("obs", tc.base)); err == nil {
				t.Fatal("malformed typed claim accepted")
			}
		})
	}
	// Imported source labels cannot select an unadmitted acquisition or grant
	// public artifacts ownership authority.
	for _, query := range []string{
		`INSERT INTO tc_catalog_sources SELECT '99999999999999999999999999999999',registration_revision,issuer_principal,adapter,adapter_version,scope,6,0 FROM tc_catalog_sources`,
		`INSERT INTO tc_catalog_sources SELECT 'provider-authenticated',registration_revision,issuer_principal,adapter,adapter_version,scope,1,0 FROM tc_catalog_sources`,
		`INSERT INTO tc_catalog_source_claims VALUES ('11111111111111111111111111111111','22222222222222222222222222222222',1,5)`,
		`INSERT INTO tc_catalog_decisions SELECT '99999999999999999999999999999999',subject_id,content_id,source_id,registration_revision,reviewer_principal,policy_revision,kind,claim_key,0,1,2,3,reason,canonical_command,canonical_result FROM tc_catalog_decisions`,
		`INSERT INTO tc_catalog_decisions SELECT '99999999999999999999999999999999',subject_id,content_id,source_id,registration_revision,reviewer_principal,policy_revision,kind,claim_key,9223372036854775807,9223372036854775807,2,3,reason,canonical_command,canonical_result FROM tc_catalog_decisions`,
	} {
		if _, err := db.db.ExecContext(t.Context(), query); err == nil {
			t.Fatal("invalid authority or revision representation accepted")
		}
	}
	if err := verifyForeignKeys(t.Context(), db.db); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogCorruptSchemaRelations(t *testing.T) {
	db := catalogSchemaFixture(t)
	catalogSchemaExec(t, db.db, `PRAGMA foreign_keys=OFF`)
	catalogSchemaExec(t, db.db, `INSERT INTO tc_catalog_decision_support VALUES (?,?,?,?,?,?)`,
		catalogSchemaID("cnt", 1), strings.Repeat("1", 32), strings.Repeat("2", 32), 1, strings.Repeat("5", 32), catalogSchemaID("obs", 99))
	catalogSchemaExec(t, db.db, `PRAGMA foreign_keys=ON`)
	assertInvalidForeignKeyBackup(t, verifyForeignKeys(t.Context(), db.db))
	snapshot, err := db.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "rejected.sqlite"), SchemaMigrations())
	assertInvalidForeignKeyBackup(t, err)
	if snapshot != (BackupFile{}) {
		t.Fatal("dangling support published as a verified snapshot")
	}
}

func catalogSchemaFixture(t *testing.T) *Database {
	return catalogSchemaFixtureMigrations(t, SchemaMigrations())
}

func catalogSchemaFixtureMigrations(t *testing.T, migrations []Migration) *Database {
	t.Helper()
	db, err := Open(t.Context(), Config{Path: filepath.Join(t.TempDir(), "catalog.sqlite")}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	source, revision, issuer, batch := strings.Repeat("1", 32), strings.Repeat("2", 32), strings.Repeat("3", 32), strings.Repeat("4", 32)
	catalogSchemaExec(t, db.db, `INSERT INTO tc_catalog_sources VALUES (?,?,?,'core-entry','1','synthetic',3,0)`, source, revision, issuer)
	for _, key := range []int{1, 2, 3, 4, 5} {
		catalogSchemaExec(t, db.db, `INSERT INTO tc_catalog_source_claims VALUES (?,?,3,?)`, source, revision, key)
	}
	catalogSchemaExec(t, db.db, `INSERT INTO tc_catalog_batches VALUES (?,?,?,?,1024,2,'test/1','')`, batch, source, revision, bytes.Repeat([]byte{1}, 32))
	catalogSchemaExec(t, db.db, `INSERT INTO tc_catalog_records VALUES (?,0,'record:0',?)`, batch, []byte(`{"title":"Candidate","cover":"https://invalid.example/unfetched"}`))
	for _, n := range []int{1, 2} {
		cid, vid := catalogSchemaID("cnt", n), catalogSchemaID("ver", n)
		catalogSchemaExec(t, db.db, `INSERT INTO tc_catalog_content VALUES (?,'Baseline',NULL,NULL)`, cid)
		catalogSchemaExec(t, db.db, `INSERT INTO tc_catalog_managed VALUES (?,1,1)`, cid)
		catalogSchemaExec(t, db.db, `INSERT INTO tc_catalog_subjects VALUES (?,?,NULL,1)`, cid, cid)
		// Independent identities may share every external fingerprint.
		catalogSchemaExec(t, db.db, `INSERT INTO tc_content_versions VALUES (?,?,123,?,0,NULL,NULL)`, vid, cid, bytes.Repeat([]byte{1}, 20))
		catalogSchemaExec(t, db.db, `INSERT INTO tc_catalog_observations VALUES (?,?,?,1,'Candidate',NULL,NULL,NULL,?,0,?,?,?,'2026-10-14T00:00:00Z',0)`,
			catalogSchemaID("obs", n), cid, cid, batch, source, revision, "asserted publisher")
	}
	catalogSchemaExec(t, db.db, `INSERT INTO tc_catalog_subjects VALUES (?,?,?,1)`, catalogSchemaID("ver", 1), catalogSchemaID("cnt", 1), catalogSchemaID("ver", 1))
	catalogSchemaExec(t, db.db, `INSERT INTO tc_catalog_decisions VALUES (?,?,?,?,?,'33333333333333333333333333333333','pi08-catalog/1',2,1,1,2,1,2,'Explicit review',?,?)`,
		strings.Repeat("5", 32), catalogSchemaID("cnt", 1), catalogSchemaID("cnt", 1), source, revision, []byte("canonical command"), []byte("original result"))
	catalogSchemaExec(t, db.db, `INSERT INTO tc_catalog_decision_support VALUES (?,?,?,?,?,?)`,
		catalogSchemaID("cnt", 1), source, revision, 1, strings.Repeat("5", 32), catalogSchemaID("obs", 1))
	return db
}

func catalogSchemaID(prefix string, n int) string {
	return fmt.Sprintf("%s_%026d", prefix, n)
}

func catalogSchemaExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

// Record storage classes and exact bytes, including NULL, empty strings,
// embedded NUL, binary command/fingerprint bytes and every migration ledger field.
func catalogV4Rows(t *testing.T, db *sql.DB) map[string][][]any {
	t.Helper()
	result := make(map[string][][]any)
	for _, table := range []string{"tc_catalog_content", "tc_tags", "tc_tag_observations", "tc_tag_decisions", "tc_tag_decision_support", "tc_blobs", "tc_content_versions", "tc_version_blobs", "tc_blob_imports"} {
		result[table] = catalogSchemaRows(t, db, "SELECT * FROM "+table+" ORDER BY 1")
	}
	result["tc_schema_migrations"] = catalogSchemaRows(t, db, "SELECT * FROM tc_schema_migrations WHERE version<=4 ORDER BY version")
	return result
}

func catalogSchemaRows(t *testing.T, db *sql.DB, query string) [][]any {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result [][]any
	for rows.Next() {
		values, targets := make([]any, len(columns)), make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		for i, value := range values {
			if b, ok := value.([]byte); ok {
				values[i] = append([]byte{}, b...)
			}
		}
		result = append(result, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func catalogSchemaContains(rows [][]any, want []any) bool {
	for _, row := range rows {
		if reflect.DeepEqual(row, want) {
			return true
		}
	}
	return false
}
