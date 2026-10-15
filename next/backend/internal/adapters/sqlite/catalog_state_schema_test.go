package sqlite

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

// These tests exercise storage envelopes only. Semantic replay and digest
// comparison belong to the future adapter and the accepted domain decoder.
func catalogStateBytes(kind, content, version string, revision uint64) []byte {
	var b bytes.Buffer
	number := func(n uint64) { _ = binary.Write(&b, binary.BigEndian, n) }
	text := func(s string) { number(uint64(len(s))); b.WriteString(s) }
	text("pi08-" + kind + "-state/1")
	text(content)
	if kind == "review" {
		text(version)
	}
	number(revision)
	for range 4 {
		number(0)
	}
	return b.Bytes()
}

func catalogStateInsert(t *testing.T, db *Database, kind string, aggregate, revision int) {
	t.Helper()
	cid := catalogSchemaID("cnt", 1)
	raw := catalogStateBytes(kind, cid, "", uint64(revision))
	digest := sha256.Sum256(raw)
	if kind == "review" {
		catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_review_states VALUES (?,?,?,?,1,?,?,?)",
			cid, cid, aggregate, revision, raw, len(raw), digest[:])
	} else {
		catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_order_states VALUES (?,?,?,1,?,?,?)",
			cid, aggregate, revision, raw, len(raw), digest[:])
	}
}

func TestCatalogStateMigrationIdentity(t *testing.T) {
	migrations := SchemaMigrations()
	want := []string{
		"23f6dda1108501084bff616ef2709e9596fe2bf684fd60e1b727ba698ba44d0b",
		"f36f56149dc69c4b0be984967cb568d57f9330d4ed275f59afbd1f673a3d3294",
		"104ff5813e462d75c6f0359c32030336d1397f8f16184e99e016762a25dfd571",
		"cd838271e3a1890210571d6b3634ca8067bcff6f3cd3b40937a0c2330b0c88ed",
		"c65137b653d80e54301a14fd7f8be52bdb7e246ec32245623cbf47d7bfa66fc7",
		"c16317bf9ac4dc90be9ba74e3887beea393526a9c99c7b85224061e34dac65d9",
	}
	if len(migrations) != 6 || migrations[5].Version != 6 || migrations[5].ID != "0006-catalog-aggregate-state" {
		t.Fatal("migration identity changed")
	}
	for i, sum := range want {
		if migrationChecksum(migrations[i]) != sum {
			t.Fatalf("v%d checksum changed", i+1)
		}
	}
	db := catalogSchemaFixture(t)
	for _, mutate := range []func([]Migration){
		func(m []Migration) { m[5].ID += "-changed" },
		func(m []Migration) { m[5].Statements[0] += " " },
		func(m []Migration) { m[4].Statements[0] += " " },
	} {
		changed := SchemaMigrations()
		mutate(changed)
		if _, err := inspectMigrationState(t.Context(), db.db, changed); !errors.Is(err, ErrChecksumDrift) {
			t.Fatalf("migration drift accepted: %v", err)
		}
	}
	for _, table := range []string{"tc_catalog_review_states", "tc_catalog_order_states", "tc_catalog_order_state_versions"} {
		var strict, count int
		if err := db.db.QueryRowContext(t.Context(), "SELECT strict FROM pragma_table_list WHERE name=?", table).Scan(&strict); err != nil || strict != 1 {
			t.Fatalf("non-STRICT state table: %s %v", table, err)
		}
		if err := db.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatal("migration fabricated state", err)
		}
	}
}

func TestCatalogStateV5Upgrade(t *testing.T) {
	migrations := SchemaMigrations()
	db := catalogSchemaFixtureMigrations(t, migrations[:5])
	catalogSchemaExec(t, db.db, "INSERT INTO tc_tags VALUES (?, ?, 1)", catalogSchemaID("tag", 1), bytes.Repeat([]byte{1}, 8))
	before := catalogV4Rows(t, db.db)
	for _, table := range catalogFoundationTables {
		before[table] = catalogSchemaRows(t, db.db, "SELECT * FROM "+table+" ORDER BY 1,2")
	}
	before["tc_schema_migrations"] = catalogSchemaRows(t, db.db, "SELECT * FROM tc_schema_migrations ORDER BY version")
	objects := catalogSchemaRows(t, db.db, "SELECT type,name,tbl_name,sql FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' ORDER BY name")
	var path string
	var sequence int
	var name string
	if err := db.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(t.Context(), Config{Path: path, UpgradeBackupPath: filepath.Join(t.TempDir(), "v5.sqlite")}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if version, err := upgraded.CurrentVersion(t.Context()); err != nil || version != 6 {
		t.Fatal("v6 upgrade failed", err)
	}
	for table, rows := range before {
		query := "SELECT * FROM " + table
		if table == "tc_schema_migrations" {
			query += " WHERE version<=5"
		}
		query += " ORDER BY 1"
		if table != "tc_schema_migrations" {
			query += ",2"
		}
		if got := catalogSchemaRows(t, upgraded.db, query); !reflect.DeepEqual(got, rows) {
			t.Fatalf("upgrade changed %s", table)
		}
	}
	after := catalogSchemaRows(t, upgraded.db, "SELECT type,name,tbl_name,sql FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' ORDER BY name")
	for _, object := range objects {
		if !catalogSchemaContains(after, object) {
			t.Fatal("upgrade changed old schema")
		}
	}
	backup, found := upgraded.UpgradeBackup()
	if !found || backup.SchemaVersion != 5 {
		t.Fatal("missing v5 recovery point")
	}
	if err := VerifyBackup(t.Context(), backup, migrations); err != nil {
		t.Fatal(err)
	}
	catalogStateInsert(t, upgraded, "review", 1, 1)
	catalogStateInsert(t, upgraded, "order", 1, 1)
	if err := verifyForeignKeys(t.Context(), upgraded.db); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogStateEnvelopeConstraints(t *testing.T) {
	for _, kind := range []string{"review", "order"} {
		t.Run(kind, func(t *testing.T) {
			db := catalogSchemaFixture(t)
			catalogStateInsert(t, db, kind, 1, 1)
			table := "tc_catalog_" + kind + "_states"
			columns := []string{"content_id", "aggregate_revision", kind + "_revision", "encoding", "state", "state_bytes", "state_sha256"}
			if kind == "review" {
				columns = append([]string{"subject_id"}, columns...)
			}
			// Advance the live fence so these inserts fail on the selected defect,
			// not merely on the existing snapshot's key.
			catalogSchemaExec(t, db.db, "UPDATE tc_catalog_managed SET aggregate_revision=2")
			catalogSchemaExec(t, db.db, "UPDATE tc_catalog_subjects SET review_revision=2")
			for _, tc := range []struct {
				name, column, expression string
			}{
				{"encoding", "encoding", "2"},
				{"empty", "state", "x''"},
				{"oversized", "state", "zeroblob(8388609)"},
				{"text", "state", "'not binary'"},
				{"short digest", "state_sha256", "zeroblob(31)"},
				{"zero digest", "state_sha256", "zeroblob(32)"},
				{"length mismatch", "state_bytes", "1"},
				{"dangling content", "content_id", "'" + catalogSchemaID("cnt", 99) + "'"},
				{"zero revision", kind + "_revision", "0"},
				{"revision limit", kind + "_revision", "1026"},
				{"future aggregate", "aggregate_revision", "3"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					raw := catalogStateBytes(kind, catalogSchemaID("cnt", 1), "", 2)
					digest := sha256.Sum256(raw)
					args := []any{}
					query := "INSERT INTO " + table + " VALUES ("
					for i, column := range columns {
						if i > 0 {
							query += ","
						}
						if column == tc.column {
							query += tc.expression
							continue
						}
						query += "?"
						switch column {
						case "subject_id", "content_id":
							args = append(args, catalogSchemaID("cnt", 1))
						case "aggregate_revision", kind + "_revision":
							args = append(args, 2)
						case "encoding":
							args = append(args, 1)
						case "state":
							args = append(args, raw)
						case "state_bytes":
							args = append(args, len(raw))
						case "state_sha256":
							args = append(args, digest[:])
						}
					}
					if _, err := db.db.ExecContext(t.Context(), query+")", args...); err == nil {
						t.Fatal("invalid state accepted")
					}
				})
			}
			catalogStateInsert(t, db, kind, 2, 2)
			before := catalogSchemaRows(t, db.db, "SELECT * FROM "+table+" ORDER BY 1,2")
			for _, query := range []string{
				"UPDATE " + table + " SET state_sha256=zeroblob(32)",
				"UPDATE " + table + " SET aggregate_revision=aggregate_revision",
				"DELETE FROM " + table,
				"INSERT OR REPLACE INTO " + table + " SELECT * FROM " + table,
				"INSERT INTO " + table + " SELECT * FROM " + table + " WHERE " + kind + "_revision=1",
			} {
				if _, err := db.db.ExecContext(t.Context(), query); err == nil {
					t.Fatal("immutable state or revision regression accepted")
				}
			}
			if got := catalogSchemaRows(t, db.db, "SELECT * FROM "+table+" ORDER BY 1,2"); !reflect.DeepEqual(got, before) {
				t.Fatal("rejected mutation changed stored bytes")
			}
		})
	}
}

func TestCatalogStateVersionRelations(t *testing.T) {
	db := catalogSchemaFixture(t)
	cid, vid := catalogSchemaID("cnt", 1), catalogSchemaID("ver", 1)
	catalogStateInsert(t, db, "order", 1, 1)
	catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_order_state_versions VALUES (?,1,?)", cid, vid)
	for _, args := range [][]any{
		{cid, 1, catalogSchemaID("ver", 99)},
		{cid, 1, catalogSchemaID("ver", 2)},
		{cid, 2, vid},
	} {
		if _, err := db.db.ExecContext(t.Context(), "INSERT INTO tc_catalog_order_state_versions VALUES (?,?,?)", args...); err == nil {
			t.Fatal("dangling or cross-content version accepted")
		}
	}
	for _, query := range []string{
		"UPDATE tc_catalog_order_state_versions SET order_revision=2",
		"DELETE FROM tc_catalog_order_state_versions",
		"INSERT OR REPLACE INTO tc_catalog_order_state_versions SELECT * FROM tc_catalog_order_state_versions",
		"DELETE FROM tc_content_versions WHERE version_id='" + vid + "'",
	} {
		if _, err := db.db.ExecContext(t.Context(), query); err == nil {
			t.Fatal("immutable version binding changed")
		}
	}
	catalogSchemaExec(t, db.db, "PRAGMA foreign_keys=OFF")
	catalogSchemaExec(t, db.db, "INSERT INTO tc_catalog_order_state_versions VALUES (?,1,?)", cid, catalogSchemaID("ver", 99))
	catalogSchemaExec(t, db.db, "PRAGMA foreign_keys=ON")
	assertInvalidForeignKeyBackup(t, verifyForeignKeys(t.Context(), db.db))
	if _, err := db.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "rejected.sqlite"), SchemaMigrations()); err == nil {
		t.Fatal("corrupt version binding published as backup")
	}
}
