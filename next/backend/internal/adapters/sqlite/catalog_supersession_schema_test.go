package sqlite

import (
	"bytes"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// C1 exercises SQL envelopes on deliberately seeded fixtures. It makes no claim
// about canonical bytes, historical support, full graph reconstruction or the
// combined accounting that C2 must implement before activating migration 9.
func supersessionSchemaMigrations() []Migration {
	return append(SchemaMigrations(), catalogSupersessionMigration())
}

func supersessionSchemaFixture(t *testing.T) *Database {
	t.Helper()
	db := registrationSchemaFixture(t)
	if err := applyMigration(t.Context(), db.db, catalogSupersessionMigration()); err != nil {
		t.Fatal(err)
	}
	cid := catalogSchemaID("cnt", 1)
	for _, n := range []int{3, 4} {
		vid := catalogSchemaID("ver", n)
		catalogSchemaExec(t, db.db, `INSERT INTO tc_content_versions VALUES (?,?,123,?,0,NULL,NULL)`, vid, cid, bytes.Repeat([]byte{1}, 20))
		catalogSchemaExec(t, db.db, `INSERT INTO tc_catalog_subjects VALUES (?,?,?,1)`, vid, cid, vid)
	}
	catalogSchemaExec(t, db.db, `UPDATE tc_catalog_managed SET aggregate_revision=3 WHERE content_id=?`, cid)
	return db
}

const supersessionSchemaInsert = `INSERT INTO tc_catalog_supersessions VALUES (?,?,?,?,?,?,?,2,3,1,2,1,1,2,1,1,?,?,?,?)`

func supersessionSchemaArgs() []any {
	return []any{strings.Repeat("a", 32), catalogSchemaID("cnt", 1), catalogSchemaID("ver", 1), catalogSchemaID("ver", 3), strings.Repeat("1", 32), strings.Repeat("2", 32), strings.Repeat("3", 32), []byte("command"), []byte("result"), bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)}
}

func supersessionSchemaEvidence(t *testing.T, db *sql.DB) {
	t.Helper()
	catalogSchemaExec(t, db, `INSERT INTO tc_catalog_supersession_evidence VALUES (?,?,?,?,?,?,?, ?,0,'','2026-01-01T00:00:00Z',0)`,
		catalogSchemaID("obs", 9), strings.Repeat("a", 32), catalogSchemaID("cnt", 1), catalogSchemaID("ver", 1), catalogSchemaID("ver", 3), strings.Repeat("1", 32), strings.Repeat("2", 32), strings.Repeat("4", 32))
	catalogSchemaExec(t, db, `INSERT INTO tc_catalog_supersession_support VALUES (?,?)`, strings.Repeat("a", 32), catalogSchemaID("obs", 9))
}

func TestSupersessionSchemaUpgrade(t *testing.T) {
	db := registrationSchemaFixture(t)
	before := registrationSchemaRows(t, db.db)
	path := dbPathForSupersessionTest(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	migrations := supersessionSchemaMigrations()
	if len(SchemaMigrations()) != 8 || migrations[8].Version != 9 || migrations[8].ID != "0009-catalog-version-supersession" {
		t.Fatal("C1 must remain staged until C2 admits all v9 owner paths")
	}
	upgraded, err := Open(t.Context(), Config{Path: path, UpgradeBackupPath: filepath.Join(t.TempDir(), "pre-v9.sqlite")}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	for table, want := range before {
		query := "SELECT * FROM " + table + " ORDER BY 1,2"
		if table == "tc_schema_migrations" {
			query = "SELECT * FROM tc_schema_migrations WHERE version<=8 ORDER BY 1,2"
		}
		if got := catalogSchemaRows(t, upgraded.db, query); !reflect.DeepEqual(got, want) {
			t.Fatalf("v8 bytes or values changed in %s", table)
		}
	}
	for _, table := range []string{"tc_catalog_supersessions", "tc_catalog_supersession_evidence", "tc_catalog_supersession_support"} {
		var strict, count int
		if err := upgraded.db.QueryRowContext(t.Context(), `SELECT strict FROM pragma_table_list WHERE name=?`, table).Scan(&strict); err != nil || strict != 1 {
			t.Fatal(table, "not STRICT", err)
		}
		if err := upgraded.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatal(table, "migration fabricated history", err)
		}
		rows := catalogSchemaRows(t, upgraded.db, "PRAGMA foreign_key_list("+table+")")
		for _, fk := range rows {
			if fk[5] != "RESTRICT" || fk[6] != "RESTRICT" {
				t.Fatalf("nonrestrictive FK: %v", fk)
			}
		}
	}
	if err := upgraded.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), Config{Path: path}, migrations)
	if err != nil {
		t.Fatal("clean v9 reopen", err)
	}
	reopened.Close()
	if _, err := Open(t.Context(), Config{Path: path}, SchemaMigrations()); !errors.Is(err, ErrSchemaNewer) {
		t.Fatal("v8 binary accepted v9", err)
	}
}

func dbPathForSupersessionTest(t *testing.T, db *Database) string {
	t.Helper()
	var seq int
	var name, path string
	if err := db.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSupersessionSchemaConstraints(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func([]any)
	}{
		{"zero key", func(a []any) { a[0] = strings.Repeat("0", 32) }},
		{"uppercase key", func(a []any) { a[0] = strings.Repeat("A", 32) }},
		{"nul key", func(a []any) { a[0] = strings.Repeat("a", 32) + "\x00" }},
		{"self link", func(a []any) { a[3] = a[2] }},
		{"cross Content", func(a []any) { a[3] = catalogSchemaID("ver", 2) }},
		{"unmanaged endpoint", func(a []any) { a[3] = catalogSchemaID("ver", 8) }},
		{"different source", func(a []any) { a[4] = strings.Repeat("b", 32) }},
		{"zero reviewer", func(a []any) { a[6] = strings.Repeat("0", 32) }},
		{"empty command", func(a []any) { a[7] = []byte{} }},
		{"command too large", func(a []any) { a[7] = make([]byte, 8388609) }},
		{"text command", func(a []any) { a[7] = "command" }},
		{"empty result", func(a []any) { a[8] = []byte{} }},
		{"short hash", func(a []any) { a[9] = make([]byte, 31) }},
		{"long hash", func(a []any) { a[10] = make([]byte, 33) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := supersessionSchemaFixture(t)
			args := supersessionSchemaArgs()
			tc.mutate(args)
			registrationReject(t, db.db, supersessionSchemaInsert, args...)
		})
	}
	for _, tc := range []struct{ name, old, replacement string }{
		{"zero review", ",1,1,2,1,1,", ",0,1,2,1,1,"},
		{"invalid protection", ",1,1,2,1,1,", ",1,1,2,2,1,"},
		{"future encoding", ",1,1,2,1,1,", ",1,1,2,1,2,"},
		{"revision overflow", ",2,3,1,2,", ",9223372036854775807,3,1,2,"},
		{"graph revision gap", ",2,3,1,2,", ",2,3,2,3,"},
		{"graph limit", ",2,3,1,2,", ",2,3,1024,1025,"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := supersessionSchemaFixture(t)
			registrationReject(t, db.db, strings.Replace(supersessionSchemaInsert, tc.old, tc.replacement, 1), supersessionSchemaArgs()...)
		})
	}
}

func TestSupersessionSchemaHistoryGuards(t *testing.T) {
	db := supersessionSchemaFixture(t)
	args := supersessionSchemaArgs()
	catalogSchemaExec(t, db.db, supersessionSchemaInsert, args...)
	supersessionSchemaEvidence(t, db.db)
	for _, table := range []string{"tc_catalog_supersessions", "tc_catalog_supersession_evidence", "tc_catalog_supersession_support"} {
		registrationReject(t, db.db, "DELETE FROM "+table)
		registrationReject(t, db.db, "UPDATE "+table+" SET command_id=command_id")
	}
	registrationReject(t, db.db, `INSERT INTO tc_catalog_supersession_support VALUES (?,?)`, strings.Repeat("b", 32), catalogSchemaID("obs", 9))
	registrationReject(t, db.db, `INSERT INTO tc_catalog_supersession_support VALUES (?,?)`, strings.Repeat("a", 32), catalogSchemaID("obs", 8))
	// A -> B -> C is valid; graph tests deliberately use storage envelopes only.
	catalogSchemaExec(t, db.db, `UPDATE tc_catalog_managed SET aggregate_revision=4 WHERE content_id=?`, args[1])
	second := supersessionSchemaArgs()
	second[0], second[2], second[3] = strings.Repeat("b", 32), args[3], catalogSchemaID("ver", 4)
	catalogSchemaExec(t, db.db, strings.Replace(supersessionSchemaInsert, ",2,3,1,2,", ",3,4,2,3,", 1), second...)
	catalogSchemaExec(t, db.db, `UPDATE tc_catalog_managed SET aggregate_revision=5 WHERE content_id=?`, args[1])
	for _, edge := range [][2]any{{args[2], second[3]}, {second[3], args[2]}, {catalogSchemaID("ver", 4), args[3]}} {
		a := supersessionSchemaArgs()
		a[0], a[2], a[3] = strings.Repeat("c", 32), edge[0], edge[1]
		registrationReject(t, db.db, strings.Replace(supersessionSchemaInsert, ",2,3,1,2,", ",4,5,3,4,", 1), a...)
	}
}

func TestSupersessionSchemaAuthorityAndCollisions(t *testing.T) {
	for _, mutation := range []string{
		`UPDATE tc_catalog_sources SET revoked=1`,
		`UPDATE tc_catalog_managed SET aggregate_revision=4`,
	} {
		t.Run(mutation, func(t *testing.T) {
			db := supersessionSchemaFixture(t)
			catalogSchemaExec(t, db.db, mutation)
			registrationReject(t, db.db, supersessionSchemaInsert, supersessionSchemaArgs()...)
		})
	}
	t.Run("operator without ownership claim", func(t *testing.T) {
		db := supersessionSchemaFixture(t)
		catalogSchemaExec(t, db.db, `INSERT INTO tc_catalog_sources VALUES (?,?,?,'operator','1','core',3,0)`, strings.Repeat("b", 32), strings.Repeat("2", 32), strings.Repeat("3", 32))
		a := supersessionSchemaArgs()
		a[4] = strings.Repeat("b", 32)
		registrationReject(t, db.db, supersessionSchemaInsert, a...)
	})
	t.Run("existing keys", func(t *testing.T) {
		db := supersessionSchemaFixture(t)
		for _, key := range []string{strings.Repeat("4", 32), strings.Repeat("5", 32), strings.Repeat("6", 32)} {
			a := supersessionSchemaArgs()
			a[0] = key
			registrationReject(t, db.db, supersessionSchemaInsert, a...)
		}
	})
	t.Run("new batch key", func(t *testing.T) {
		db := supersessionSchemaFixture(t)
		catalogSchemaExec(t, db.db, supersessionSchemaInsert, supersessionSchemaArgs()...)
		registrationReject(t, db.db, `INSERT INTO tc_catalog_batches SELECT ?,source_id,registration_revision,artifact_sha256,byte_length,record_count,parser_revision,source_revision FROM tc_catalog_batches LIMIT 1`, strings.Repeat("a", 32))
	})
}

func TestSupersessionSchemaRollbackAndDirty(t *testing.T) {
	db := registrationSchemaFixture(t)
	before := registrationSchemaRows(t, db.db)
	broken := catalogSupersessionMigration()
	broken.Statements = append(broken.Statements, "CREATE TABLE broken (")
	if err := applyMigration(t.Context(), db.db, broken); err == nil {
		t.Fatal("broken migration accepted")
	}
	for table, want := range before {
		if table == "tc_schema_migrations" {
			continue
		}
		if got := catalogSchemaRows(t, db.db, "SELECT * FROM "+table+" ORDER BY 1,2"); !reflect.DeepEqual(got, want) {
			t.Fatal("rollback changed", table)
		}
	}
	var n int
	if err := db.db.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_schema WHERE name LIKE 'tc_catalog_supersession%'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("partial schema published", n, err)
	}
	if _, err := inspectMigrationState(t.Context(), db.db, supersessionSchemaMigrations()); !errors.Is(err, ErrDirtyMigration) {
		t.Fatal("dirty accepted", err)
	}
}

func TestSupersessionSchemaFreshAndCorrupt(t *testing.T) {
	t.Run("fresh v9 and checksum drift", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "fresh-v9.sqlite")
		migrations := supersessionSchemaMigrations()
		db, err := Open(t.Context(), Config{Path: path}, migrations)
		if err != nil {
			t.Fatal(err)
		}
		catalogSchemaExec(t, db.db, `UPDATE tc_schema_migrations SET checksum='changed' WHERE version=9`)
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		if reopened, err := Open(t.Context(), Config{Path: path}, migrations); !errors.Is(err, ErrChecksumDrift) {
			if reopened != nil {
				reopened.Close()
			}
			t.Fatal("v9 checksum corruption accepted", err)
		}
	})
	t.Run("dangling support fails snapshot verification", func(t *testing.T) {
		db := supersessionSchemaFixture(t)
		catalogSchemaExec(t, db.db, supersessionSchemaInsert, supersessionSchemaArgs()...)
		supersessionSchemaEvidence(t, db.db)
		if err := verifyForeignKeys(t.Context(), db.db); err != nil {
			t.Fatal(err)
		}
		// Deliberate offline corruption only; normal connections require FKs.
		catalogSchemaExec(t, db.db, `PRAGMA foreign_keys=OFF`)
		catalogSchemaExec(t, db.db, `INSERT INTO tc_catalog_supersession_support VALUES (?,?)`, strings.Repeat("a", 32), catalogSchemaID("obs", 8))
		catalogSchemaExec(t, db.db, `PRAGMA foreign_keys=ON`)
		before := registrationSchemaRows(t, db.db)
		if err := verifyForeignKeys(t.Context(), db.db); !errors.Is(err, ErrBackupInvalid) {
			t.Fatal("corrupt relation accepted", err)
		}
		if !reflect.DeepEqual(before, registrationSchemaRows(t, db.db)) {
			t.Fatal("verification repaired corrupt history")
		}
	})
	t.Run("exact blob byte bound", func(t *testing.T) {
		db := supersessionSchemaFixture(t)
		a := supersessionSchemaArgs()
		a[7], a[8] = make([]byte, 8388608), make([]byte, 8388608)
		catalogSchemaExec(t, db.db, supersessionSchemaInsert, a...)
		var command, result int
		if err := db.db.QueryRowContext(t.Context(), `SELECT length(canonical_command),length(canonical_result) FROM tc_catalog_supersessions`).Scan(&command, &result); err != nil || command != 8388608 || result != 8388608 {
			t.Fatal(command, result, err)
		}
	})
}
