package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// C1 tests SQL storage fences using deliberate fixtures, not the C2 repository
// or the D authoring milestone. Header-only states are intentionally not domain
// encodings. Semantic replay, canonical comparison, SHA verification, at least
// one operator, complete manifests/accounting and trusted authorization remain
// C2 requirements. None of these fixtures is registration-command evidence.
var registrationSchemaTables = []string{
	"tc_catalog_registrations", "tc_catalog_registration_sources", "tc_catalog_registration_support",
	"tc_catalog_review_states_v2", "tc_catalog_order_states_v2", "tc_catalog_order_state_versions_v2",
}

func registrationSchemaRows(t *testing.T, db *sql.DB) map[string][][]any {
	t.Helper()
	objects := catalogSchemaRows(t, db, "SELECT name FROM sqlite_schema WHERE type='table' AND name LIKE 'tc_%' ORDER BY name")
	result := make(map[string][][]any)
	for _, object := range objects {
		table := object[0].(string)
		result[table] = catalogSchemaRows(t, db, "SELECT * FROM "+table+" ORDER BY 1,2")
	}
	return result
}

func registrationReject(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	before := registrationSchemaRows(t, db)
	if _, err := db.ExecContext(t.Context(), query, args...); err == nil {
		t.Fatalf("invalid mutation accepted: %s", query)
	}
	if got := registrationSchemaRows(t, db); !reflect.DeepEqual(got, before) {
		t.Fatalf("rejected mutation changed database: %s", query)
	}
}

func registrationEnvelope(kind string) []byte {
	tag := []byte("pi08-" + kind + "-state/2")
	raw := make([]byte, 8+len(tag))
	binary.BigEndian.PutUint64(raw, uint64(len(tag)))
	copy(raw[8:], tag)
	return raw
}

func registrationSchemaInsert(t *testing.T, db *sql.DB, kind int, key string) {
	t.Helper()
	cid, subject, expected, order := catalogSchemaID("cnt", 1), catalogSchemaID("cnt", 1), 0, 1
	if kind == 2 {
		subject, expected, order = catalogSchemaID("ver", 1), 1, 2
		catalogSchemaExec(t, db, "UPDATE tc_catalog_managed SET aggregate_revision=2 WHERE content_id=?", cid)
	}
	catalogSchemaExec(t, db, "INSERT INTO tc_catalog_registrations VALUES (?,?,?,?,?,?,?,1,?,1,?,?)",
		key, kind, subject, cid, kind-1, expected, expected+1, order, []byte("command"), []byte("result"))
}

func registrationSchemaSource(t *testing.T, db *sql.DB, key string) {
	t.Helper()
	catalogSchemaExec(t, db, "INSERT INTO tc_catalog_registration_sources VALUES (?,?,?,?,1)",
		key, strings.Repeat("1", 32), strings.Repeat("2", 32), strings.Repeat("3", 32))
}

func registrationSchemaState(t *testing.T, db *sql.DB, kind string, aggregate, revision int, bootstrap string) {
	t.Helper()
	raw := registrationEnvelope(kind)
	sum := sha256.Sum256(raw)
	cid := catalogSchemaID("cnt", 1)
	if kind == "review" {
		catalogSchemaExec(t, db, "INSERT INTO tc_catalog_review_states_v2 VALUES (?,?,?,?,2,?,?,?,?)",
			cid, cid, aggregate, revision, raw, len(raw), sum[:], bootstrap)
	} else {
		catalogSchemaExec(t, db, "INSERT INTO tc_catalog_order_states_v2 VALUES (?,?,?,2,?,?,?)",
			cid, aggregate, revision, raw, len(raw), sum[:])
	}
}

func registrationSchemaFixture(t *testing.T) *Database {
	t.Helper()
	db := catalogSchemaFixture(t)
	key, cid := strings.Repeat("6", 32), catalogSchemaID("cnt", 1)
	registrationSchemaInsert(t, db.db, 1, key)
	registrationSchemaSource(t, db.db, key)
	catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_registration_support VALUES (?,?,?,?,?,1)",
		key, catalogSchemaID("obs", 1), cid, strings.Repeat("1", 32), strings.Repeat("2", 32))
	registrationSchemaState(t, db.db, "review", 1, 1, key)
	catalogStateInsert(t, db, "order", 1, 1)
	catalogSchemaExec(t, db.db, "UPDATE tc_catalog_managed SET aggregate_revision=2 WHERE content_id=?", cid)
	registrationSchemaState(t, db.db, "order", 2, 2, "")
	catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_order_state_versions_v2 VALUES (?,2,?)", cid, catalogSchemaID("ver", 1))
	return db
}

func TestRegistrationMigrationQuery(t *testing.T) {
	t.Run("real v7 upgrade and exact restore", func(t *testing.T) {
		migrations := SchemaMigrations()
		if len(migrations) != 8 || migrations[7].Version != 8 || migrations[7].ID != "0008-catalog-registration" {
			t.Fatal("migration 8 identity")
		}
		checksums := []string{
			"23f6dda1108501084bff616ef2709e9596fe2bf684fd60e1b727ba698ba44d0b",
			"f36f56149dc69c4b0be984967cb568d57f9330d4ed275f59afbd1f673a3d3294",
			"104ff5813e462d75c6f0359c32030336d1397f8f16184e99e016762a25dfd571",
			"cd838271e3a1890210571d6b3634ca8067bcff6f3cd3b40937a0c2330b0c88ed",
			"c65137b653d80e54301a14fd7f8be52bdb7e246ec32245623cbf47d7bfa66fc7",
			"f3bd874bbb9d8ae2228ba91c3d2b5d5077d0110d69d13551a24c87e54558fcd1",
			"2b40000e9e445357008b1935d490f3ec7f901741ed5d9bae0638f59092ed5727",
		}
		for i, want := range checksums {
			if got := migrationChecksum(migrations[i]); got != want {
				t.Fatalf("migration %d checksum changed: %s", i+1, got)
			}
		}
		db := catalogSchemaFixtureMigrations(t, migrations[:7])
		catalogSchemaExec(t, db.db, "INSERT INTO tc_tags VALUES (?,?,7)", catalogSchemaID("tag", 1), bytes.Repeat([]byte{1}, 8))
		catalogSchemaExec(t, db.db, "INSERT INTO tc_tag_observations VALUES (?,?,2,0,1,'legacy','1','0','2000-01-01T00:00:00.000000001Z',0,1)", catalogSchemaID("obs", 1), catalogSchemaID("tag", 1))
		catalogSchemaExec(t, db.db, "INSERT INTO tc_tag_decisions VALUES (?,?,0,2,3)", catalogSchemaID("dec", 1), catalogSchemaID("tag", 1))
		catalogSchemaExec(t, db.db, "INSERT INTO tc_tag_decision_support VALUES (?,?,?)", catalogSchemaID("tag", 1), catalogSchemaID("dec", 1), catalogSchemaID("obs", 1))
		// A real legacy PI-07 command supplies nonempty receipt, binding and
		// blob rows, with complete canonical bytes and known original order.
		catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_content VALUES (?,'Legacy receipt',NULL,NULL)", testContent(t, "", false).ID().String())
		legacy := blobCommand(t, 99, true)
		tx, err := db.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := (&blobRepository{transaction: tx, ctx: t.Context()}).RecordImport(t.Context(), blobKey(t, 99), legacy); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_imports VALUES (?, ?, ?, 4)", strings.Repeat("4", 32), []byte("{}"), []byte("{}"))
		catalogStateInsert(t, db, "review", 1, 1)
		catalogStateInsert(t, db, "order", 1, 1)
		catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_order_state_versions VALUES (?,1,?)", catalogSchemaID("cnt", 1), catalogSchemaID("ver", 1))
		before := registrationSchemaRows(t, db.db)
		objects := catalogSchemaRows(t, db.db, catalogRecoveryObjects)
		var seq int
		var name, path string
		if err := db.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		owner, err := OpenLifecycleOwner(t.Context(), Config{Path: path, UpgradeBackupPath: filepath.Join(dir, "v7.sqlite")}, migrations)
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Close(context.Background())
		if version, err := owner.CurrentVersion(t.Context()); err != nil || version != 8 {
			t.Fatal("upgrade version", version, err)
		}
		after := registrationSchemaRows(t, owner.database.db)
		for table, rows := range before {
			got := after[table]
			if table == "tc_schema_migrations" {
				got = catalogSchemaRows(t, owner.database.db, "SELECT * FROM tc_schema_migrations WHERE version<=7 ORDER BY 1,2")
			}
			if !reflect.DeepEqual(got, rows) {
				t.Fatalf("upgrade rewrote %s", table)
			}
		}
		for _, object := range objects {
			if !catalogSchemaContains(catalogSchemaRows(t, owner.database.db, catalogRecoveryObjects), object) {
				t.Fatal("upgrade changed an existing schema object")
			}
		}
		for _, table := range registrationSchemaTables {
			if len(after[table]) != 0 {
				t.Fatalf("migration fabricated %s", table)
			}
			var strict int
			if err := owner.database.db.QueryRowContext(t.Context(), "SELECT strict FROM pragma_table_list WHERE name=?", table).Scan(&strict); err != nil || strict != 1 {
				t.Fatalf("non-STRICT table %s: %v", table, err)
			}
			for _, row := range catalogSchemaRows(t, owner.database.db, "SELECT on_update,on_delete FROM pragma_foreign_key_list('"+table+"')") {
				if row[0] != "RESTRICT" || row[1] != "RESTRICT" {
					t.Fatalf("non-restrictive FK: %s %v", table, row)
				}
			}
		}
		fresh, err := Open(t.Context(), Config{Path: filepath.Join(dir, "fresh.sqlite")}, migrations)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := catalogSchemaRows(t, owner.database.db, catalogRecoveryObjects), catalogSchemaRows(t, fresh.db, catalogRecoveryObjects); !reflect.DeepEqual(got, want) {
			t.Fatal("upgrade/fresh schemas differ")
		}
		fresh.Close()
		pre, found, err := owner.UpgradeBackup(t.Context())
		if err != nil || !found || pre.SchemaVersion != 7 {
			t.Fatal("missing verified v7 snapshot", err)
		}
		current, err := owner.database.CreateBackup(t.Context(), filepath.Join(dir, "v8.sqlite"), migrations)
		if err != nil {
			t.Fatal(err)
		}
		for i, snapshot := range []BackupFile{pre, current, pre} {
			if err := VerifyBackup(t.Context(), snapshot, migrations); err != nil {
				t.Fatal(err)
			}
			if err := owner.Restore(t.Context(), snapshot, filepath.Join(dir, fmt.Sprintf("restored-%d.sqlite", i))); err != nil {
				t.Fatal(err)
			}
			if version, err := owner.CurrentVersion(t.Context()); err != nil || version != snapshot.SchemaVersion {
				t.Fatal("restore silently migrated", version, err)
			}
			if got := registrationSchemaRows(t, owner.database.db); snapshot.SchemaVersion == 7 && !reflect.DeepEqual(got, before) {
				t.Fatal("v7 restore changed original rows")
			} else if snapshot.SchemaVersion == 8 && !reflect.DeepEqual(got, after) {
				t.Fatal("v8 restore changed rows")
			}
			if result, found, err := blobLookup(t, owner, blobKey(t, 99), legacy); err != nil || !found || result != importResult(legacy) {
				t.Fatal("original PI-07 receipt replay changed", found, err)
			}
		}
	})
	t.Run("migration rollback at every statement", func(t *testing.T) {
		migrations := SchemaMigrations()
		for stage := range migrations[7].Statements {
			t.Run(fmt.Sprint(stage), func(t *testing.T) {
				db := catalogSchemaFixtureMigrations(t, migrations[:7])
				before := registrationSchemaRows(t, db.db)
				objects := catalogSchemaRows(t, db.db, catalogRecoveryObjects)
				broken := migrations[7]
				broken.Statements = append(append([]string{}, broken.Statements[:stage+1]...), "INVALID SQL FOR ROLLBACK")
				if err := applyMigration(t.Context(), db.db, broken); err == nil {
					t.Fatal("injected migration failure accepted")
				}
				if got := catalogSchemaRows(t, db.db, catalogRecoveryObjects); !reflect.DeepEqual(got, objects) {
					t.Fatal("partial migration objects survived rollback")
				}
				for table, rows := range before {
					query := "SELECT * FROM " + table
					if table == "tc_schema_migrations" {
						query += " WHERE version<=7"
					}
					if !reflect.DeepEqual(catalogSchemaRows(t, db.db, query+" ORDER BY 1,2"), rows) {
						t.Fatal("failed migration rewrote", table)
					}
				}
				if _, err := inspectMigrationState(t.Context(), db.db, migrations); !errors.Is(err, ErrDirtyMigration) {
					t.Fatal("dirty upgrade did not fail closed", err)
				}
			})
		}
	})
	t.Run("dangling v2 manifest rejects backup", func(t *testing.T) {
		db := registrationSchemaFixture(t)
		catalogSchemaExec(t, db.db, "PRAGMA foreign_keys=OFF")
		catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_order_state_versions_v2 VALUES (?,2,?)", catalogSchemaID("cnt", 1), catalogSchemaID("ver", 99))
		catalogSchemaExec(t, db.db, "PRAGMA foreign_keys=ON")
		assertInvalidForeignKeyBackup(t, verifyForeignKeys(t.Context(), db.db))
		backup, err := db.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "rejected.sqlite"), SchemaMigrations())
		assertInvalidForeignKeyBackup(t, err)
		if backup != (BackupFile{}) {
			t.Fatal("corrupt manifest published")
		}
	})
}

func TestRegistrationSchemaConstraints(t *testing.T) {
	t.Run("registration shape and bounds", func(t *testing.T) {
		db := catalogSchemaFixture(t)
		key, cid := strings.Repeat("6", 32), catalogSchemaID("cnt", 1)
		columns := []string{"command_id", "kind", "subject_id", "content_id", "expected_kind", "expected_revision", "result_revision", "review_revision", "order_revision", "protected_custom", "canonical_command", "canonical_result"}
		values := []any{key, 1, cid, cid, 0, 0, 1, 1, 1, 1, []byte("command"), []byte("result")}
		for _, tc := range []struct {
			name, column string
			value        any
		}{
			{"zero key", "command_id", strings.Repeat("0", 32)}, {"short key", "command_id", "a"},
			{"upper key", "command_id", strings.Repeat("A", 32)}, {"nul key", "command_id", strings.Repeat("a", 31) + "\x00"},
			{"unknown kind", "kind", 3}, {"dangling subject", "subject_id", catalogSchemaID("cnt", 99)},
			{"cross content", "content_id", catalogSchemaID("cnt", 2)}, {"expected union", "expected_kind", 1},
			{"expected absence", "expected_revision", 1}, {"result revision", "result_revision", 2},
			{"review revision", "review_revision", 2}, {"order revision", "order_revision", 0},
			{"order limit", "order_revision", 1026}, {"protection", "protected_custom", 2},
			{"protection mismatch", "protected_custom", 0}, {"empty command", "canonical_command", []byte{}},
			{"large command", "canonical_command", make([]byte, 8388609)}, {"text command", "canonical_command", "command"},
			{"empty result", "canonical_result", []byte{}}, {"large result", "canonical_result", make([]byte, 8388609)},
		} {
			t.Run(tc.name, func(t *testing.T) {
				args := append([]any(nil), values...)
				for i, column := range columns {
					if column == tc.column {
						args[i] = tc.value
					}
				}
				registrationReject(t, db.db, "INSERT INTO tc_catalog_registrations VALUES (?,?,?,?,?,?,?,?,?,?,?,?)", args...)
			})
		}
		// Exact per-BLOB boundary; combined aggregate accounting belongs to C2.
		values[10], values[11] = make([]byte, 8388608), make([]byte, 8388608)
		catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_registrations VALUES (?,?,?,?,?,?,?,?,?,?,?,?)", values...)
	})
	t.Run("version expected union and binding", func(t *testing.T) {
		db := catalogSchemaFixture(t)
		cid, vid := catalogSchemaID("cnt", 1), catalogSchemaID("ver", 1)
		catalogSchemaExec(t, db.db, "UPDATE tc_catalog_managed SET aggregate_revision=2 WHERE content_id=?", cid)
		query := "INSERT INTO tc_catalog_registrations VALUES (?,2,?,?,1,?,?,1,2,1,x'01',x'02')"
		for _, revisions := range [][2]int64{{0, 1}, {-1, 0}, {1, 3}, {9223372036854775807, 9223372036854775807}} {
			registrationReject(t, db.db, query, strings.Repeat("6", 32), vid, cid, revisions[0], revisions[1])
		}
		registrationSchemaInsert(t, db.db, 2, strings.Repeat("6", 32))
		registrationReject(t, db.db, query, strings.Repeat("7", 32), vid, cid, 1, 2)
	})
	t.Run("known original version order cannot register", func(t *testing.T) {
		db := catalogSchemaFixture(t)
		cid, vid := catalogSchemaID("cnt", 1), catalogSchemaID("ver", 1)
		catalogSchemaExec(t, db.db, "UPDATE tc_catalog_managed SET aggregate_revision=2 WHERE content_id=?", cid)
		catalogSchemaExec(t, db.db, "UPDATE tc_content_versions SET order_known=1,order_namespace='explicit',order_position=? WHERE version_id=?", bytes.Repeat([]byte{1}, 8), vid)
		registrationReject(t, db.db, "INSERT INTO tc_catalog_registrations VALUES (?,2,?,?,1,1,2,1,2,1,x'01',x'02')", strings.Repeat("6", 32), vid, cid)
	})
	t.Run("all new history immutable including replace", func(t *testing.T) {
		db := registrationSchemaFixture(t)
		for _, table := range registrationSchemaTables {
			for _, query := range []string{
				"UPDATE " + table + " SET " + map[bool]string{true: "command_id=command_id", false: "content_id=content_id"}[strings.Contains(table, "registration")],
				"DELETE FROM " + table,
				"INSERT OR REPLACE INTO " + table + " SELECT * FROM " + table,
			} {
				registrationReject(t, db.db, query)
			}
		}
		registrationReject(t, db.db, "DELETE FROM tc_content_versions WHERE version_id=?", catalogSchemaID("ver", 1))
	})
}

func TestRegistrationSchemaRelations(t *testing.T) {
	key, source, revision, reviewer := strings.Repeat("6", 32), strings.Repeat("1", 32), strings.Repeat("2", 32), strings.Repeat("3", 32)
	t.Run("source and support subject guards", func(t *testing.T) {
		db := catalogSchemaFixture(t)
		registrationSchemaInsert(t, db.db, 1, key)
		offline, operator, revoked := strings.Repeat("8", 32), strings.Repeat("9", 32), strings.Repeat("a", 32)
		for _, row := range []struct {
			id                   string
			acquisition, revoked int
		}{{offline, 1, 0}, {operator, 3, 0}, {revoked, 3, 1}} {
			catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_sources VALUES (?,?,?,'test','1','catalog',?,?)", row.id, revision, reviewer, row.acquisition, row.revoked)
			catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_source_claims VALUES (?,?,?,1)", row.id, revision, row.acquisition)
		}
		query := "INSERT INTO tc_catalog_registration_sources VALUES (?,?,?,?,?)"
		for _, args := range [][]any{
			{key, "missing", revision, reviewer, 0}, {key, source, "wrong", reviewer, 0},
			{key, source, revision, strings.Repeat("0", 32), 1}, {key, source, revision, strings.Repeat("A", 32), 1},
			{key, source, revision, strings.Repeat("a", 31) + "\x00", 1}, {key, source, revision, reviewer, 2},
			{key, offline, revision, reviewer, 1}, {key, revoked, revision, reviewer, 0},
			{strings.Repeat("7", 32), source, revision, reviewer, 1},
		} {
			registrationReject(t, db.db, query, args...)
		}
		registrationSchemaSource(t, db.db, key)
		registrationReject(t, db.db, query, key, operator, revision, reviewer, 1)
		registrationReject(t, db.db, "INSERT OR REPLACE INTO tc_catalog_registration_sources VALUES (?,?,?,?,1)", key, operator, revision, reviewer)
		registrationReject(t, db.db, query, key, offline, revision, strings.Repeat("b", 32), 0)
		catalogSchemaExec(t, db.db, query, key, offline, revision, reviewer, 0)
		cid := catalogSchemaID("cnt", 1)
		support := "INSERT INTO tc_catalog_registration_support VALUES (?,?,?,?,?,?)"
		for _, args := range [][]any{
			{key, catalogSchemaID("obs", 99), cid, source, revision, 1},
			{key, catalogSchemaID("obs", 2), catalogSchemaID("cnt", 2), source, revision, 1},
			{key, catalogSchemaID("obs", 1), cid, offline, revision, 1},
			{key, catalogSchemaID("obs", 1), cid, source, strings.Repeat("a", 32), 1},
			{key, catalogSchemaID("obs", 1), cid, source, revision, 2},
			{key, catalogSchemaID("obs", 1), cid, source, revision, 6},
		} {
			registrationReject(t, db.db, support, args...)
		}
		catalogSchemaExec(t, db.db, support, key, catalogSchemaID("obs", 1), cid, source, revision, 1)
		catalogSchemaExec(t, db.db, "UPDATE tc_catalog_sources SET revoked=1 WHERE source_id=?", source)
		registrationReject(t, db.db, support, key, catalogSchemaID("obs", 1), cid, source, revision, 1)
		if err := verifyForeignKeys(t.Context(), db.db); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("reviewer agreement also when operator is last", func(t *testing.T) {
		db := catalogSchemaFixture(t)
		registrationSchemaInsert(t, db.db, 1, key)
		catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_registration_sources VALUES (?,?,?,?,0)", key, source, revision, reviewer)
		catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_sources SELECT ?,registration_revision,issuer_principal,adapter,adapter_version,scope,acquisition,revoked FROM tc_catalog_sources", strings.Repeat("8", 32))
		registrationReject(t, db.db, "INSERT INTO tc_catalog_registration_sources VALUES (?,?,?,?,1)", key, strings.Repeat("8", 32), revision, strings.Repeat("9", 32))
		catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_registration_sources VALUES (?,?,?,?,1)", key, strings.Repeat("8", 32), revision, reviewer)
	})
	t.Run("ownership support requires core operator evidence", func(t *testing.T) {
		db := catalogSchemaFixture(t)
		registrationSchemaInsert(t, db.db, 2, key)
		registrationSchemaSource(t, db.db, key)
		provider, batch := strings.Repeat("8", 32), strings.Repeat("9", 32)
		catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_sources VALUES (?,?,?,'provider','1','catalog',5,0)", provider, revision, reviewer)
		catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_source_claims VALUES (?,?,5,5)", provider, revision)
		catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_batches VALUES (?,?,?,?,2,1,'1','')", batch, provider, revision, bytes.Repeat([]byte{1}, 32))
		catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_records VALUES (?,0,'0',x'7b7d')", batch)
		catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_registration_sources VALUES (?,?,?,?,0)", key, provider, revision, reviewer)
		cid, vid, obs := catalogSchemaID("cnt", 1), catalogSchemaID("ver", 1), catalogSchemaID("obs", 99)
		catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_observations VALUES (?,?,?,5,NULL,NULL,NULL,?,?,0,?,?,'','2026-01-01T00:00:00Z',0)", obs, vid, cid, cid, batch, provider, revision)
		registrationReject(t, db.db, "INSERT INTO tc_catalog_registration_support VALUES (?,?,?,?,?,5)", key, obs, vid, provider, revision)
		catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_observations VALUES (?,?,?,5,NULL,NULL,NULL,?,?,0,?,?,'','2026-01-01T00:00:00Z',0)", catalogSchemaID("obs", 98), vid, cid, cid, strings.Repeat("4", 32), source, revision)
		catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_registration_support VALUES (?,?,?,?,?,5)", key, catalogSchemaID("obs", 98), vid, source, revision)
	})
	t.Run("source index exact bound", func(t *testing.T) {
		db := catalogSchemaFixture(t)
		registrationSchemaInsert(t, db.db, 1, key)
		registrationSchemaSource(t, db.db, key)
		for n := 0; n < 256; n++ {
			id := fmt.Sprintf("%032x", n+1000)
			catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_sources VALUES (?,?,?,'test','1','catalog',1,0)", id, revision, reviewer)
			if n < 255 {
				catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_registration_sources VALUES (?,?,?,?,0)", key, id, revision, reviewer)
			} else {
				registrationReject(t, db.db, "INSERT INTO tc_catalog_registration_sources VALUES (?,?,?,?,0)", key, id, revision, reviewer)
			}
		}
	})
	t.Run("support index exact bound", func(t *testing.T) {
		db := catalogSchemaFixture(t)
		registrationSchemaInsert(t, db.db, 1, key)
		registrationSchemaSource(t, db.db, key)
		cid := catalogSchemaID("cnt", 1)
		for n := 0; n < 257; n++ {
			id := catalogSchemaID("obs", 1000+n)
			catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_observations SELECT ?,subject_id,content_id,claim_key,text_value,audio_id,audio_sha1,owner_content_id,batch_id,record_index,source_id,registration_revision,asserted_origin,observed_at,confidence FROM tc_catalog_observations WHERE observation_id=?", id, catalogSchemaID("obs", 1))
			query := "INSERT INTO tc_catalog_registration_support VALUES (?,?,?,?,?,1)"
			if n < 256 {
				catalogSchemaExec(t, db.db, query, key, id, cid, source, revision)
			} else {
				registrationReject(t, db.db, query, key, id, cid, source, revision)
			}
		}
	})
	t.Run("manifest relations and exact bound", func(t *testing.T) {
		db := registrationSchemaFixture(t)
		cid := catalogSchemaID("cnt", 1)
		for _, args := range [][]any{{cid, 2, catalogSchemaID("ver", 99)}, {cid, 2, catalogSchemaID("ver", 2)}, {cid, 3, catalogSchemaID("ver", 1)}} {
			registrationReject(t, db.db, "INSERT INTO tc_catalog_order_state_versions_v2 VALUES (?,?,?)", args...)
		}
		for n := 0; n < 1024; n++ {
			vid := catalogSchemaID("ver", 1000+n)
			catalogSchemaExec(t, db.db, "INSERT INTO tc_content_versions VALUES (?,?,123,?,0,NULL,NULL)", vid, cid, bytes.Repeat([]byte{1}, 20))
			if n < 1023 {
				catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_order_state_versions_v2 VALUES (?,2,?)", cid, vid)
			} else {
				registrationReject(t, db.db, "INSERT INTO tc_catalog_order_state_versions_v2 VALUES (?,2,?)", cid, vid)
			}
		}
	})
}

func TestRegistrationSchemaAppend(t *testing.T) {
	key, cid := strings.Repeat("6", 32), catalogSchemaID("cnt", 1)
	t.Run("review bootstrap and generations", func(t *testing.T) {
		db := registrationSchemaFixture(t)
		catalogSchemaExec(t, db.db, "UPDATE tc_catalog_managed SET aggregate_revision=3 WHERE content_id=?", cid)
		catalogSchemaExec(t, db.db, "UPDATE tc_catalog_subjects SET review_revision=3 WHERE subject_id=?", cid)
		raw := registrationEnvelope("review")
		sum := sha256.Sum256(raw)
		query := "INSERT INTO tc_catalog_review_states_v2 VALUES (?,?,?,?,2,?,?,?,?)"
		registrationReject(t, db.db, query, cid, cid, 3, 3, raw, len(raw), sum[:], key)
		// Restore a fixture's live subject fence only by using another database;
		// production monotonic subject revisions must never be lowered.
	})
	t.Run("review contiguous append and no downgrade", func(t *testing.T) {
		db := registrationSchemaFixture(t)
		catalogSchemaExec(t, db.db, "UPDATE tc_catalog_managed SET aggregate_revision=3 WHERE content_id=?", cid)
		catalogSchemaExec(t, db.db, "UPDATE tc_catalog_subjects SET review_revision=2 WHERE subject_id=?", cid)
		raw := registrationEnvelope("review")
		sum := sha256.Sum256(raw)
		query := "INSERT INTO tc_catalog_review_states_v2 VALUES (?,?,?,?,2,?,?,?,?)"
		for _, args := range [][]any{
			{cid, cid, 3, 2, raw, len(raw), sum[:], strings.Repeat("7", 32)},
			{catalogSchemaID("cnt", 2), catalogSchemaID("cnt", 2), 1, 1, raw, len(raw), sum[:], key},
			{cid, cid, 2, 2, raw, len(raw), sum[:], key},
		} {
			registrationReject(t, db.db, query, args...)
		}
		registrationSchemaState(t, db.db, "review", 3, 2, key)
		catalogSchemaExec(t, db.db, "UPDATE tc_catalog_managed SET aggregate_revision=4 WHERE content_id=?", cid)
		catalogSchemaExec(t, db.db, "UPDATE tc_catalog_subjects SET review_revision=3 WHERE subject_id=?", cid)
		old := catalogStateBytes(t, "review", cid, "", 3)
		hash := sha256.Sum256(old)
		registrationReject(t, db.db, "INSERT INTO tc_catalog_review_states VALUES (?,?,4,3,1,?,?,?)", cid, cid, old, len(old), hash[:])
		registrationSchemaState(t, db.db, "review", 4, 3, key)
	})
	t.Run("review cannot convert a baseline subject", func(t *testing.T) {
		db := catalogSchemaFixture(t)
		catalogStateInsert(t, db, "review", 1, 1)
		registrationReject(t, db.db, "INSERT INTO tc_catalog_registrations VALUES (?,1,?,?,0,0,1,1,1,1,x'01',x'02')", key, cid, cid)
	})
	t.Run("order needs v1 predecessor and exact next revision", func(t *testing.T) {
		db := catalogSchemaFixture(t)
		catalogSchemaExec(t, db.db, "UPDATE tc_catalog_managed SET aggregate_revision=2 WHERE content_id=?", cid)
		raw := registrationEnvelope("order")
		sum := sha256.Sum256(raw)
		registrationReject(t, db.db, "INSERT INTO tc_catalog_order_states_v2 VALUES (?,2,2,2,?,?,?)", cid, raw, len(raw), sum[:])
		catalogStateInsert(t, db, "order", 2, 1)
		catalogSchemaExec(t, db.db, "UPDATE tc_catalog_managed SET aggregate_revision=3 WHERE content_id=?", cid)
		registrationReject(t, db.db, "INSERT INTO tc_catalog_order_states_v2 VALUES (?,3,3,2,?,?,?)", cid, raw, len(raw), sum[:])
		registrationReject(t, db.db, "INSERT INTO tc_catalog_order_states_v2 VALUES (?,2,2,2,?,?,?)", cid, raw, len(raw), sum[:])
		registrationSchemaState(t, db.db, "order", 3, 2, "")
		catalogSchemaExec(t, db.db, "UPDATE tc_catalog_managed SET aggregate_revision=4 WHERE content_id=?", cid)
		old := catalogStateBytes(t, "order", cid, "", 3)
		hash := sha256.Sum256(old)
		registrationReject(t, db.db, "INSERT INTO tc_catalog_order_states VALUES (?,4,3,1,?,?,?)", cid, old, len(old), hash[:])
		registrationReject(t, db.db, "INSERT INTO tc_catalog_order_states_v2 VALUES (?,4,4,2,?,?,?)", cid, raw, len(raw), sum[:])
		registrationSchemaState(t, db.db, "order", 4, 3, "")
	})
}

func TestRegistrationSchemaEnvelopes(t *testing.T) {
	for _, kind := range []string{"review", "order"} {
		t.Run(kind, func(t *testing.T) {
			db := catalogSchemaFixture(t)
			cid, key := catalogSchemaID("cnt", 1), strings.Repeat("6", 32)
			registrationSchemaInsert(t, db.db, 1, key)
			if kind == "order" {
				catalogStateInsert(t, db, "order", 1, 1)
				catalogSchemaExec(t, db.db, "UPDATE tc_catalog_managed SET aggregate_revision=2 WHERE content_id=?", cid)
			}
			raw := registrationEnvelope(kind)
			sum := sha256.Sum256(raw)
			columns := []string{"content_id", "aggregate_revision", kind + "_revision", "encoding", "state", "state_bytes", "state_sha256"}
			values := []any{cid, 2, 2, 2, raw, len(raw), sum[:]}
			if kind == "review" {
				columns = append([]string{"subject_id"}, columns...)
				columns = append(columns, "bootstrap_command")
				values = []any{cid, cid, 1, 1, 2, raw, len(raw), sum[:], key}
			}
			for _, tc := range []struct {
				name, column string
				value        any
			}{
				{"wrong encoding", "encoding", 1}, {"empty state", "state", []byte{}}, {"oversize state", "state", make([]byte, 8388609)},
				{"text state", "state", "not binary"}, {"wrong header", "state", registrationEnvelope(map[string]string{"review": "order", "order": "review"}[kind])},
				{"length mismatch", "state_bytes", 0}, {"short hash", "state_sha256", make([]byte, 31)}, {"zero hash", "state_sha256", make([]byte, 32)},
				{"nonpositive revision", kind + "_revision", 0}, {"revision overflow", kind + "_revision", 1026}, {"future aggregate", "aggregate_revision", 9},
			} {
				t.Run(tc.name, func(t *testing.T) {
					args := append([]any(nil), values...)
					for i, column := range columns {
						if column == tc.column {
							args[i] = tc.value
						}
					}
					registrationReject(t, db.db, "INSERT INTO tc_catalog_"+kind+"_states_v2 VALUES ("+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+")", args...)
				})
			}
			// Valid header and exact 8 MiB envelope succeeds. SQL checks hash
			// shape only; a deliberately mismatching digest here documents the
			// remaining mandatory C2 hash/reconstruction check before decode.
			large := make([]byte, 8388608)
			copy(large, raw)
			sum[0] ^= 1
			for i, col := range columns {
				switch col {
				case "state":
					values[i] = large
				case "state_bytes":
					values[i] = len(large)
				case "state_sha256":
					values[i] = sum[:]
				}
			}
			catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_"+kind+"_states_v2 VALUES ("+strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")+")", values...)
		})
	}
}

func TestRegistrationSchemaKeyExclusion(t *testing.T) {
	key, cid := strings.Repeat("6", 32), catalogSchemaID("cnt", 1)
	t.Run("new registrations reject legacy keys", func(t *testing.T) {
		db := catalogSchemaFixture(t)
		for _, occupied := range []string{strings.Repeat("4", 32), strings.Repeat("5", 32)} {
			registrationReject(t, db.db, "INSERT INTO tc_catalog_registrations VALUES (?,1,?,?,0,0,1,1,1,1,x'01',x'02')", occupied, cid, cid)
		}
	})
	t.Run("legacy writers reject registration keys", func(t *testing.T) {
		db := registrationSchemaFixture(t)
		registrationReject(t, db.db, `INSERT INTO tc_catalog_decisions SELECT ?,subject_id,content_id,source_id,registration_revision,reviewer_principal,policy_revision,kind,claim_key,2,3,2,3,reason,canonical_command,canonical_result FROM tc_catalog_decisions`, key)
		registrationReject(t, db.db, `INSERT INTO tc_catalog_batches SELECT ?,source_id,registration_revision,artifact_sha256,byte_length,record_count,parser_revision,source_revision FROM tc_catalog_batches`, key)
		registrationReject(t, db.db, `INSERT INTO tc_catalog_imports VALUES (?,x'01',x'02',2)`, key)
		// Pre-existing overlaps are retained, and unrelated future legacy
		// writes retain their historical collision policy.
		catalogSchemaExec(t, db.db, `INSERT INTO tc_catalog_batches SELECT ?,source_id,registration_revision,artifact_sha256,byte_length,record_count,parser_revision,source_revision FROM tc_catalog_batches`, strings.Repeat("5", 32))
	})
}

func TestRegistrationSchemaRecovery(t *testing.T) {
	t.Run("populated current snapshot survives fenced restore", func(t *testing.T) {
		db := registrationSchemaFixture(t)
		before := registrationSchemaRows(t, db.db)
		snapshot, err := db.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "populated-v8.sqlite"), SchemaMigrations())
		if err != nil {
			t.Fatal(err)
		}
		owner, err := OpenLifecycleOwner(t.Context(), Config{Path: filepath.Join(t.TempDir(), "owner.sqlite")}, SchemaMigrations())
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Close(context.Background())
		if err := owner.Restore(t.Context(), snapshot, filepath.Join(t.TempDir(), "restored.sqlite")); err != nil {
			t.Fatal(err)
		}
		if got := registrationSchemaRows(t, owner.database.db); !reflect.DeepEqual(got, before) {
			t.Fatal("restore changed registration evidence")
		}
		if err := VerifyBackup(t.Context(), snapshot, SchemaMigrations()); err != nil {
			t.Fatal("original changed", err)
		}
	})
	t.Run("dirty future and checksum drift fail closed", func(t *testing.T) {
		for _, tc := range []struct {
			name, mutation string
			want           error
		}{
			{"dirty", "UPDATE tc_schema_migrations SET dirty=1 WHERE version=8", ErrDirtyMigration},
			{"future", "INSERT INTO tc_schema_migrations VALUES (9,'future','unknown',0,NULL)", ErrSchemaNewer},
			{"checksum", "UPDATE tc_schema_migrations SET checksum='changed' WHERE version=8", ErrChecksumDrift},
		} {
			t.Run(tc.name, func(t *testing.T) {
				db := registrationSchemaFixture(t)
				catalogSchemaExec(t, db.db, tc.mutation)
				before := registrationSchemaRows(t, db.db)
				if _, err := inspectMigrationState(t.Context(), db.db, SchemaMigrations()); !errors.Is(err, tc.want) {
					t.Fatal("invalid ledger accepted", err)
				}
				if _, err := db.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "invalid.sqlite"), SchemaMigrations()); err == nil {
					t.Fatal("invalid schema backed up")
				}
				if got := registrationSchemaRows(t, db.db); !reflect.DeepEqual(got, before) {
					t.Fatal("validation rewrote invalid schema")
				}
			})
		}
	})
	t.Run("every registration row stage rolls back", func(t *testing.T) {
		key, cid := strings.Repeat("6", 32), catalogSchemaID("cnt", 1)
		raw := registrationEnvelope("review")
		sum := sha256.Sum256(raw)
		stages := []struct {
			query string
			args  []any
		}{
			{"INSERT INTO tc_catalog_registrations VALUES (?,1,?,?,0,0,1,1,1,1,x'01',x'02')", []any{key, cid, cid}},
			{"INSERT INTO tc_catalog_registration_sources VALUES (?,?,?,?,1)", []any{key, strings.Repeat("1", 32), strings.Repeat("2", 32), strings.Repeat("3", 32)}},
			{"INSERT INTO tc_catalog_registration_support VALUES (?,?,?,?,?,1)", []any{key, catalogSchemaID("obs", 1), cid, strings.Repeat("1", 32), strings.Repeat("2", 32)}},
			{"INSERT INTO tc_catalog_review_states_v2 VALUES (?,?,1,1,2,?,?,?,?)", []any{cid, cid, raw, len(raw), sum[:], key}},
		}
		for stop := range stages {
			t.Run(fmt.Sprint(stop), func(t *testing.T) {
				db := catalogSchemaFixture(t)
				before := registrationSchemaRows(t, db.db)
				tx, err := db.db.BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				for _, stage := range stages[:stop+1] {
					if _, err := tx.ExecContext(t.Context(), stage.query, stage.args...); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := tx.ExecContext(t.Context(), "INSERT INTO tc_catalog_registration_support VALUES ('missing','missing','missing','missing','missing',1)"); err == nil {
					t.Fatal("failure injection accepted")
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				if got := registrationSchemaRows(t, db.db); !reflect.DeepEqual(got, before) {
					t.Fatal("partial registration rows survived rollback")
				}
			})
		}
	})
}
