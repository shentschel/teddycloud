package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

// These tests exercise storage envelopes only. Semantic replay and digest
// comparison belong to the future adapter and the accepted domain decoder.
func catalogStateBytes(t *testing.T, kind, content, version string, revision uint64) []byte {
	t.Helper()
	cid, err := identity.ParseContentID(content)
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	var revisionOffset int
	if kind == "review" {
		subject, err := catalog.ContentSubject(cid)
		if version != "" {
			vid, parseErr := identity.ParseContentVersionID(version)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			subject, err = catalog.VersionSubject(cid, vid)
		}
		if err != nil {
			t.Fatal(err)
		}
		state, err := catalog.NewCatalogReviewState(subject, false)
		if err != nil {
			t.Fatal(err)
		}
		raw, err = state.PersistenceEncoding()
		if err != nil {
			t.Fatal(err)
		}
		revisionOffset = 8 + len("pi08-review-state/1") + 8 + len(content) + 8 + len(version)
	} else {
		state, err := catalog.NewCatalogOrderState(cid, nil)
		if err != nil {
			t.Fatal(err)
		}
		raw, err = state.PersistenceEncoding()
		if err != nil {
			t.Fatal(err)
		}
		revisionOffset = 8 + len("pi08-order-state/1") + 8 + len(content)
	}
	// Revision 1 is the exact accepted domain encoding. Later revisions are
	// envelope-only mutations for storage-fence tests, not valid semantic replay.
	if revision != 1 {
		binary.BigEndian.PutUint64(raw[revisionOffset:revisionOffset+8], revision)
	}
	return raw
}

func TestCatalogStateDomainEnvelopeOffsets(t *testing.T) {
	for _, tc := range []struct {
		name, kind, version string
	}{
		{name: "content review", kind: "review"},
		{name: "version review", kind: "review", version: catalogSchemaID("ver", 1)},
		{name: "order", kind: "order"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := catalogSchemaFixture(t)
			cid := catalogSchemaID("cnt", 1)
			raw := catalogStateBytes(t, tc.kind, cid, tc.version, 1)
			bindings := catalog.CatalogPersistenceBindings{AllowUnbound: true}
			if tc.kind == "review" {
				if _, err := catalog.RehydrateCatalogReviewState(raw, bindings); err != nil {
					t.Fatal("domain encoder/decoder disagree", err)
				}
			} else if _, err := catalog.RehydrateCatalogOrderState(raw, bindings); err != nil {
				t.Fatal("domain encoder/decoder disagree", err)
			}
			insert := func(state, digest []byte) error {
				if tc.kind == "review" {
					subject := cid
					if tc.version != "" {
						subject = tc.version
					}
					_, err := db.db.ExecContext(t.Context(), "INSERT INTO tc_catalog_review_states VALUES (?,?,1,1,1,?,?,?)", subject, cid, state, len(state), digest)
					return err
				}
				_, err := db.db.ExecContext(t.Context(), "INSERT INTO tc_catalog_order_states VALUES (?,1,1,1,?,?,?)", cid, state, len(state), digest)
				return err
			}
			contentOffset := 8 + len("pi08-"+tc.kind+"-state/1") + 8
			revisionOffset := contentOffset + len(cid)
			offsets := []int{7, 8, contentOffset + len(cid) - 1}
			if tc.kind == "review" {
				offsets = append(offsets, revisionOffset+7)
				revisionOffset += 8 + len(tc.version)
				if tc.version != "" {
					offsets = append(offsets, revisionOffset-1)
				}
			}
			offsets = append(offsets, revisionOffset+7)
			for _, offset := range offsets {
				changed := bytes.Clone(raw)
				changed[offset] ^= 1
				digest := sha256.Sum256(changed)
				if err := insert(changed, digest[:]); err == nil {
					t.Fatalf("wrong envelope byte %d accepted", offset)
				}
			}
			digest := sha256.Sum256(raw)
			// SQLite enforces digest shape, not SHA-256 content. Adapter reads
			// must compare it before decoding or trusting any retained state.
			digest[0] ^= 1
			if err := insert(raw, digest[:]); err != nil {
				t.Fatal("accepted domain envelope rejected", err)
			}
		})
	}
}

func TestCatalogStateQuerySchemaCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name     string
		version  int
		mutation string
		accepted bool
	}{
		{name: "v4", version: 4},
		{name: "v5", version: 5, accepted: true},
		{name: "v6", version: 6, accepted: true},
		{name: "dirty v6", version: 6, mutation: "UPDATE tc_schema_migrations SET dirty=1 WHERE version=6"},
		{name: "future v7", version: 6, mutation: "INSERT INTO tc_schema_migrations(version,migration_id,checksum,dirty) VALUES (7,'future','unknown',0)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner, err := OpenLifecycleOwner(t.Context(), Config{Path: filepath.Join(t.TempDir(), "compat.sqlite")}, SchemaMigrations()[:tc.version])
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close(context.Background())
			catalogSeedVersions(t, owner, 1)
			if tc.mutation != "" {
				catalogSchemaExec(t, owner.database.db, tc.mutation)
			}
			result, err := catalogRead(t, owner, app.Query{Kind: app.GetContent, Content: catalogQueryContent(t, 1), Page: app.PageRequest{Limit: 128}})
			if tc.accepted {
				if err != nil || result.Kind != app.Found || len(result.Entries) != 1 {
					t.Fatalf("accepted query regressed: %#v %v", result, err)
				}
				// v5 retains query support; only v6 enables state reads.
				err = owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
					subject, _ := catalog.ContentSubject(catalogQueryContent(t, 1))
					_, _, reviewErr := r.LoadReview(t.Context(), subject)
					_, _, orderErr := r.LoadOrder(t.Context(), catalogQueryContent(t, 1))
					if tc.version == 6 {
						if reviewErr != nil || orderErr != nil {
							t.Fatal("v6 state reads unavailable", reviewErr, orderErr)
						}
					} else if !errors.Is(reviewErr, app.ErrUnsupported) || !errors.Is(orderErr, app.ErrUnsupported) {
						t.Fatal("v5 state restoration unexpectedly enabled")
					}
					return app.ErrUnsupported
				})
			}
			if !errors.Is(err, app.ErrUnsupported) {
				t.Fatalf("unsupported boundary changed: %v", err)
			}
		})
	}
}

func catalogStateInsert(t *testing.T, db *Database, kind string, aggregate, revision int) {
	t.Helper()
	cid := catalogSchemaID("cnt", 1)
	raw := catalogStateBytes(t, kind, cid, "", uint64(revision))
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
		"f3bd874bbb9d8ae2228ba91c3d2b5d5077d0110d69d13551a24c87e54558fcd1",
	}
	if len(migrations) != 6 || migrations[5].Version != 6 || migrations[5].ID != "0006-catalog-aggregate-state" {
		t.Fatal("migration identity changed")
	}
	for i, sum := range want {
		if got := migrationChecksum(migrations[i]); got != sum {
			t.Fatalf("v%d checksum changed: %s", i+1, got)
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
					raw := catalogStateBytes(t, kind, catalogSchemaID("cnt", 1), "", 2)
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
