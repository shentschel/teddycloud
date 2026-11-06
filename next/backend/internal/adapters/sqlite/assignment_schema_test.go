package sqlite

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// A2a tests SQL storage envelopes only. These intentionally synthetic blobs do
// not claim canonical replay, authenticated writes or A2b repository admission.
var assignmentSchemaTables = []string{
	"tc_assignments", "tc_assignment_events", "tc_assignment_evidence",
	"tc_assignment_support", "tc_assignment_commands", "tc_assignment_heads",
	"tc_assignment_projections",
}

func assignmentSchemaMigrations() []Migration {
	return append(SchemaMigrations()[:9], assignmentMigration())
}

func assignmentSchemaFixture(t *testing.T) *Database {
	t.Helper()
	db, err := Open(t.Context(), Config{Path: filepath.Join(t.TempDir(), "assignment.sqlite")}, assignmentSchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, n := range []int{1, 2, 3} {
		catalogSchemaExec(t, db.db, `INSERT INTO tc_tags VALUES (?,?,1)`, catalogSchemaID("tag", n), bytes.Repeat([]byte{byte(n)}, 8))
		catalogSchemaExec(t, db.db, `INSERT INTO tc_catalog_content VALUES (?,'Exact target',NULL,NULL)`, catalogSchemaID("cnt", n))
		catalogSchemaExec(t, db.db, `INSERT INTO tc_content_versions VALUES (?,?,123,?,0,NULL,NULL)`, catalogSchemaID("ver", n), catalogSchemaID("cnt", n), bytes.Repeat([]byte{1}, 20))
	}
	return db
}

func assignmentSchemaAggregate(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	catalogSchemaExec(t, db, `INSERT INTO tc_assignments VALUES (?,?)`, catalogSchemaID("asn", n), catalogSchemaID("tag", n))
}

const assignmentSchemaEventInsert = `INSERT INTO tc_assignment_events VALUES (?,?,?,?,1,10,1,20,2,?,?,NULL,1,?,?,0,0,100)`

func assignmentSchemaEventArgs(n, revision int) []any {
	return []any{catalogSchemaID("asn", n), catalogSchemaID("tag", n), revision,
		fmt.Sprintf("%032x", n*100+revision), catalogSchemaID("cnt", n), catalogSchemaID("ver", n), []byte("event"), bytes.Repeat([]byte{1}, 32)}
}

func assignmentSchemaEvent(t *testing.T, db *sql.DB, n, revision int) {
	t.Helper()
	catalogSchemaExec(t, db, assignmentSchemaEventInsert, assignmentSchemaEventArgs(n, revision)...)
}

const assignmentSchemaEvidenceInsert = `INSERT INTO tc_assignment_evidence VALUES (?,?,?,?,1,?,?,?,?)`

func assignmentSchemaEvidenceArgs(n int) []any {
	return []any{catalogSchemaID("evd", n), catalogSchemaID("asn", n), catalogSchemaID("tag", n), 1,
		[]byte("evidence"), bytes.Repeat([]byte{1}, 32), catalogSchemaID("cnt", n), catalogSchemaID("ver", n)}
}

const assignmentSchemaCommandInsert = `INSERT INTO tc_assignment_commands VALUES (?,?,?,?,1,?,?,?,?)`

func assignmentSchemaCommandArgs(n, revision int) []any {
	return []any{fmt.Sprintf("%032x", n*100+revision), catalogSchemaID("asn", n), catalogSchemaID("tag", n), revision,
		[]byte("command"), []byte("result"), bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)}
}

func assignmentSchemaReject(t *testing.T, db *sql.DB, code int, query string, args ...any) {
	t.Helper()
	before := registrationSchemaRows(t, db)
	_, err := db.ExecContext(t.Context(), query, args...)
	var coded interface{ Code() int }
	if !errors.As(err, &coded) {
		t.Fatalf("wanted constraint %d, got %v for %s", code, err, query)
	}
	got := coded.Code()
	// SQLite implements parent-side RESTRICT as an internal trigger (1811),
	// while child-side FK violations use 787. Require the FK diagnostic too.
	if code == 787 && got == 1811 && strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		got = 787
	}
	if (code != 0 && got != code) || got&255 != 19 {
		t.Fatalf("wanted constraint %d, got %v for %s", code, err, query)
	}
	if !reflect.DeepEqual(before, registrationSchemaRows(t, db)) {
		t.Fatal("rejected statement changed retained rows")
	}
}

func TestAssignmentSchemaUpgrade(t *testing.T) {
	active := SchemaMigrations()
	if len(active) != 10 || active[8].Version != 9 || active[8].ID != "0009-catalog-version-supersession" ||
		active[9].Version != 10 || active[9].ID != "0010-assignment-ledger" {
		t.Fatal("active v9/v10 identity changed")
	}
	candidate := assignmentMigration()
	if candidate.Version != 10 || candidate.ID != "0010-assignment-ledger" {
		t.Fatal("candidate identity changed", candidate)
	}
	// Frozen applied checksums and candidate manifest: A2a never rewrites history.
	wantChecksums := []string{
		"23f6dda1108501084bff616ef2709e9596fe2bf684fd60e1b727ba698ba44d0b",
		"f36f56149dc69c4b0be984967cb568d57f9330d4ed275f59afbd1f673a3d3294",
		"104ff5813e462d75c6f0359c32030336d1397f8f16184e99e016762a25dfd571",
		"cd838271e3a1890210571d6b3634ca8067bcff6f3cd3b40937a0c2330b0c88ed",
		"c65137b653d80e54301a14fd7f8be52bdb7e246ec32245623cbf47d7bfa66fc7",
		"f3bd874bbb9d8ae2228ba91c3d2b5d5077d0110d69d13551a24c87e54558fcd1",
		"2b40000e9e445357008b1935d490f3ec7f901741ed5d9bae0638f59092ed5727",
		"4504d2bc70747671589c83a1036c63d66a190394e82207f5c74f14848b67544c",
		"3e888ebfa6299223cb2c8b762baec55f50b53f4953defff63d20fd5555201b8f",
		"f21c185d37f1c4c476340fbb87e19b20ef8f54b34b5368904ab1788a5b8d8d75",
	}
	for i, m := range assignmentSchemaMigrations() {
		if got := migrationChecksum(m); got != wantChecksums[i] {
			t.Errorf("v%d checksum = %s, want %s", i+1, got, wantChecksums[i])
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	db := registrationSchemaFixtureMigrations(t, SchemaMigrations()[:9])
	for _, n := range []int{1, 2} {
		catalogSchemaExec(t, db.db, `INSERT INTO tc_tags VALUES (?,?,7)`, catalogSchemaID("tag", n), bytes.Repeat([]byte{byte(n)}, 8))
	}
	catalogSchemaExec(t, db.db, `INSERT INTO tc_tag_observations VALUES (?,?,2,0,1,'legacy','1','0','2000-01-01T00:00:00.000000001Z',0,1)`, catalogSchemaID("obs", 1), catalogSchemaID("tag", 1))
	catalogSchemaExec(t, db.db, `INSERT INTO tc_tag_decisions VALUES (?,?,0,2,3)`, catalogSchemaID("dec", 1), catalogSchemaID("tag", 1))
	catalogSchemaExec(t, db.db, `INSERT INTO tc_tag_decision_support VALUES (?,?,?)`, catalogSchemaID("tag", 1), catalogSchemaID("dec", 1), catalogSchemaID("obs", 1))
	digest := bytes.Repeat([]byte{1}, 32)
	catalogSchemaExec(t, db.db, `INSERT INTO tc_blobs VALUES (?,1,4097,1)`, digest)
	catalogSchemaExec(t, db.db, `INSERT INTO tc_version_blobs VALUES (?,?)`, catalogSchemaID("ver", 1), digest)
	catalogSchemaExec(t, db.db, `INSERT INTO tc_blob_imports VALUES (?,1,?,?,?)`, catalogSchemaID("imp", 1), bytes.Repeat([]byte{7}, 137), digest, catalogSchemaID("ver", 1))
	before := registrationSchemaRows(t, db.db)
	objects := catalogSchemaRows(t, db.db, catalogRecoveryObjects)
	path := dbPathForSupersessionTest(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	migrations := assignmentSchemaMigrations()
	if rejected, err := Open(t.Context(), Config{Path: path}, migrations); !errors.Is(err, ErrUpgradeBackupRequired) {
		if rejected != nil {
			rejected.Close()
		}
		t.Fatal("v9 upgrade without backup accepted", err)
	}
	upgraded, err := Open(t.Context(), Config{Path: path, UpgradeBackupPath: filepath.Join(t.TempDir(), "pre-v10.sqlite")}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if version, err := upgraded.CurrentVersion(t.Context()); err != nil || version != 10 {
		t.Fatal("explicit candidate upgrade", version, err)
	}
	backup, ok := upgraded.UpgradeBackup()
	if !ok || backup.SchemaVersion != 9 {
		t.Fatal("missing v9 backup")
	}
	if err := VerifyBackup(t.Context(), backup, active); err != nil {
		t.Fatal("v9 backup verification", err)
	}
	for table, want := range before {
		query := "SELECT * FROM " + table + " ORDER BY 1,2"
		if table == "tc_schema_migrations" {
			query = "SELECT * FROM tc_schema_migrations WHERE version<=9 ORDER BY 1,2"
		}
		if got := catalogSchemaRows(t, upgraded.db, query); !reflect.DeepEqual(got, want) {
			t.Fatal("v9 values, types or bytes changed", table)
		}
	}
	for _, object := range objects {
		if !catalogSchemaContains(catalogSchemaRows(t, upgraded.db, catalogRecoveryObjects), object) {
			t.Fatal("candidate changed an existing schema object", object)
		}
	}
	for _, table := range assignmentSchemaTables {
		var strict, count int
		if err := upgraded.db.QueryRowContext(t.Context(), `SELECT strict FROM pragma_table_list WHERE name=?`, table).Scan(&strict); err != nil || strict != 1 {
			t.Fatal("not STRICT", table, err)
		}
		if err := upgraded.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatal("candidate manufactured assignment history", table, count, err)
		}
	}
	if err := verifyForeignKeys(t.Context(), upgraded.db); err != nil {
		t.Fatal(err)
	}
	current := registrationSchemaRows(t, upgraded.db)
	if err := upgraded.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), Config{Path: path}, migrations)
	if err != nil {
		t.Fatal("explicit candidate reopen", err)
	}
	if _, created := reopened.UpgradeBackup(); created || !reflect.DeepEqual(current, registrationSchemaRows(t, reopened.db)) {
		t.Fatal("candidate reopen changed rows or made another backup")
	}
	reopened.Close()
	if rejected, err := Open(t.Context(), Config{Path: path}, SchemaMigrations()[:9]); !errors.Is(err, ErrSchemaNewer) {
		if rejected != nil {
			rejected.Close()
		}
		t.Fatal("source-only v9 runtime accepted v10", err)
	}
	// A normal fresh runtime activates v10 without manufacturing history.
	normal, err := Open(t.Context(), Config{Path: filepath.Join(t.TempDir(), "normal.sqlite")}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	defer normal.Close()
	if version, err := normal.CurrentVersion(t.Context()); err != nil || version != 10 {
		t.Fatal("default runtime did not activate v10", version, err)
	}
	for _, table := range assignmentSchemaTables {
		var count int
		if err := normal.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatal("fresh runtime manufactured assignment history", table, count, err)
		}
	}
}

func TestAssignmentSchemaManifest(t *testing.T) {
	db := assignmentSchemaFixture(t)
	wantIndexes := map[string]string{
		"tc_assignment_events_effective": "assignment_id,effective_seconds,effective_nanos,revision",
		"tc_assignment_events_target":    "content_id,version_id",
		"tc_assignment_evidence_history": "assignment_id,introduced_revision,evidence_id",
		"tc_assignment_evidence_target":  "content_id,version_id",
		"tc_assignment_support_evidence": "assignment_id,tag_id,evidence_id",
	}
	wantObjects := map[string]string{}
	for _, table := range assignmentSchemaTables {
		wantObjects[table] = "table"
	}
	for name, want := range wantIndexes {
		wantObjects[name] = "index"
		var columns []string
		for _, row := range catalogSchemaRows(t, db.db, "PRAGMA index_info("+name+")") {
			columns = append(columns, row[2].(string))
		}
		if got := strings.Join(columns, ","); got != want {
			t.Fatalf("%s columns = %s, want %s", name, got, want)
		}
	}
	for _, table := range []string{"tc_assignments", "tc_assignment_events", "tc_assignment_evidence", "tc_assignment_support", "tc_assignment_commands"} {
		wantObjects[table+"_no_update"] = "trigger"
		wantObjects[table+"_no_delete"] = "trigger"
	}
	gotObjects := map[string]string{}
	for _, row := range catalogSchemaRows(t, db.db, `SELECT name,type FROM sqlite_schema WHERE name LIKE 'tc_assignment%'`) {
		gotObjects[row[0].(string)] = row[1].(string)
	}
	if !reflect.DeepEqual(gotObjects, wantObjects) {
		t.Fatalf("additive object manifest: got %v, want %v", gotObjects, wantObjects)
	}
	wantFKs := map[string][]string{
		"tc_assignments": {"tag_id->tc_tags(tag_id)"},
		"tc_assignment_events": {
			"assignment_id,tag_id->tc_assignments(assignment_id,tag_id)",
			"content_id->tc_catalog_content(content_id)",
			"content_id,version_id->tc_content_versions(content_id,version_id)",
		},
		"tc_assignment_evidence": {
			"assignment_id,tag_id,introduced_revision->tc_assignment_events(assignment_id,tag_id,revision)",
			"content_id->tc_catalog_content(content_id)",
			"content_id,version_id->tc_content_versions(content_id,version_id)",
		},
		"tc_assignment_support": {
			"assignment_id,tag_id,revision->tc_assignment_events(assignment_id,tag_id,revision)",
			"assignment_id,tag_id,evidence_id->tc_assignment_evidence(assignment_id,tag_id,evidence_id)",
		},
		"tc_assignment_commands":    {"assignment_id,tag_id,revision,command_id->tc_assignment_events(assignment_id,tag_id,revision,command_id)"},
		"tc_assignment_heads":       {"assignment_id,tag_id,head_revision->tc_assignment_events(assignment_id,tag_id,revision)"},
		"tc_assignment_projections": {"assignment_id,tag_id,head_revision->tc_assignment_events(assignment_id,tag_id,revision)"},
	}
	for table, want := range wantFKs {
		groups := map[int64][]string{}
		targets := map[int64][]string{}
		owners := map[int64]string{}
		for _, row := range catalogSchemaRows(t, db.db, "PRAGMA foreign_key_list("+table+")") {
			if row[5] != "RESTRICT" || row[6] != "RESTRICT" {
				t.Fatal("nonrestrictive assignment FK", table, row)
			}
			id := row[0].(int64)
			groups[id] = append(groups[id], row[3].(string))
			targets[id] = append(targets[id], row[4].(string))
			owners[id] = row[2].(string)
		}
		var got []string
		for id, from := range groups {
			got = append(got, strings.Join(from, ",")+"->"+owners[id]+"("+strings.Join(targets[id], ",")+")")
		}
		sort.Strings(got)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s FKs = %v, want %v", table, got, want)
		}
	}
	// Each call returns independently owned statements, like SchemaMigrations.
	changed := assignmentMigration()
	changed.Statements[0] = "modified"
	if assignmentMigration().Statements[0] == "modified" || len(SchemaMigrations()) != 10 {
		t.Fatal("migration aliases mutable state or activation changed")
	}
}

func TestAssignmentRestrictiveReferences(t *testing.T) {
	db := assignmentSchemaFixture(t)
	for _, n := range []int{1, 2} {
		assignmentSchemaAggregate(t, db.db, n)
		assignmentSchemaEvent(t, db.db, n, 1)
		catalogSchemaExec(t, db.db, assignmentSchemaEvidenceInsert, assignmentSchemaEvidenceArgs(n)...)
	}
	asn, tag, evidence := catalogSchemaID("asn", 1), catalogSchemaID("tag", 1), catalogSchemaID("evd", 1)
	t.Run("aggregate missing Tag", func(t *testing.T) {
		assignmentSchemaReject(t, db.db, 787, `INSERT INTO tc_assignments VALUES (?,?)`, catalogSchemaID("asn", 9), catalogSchemaID("tag", 9))
	})
	for _, tc := range []struct {
		name  string
		index int
		value any
	}{
		{"missing aggregate", 0, catalogSchemaID("asn", 9)},
		{"foreign aggregate Tag", 1, catalogSchemaID("tag", 2)},
		{"missing Tag", 1, catalogSchemaID("tag", 9)},
		{"missing content", 4, catalogSchemaID("cnt", 9)},
		{"missing version", 5, catalogSchemaID("ver", 9)},
		{"foreign content version", 4, catalogSchemaID("cnt", 2)},
	} {
		t.Run("event/"+tc.name, func(t *testing.T) {
			args := assignmentSchemaEventArgs(1, 2)
			args[tc.index] = tc.value
			assignmentSchemaReject(t, db.db, 787, assignmentSchemaEventInsert, args...)
		})
	}
	for _, tc := range []struct {
		name  string
		index int
		value any
	}{
		{"missing aggregate", 1, catalogSchemaID("asn", 9)},
		{"foreign Tag", 2, catalogSchemaID("tag", 2)},
		{"missing event", 3, 9},
		{"missing content", 6, catalogSchemaID("cnt", 9)},
		{"missing version", 7, catalogSchemaID("ver", 9)},
		{"foreign content version", 6, catalogSchemaID("cnt", 2)},
	} {
		t.Run("evidence/"+tc.name, func(t *testing.T) {
			args := assignmentSchemaEvidenceArgs(1)
			args[0] = catalogSchemaID("evd", 9)
			args[tc.index] = tc.value
			assignmentSchemaReject(t, db.db, 787, assignmentSchemaEvidenceInsert, args...)
		})
	}
	for _, tc := range []struct {
		name            string
		assignment, tag string
		revision        int
		evidence        string
	}{
		{"missing evidence", asn, tag, 1, catalogSchemaID("evd", 9)},
		{"foreign subject evidence", asn, tag, 1, catalogSchemaID("evd", 2)},
		{"foreign event Tag", asn, catalogSchemaID("tag", 2), 1, evidence},
		{"foreign aggregate evidence", catalogSchemaID("asn", 2), catalogSchemaID("tag", 2), 1, evidence},
		{"missing event", asn, tag, 9, evidence},
		{"missing aggregate", catalogSchemaID("asn", 9), tag, 1, evidence},
	} {
		t.Run("support/"+tc.name, func(t *testing.T) {
			assignmentSchemaReject(t, db.db, 787, `INSERT INTO tc_assignment_support VALUES (?,?,?,?)`, tc.assignment, tc.tag, tc.revision, tc.evidence)
		})
	}
	catalogSchemaExec(t, db.db, `INSERT INTO tc_assignment_support VALUES (?,?,1,?)`, asn, tag, evidence)
	for _, index := range []int{0, 1, 2, 3} {
		t.Run(fmt.Sprintf("command foreign binding/%d", index), func(t *testing.T) {
			args := assignmentSchemaCommandArgs(1, 1)
			switch index {
			case 0:
				args[0] = fmt.Sprintf("%032x", 999)
			case 1:
				args[1] = catalogSchemaID("asn", 9)
			case 2:
				args[2] = catalogSchemaID("tag", 2)
			case 3:
				args[3] = 9
			}
			assignmentSchemaReject(t, db.db, 787, assignmentSchemaCommandInsert, args...)
		})
	}
	for _, table := range []string{"tc_assignment_heads", "tc_assignment_projections"} {
		for _, foreignTag := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/foreignTag=%t", table, foreignTag), func(t *testing.T) {
				refTag, rev := tag, 9
				if foreignTag {
					refTag, rev = catalogSchemaID("tag", 2), 1
				}
				if table == "tc_assignment_heads" {
					assignmentSchemaReject(t, db.db, 787, `INSERT INTO tc_assignment_heads VALUES (?,?,?,0,?,0,0,100)`, asn, refTag, rev, rev)
				} else {
					assignmentSchemaReject(t, db.db, 787, `INSERT INTO tc_assignment_projections VALUES (?,?,?,1,?,?)`, asn, refTag, rev, []byte("projection"), bytes.Repeat([]byte{1}, 32))
				}
			})
		}
	}
	// Pin a third, unbound target solely through retained original-target evidence.
	args := assignmentSchemaEvidenceArgs(1)
	args[0], args[6], args[7] = catalogSchemaID("evd", 3), catalogSchemaID("cnt", 3), catalogSchemaID("ver", 3)
	catalogSchemaExec(t, db.db, assignmentSchemaEvidenceInsert, args...)
	for _, tc := range []struct {
		query string
		arg   any
	}{
		{`DELETE FROM tc_tags WHERE tag_id=?`, tag},
		{`DELETE FROM tc_catalog_content WHERE content_id=?`, catalogSchemaID("cnt", 1)},
		{`DELETE FROM tc_content_versions WHERE version_id=?`, catalogSchemaID("ver", 1)},
		{`DELETE FROM tc_content_versions WHERE version_id=?`, catalogSchemaID("ver", 3)},
		{`UPDATE tc_tags SET tag_id='tag_00000000000000000000000009' WHERE tag_id=?`, tag},
		{`UPDATE tc_content_versions SET content_id='cnt_00000000000000000000000002' WHERE version_id=?`, catalogSchemaID("ver", 1)},
	} {
		assignmentSchemaReject(t, db.db, 787, tc.query, tc.arg)
	}
	if err := verifyForeignKeys(t.Context(), db.db); err != nil {
		t.Fatal(err)
	}
}

func TestAssignmentSchemaConstraints(t *testing.T) {
	db := assignmentSchemaFixture(t)
	for _, n := range []int{1, 2} {
		assignmentSchemaAggregate(t, db.db, n)
		assignmentSchemaEvent(t, db.db, n, 1)
	}
	assignmentSchemaReject(t, db.db, 2067, `INSERT INTO tc_assignments VALUES (?,?)`, catalogSchemaID("asn", 3), catalogSchemaID("tag", 1))
	assignmentSchemaReject(t, db.db, 1555, `INSERT INTO tc_assignments VALUES (?,?)`, catalogSchemaID("asn", 1), catalogSchemaID("tag", 3))
	for _, id := range []string{"asn_1", "cnt_" + strings.Repeat("0", 26), "asn_" + strings.Repeat("u", 26), "asn_" + strings.Repeat("0", 25) + "\x00"} {
		assignmentSchemaReject(t, db.db, 275, `INSERT INTO tc_assignments VALUES (?,?)`, id, catalogSchemaID("tag", 3))
	}
	fullInsert := `INSERT INTO tc_assignment_events VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	base := []any{catalogSchemaID("asn", 1), catalogSchemaID("tag", 1), 2, fmt.Sprintf("%032x", 102), 1, 10, 1, 20, 2,
		catalogSchemaID("cnt", 1), catalogSchemaID("ver", 1), nil, 1, []byte("event"), bytes.Repeat([]byte{1}, 32), 0, 0, 100}
	for _, tc := range []struct {
		name  string
		index int
		value any
		code  int
	}{
		{"zero revision", 2, 0, 275}, {"revision ceiling", 2, 4097, 275}, {"fractional revision", 2, 1.5, 3091},
		{"zero command", 3, strings.Repeat("0", 32), 275}, {"uppercase command", 3, strings.Repeat("A", 32), 275},
		{"NUL command", 3, strings.Repeat("1", 31) + "\x00", 275}, {"short command", 3, "1", 275},
		{"unknown kind", 4, 8, 275}, {"zero kind", 4, 0, 275}, {"early time", 5, int64(-62135596801), 275},
		{"late time", 7, int64(253402300800), 275}, {"negative nanos", 6, -1, 275}, {"nanos ceiling", 8, 1000000000, 275},
		{"half target content", 9, nil, 275}, {"half target version", 10, nil, 275},
		{"nonreview review ID", 11, catalogSchemaID("rev", 1), 275}, {"unknown encoding", 12, 2, 275},
		{"empty event", 13, []byte{}, 275}, {"text event", 13, "event", 3091}, {"NULL event", 13, nil, 1299},
		{"short digest", 14, []byte{1}, 275}, {"text digest", 14, strings.Repeat("1", 32), 3091},
		{"negative observations", 15, -1, 275}, {"new observation ceiling", 15, 65, 275},
		{"negative support", 16, -1, 275}, {"selected support ceiling", 16, 65, 275},
		{"zero charge", 17, 0, 275}, {"charge ceiling", 17, 16777217, 275},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]any(nil), base...)
			args[tc.index] = tc.value
			assignmentSchemaReject(t, db.db, tc.code, fullInsert, args...)
		})
	}
	t.Run("event revision and global command fences", func(t *testing.T) {
		args := assignmentSchemaEventArgs(1, 1)
		args[3] = fmt.Sprintf("%032x", 999)
		assignmentSchemaReject(t, db.db, 0, assignmentSchemaEventInsert, args...)
		args = assignmentSchemaEventArgs(2, 2)
		args[3] = fmt.Sprintf("%032x", 101)
		assignmentSchemaReject(t, db.db, 2067, assignmentSchemaEventInsert, args...)
	})
	// Every application kind has a storage shape, including reviews and tombstone.
	for kind := 1; kind <= 7; kind++ {
		args := append([]any(nil), base...)
		args[2], args[3], args[4] = kind+1, fmt.Sprintf("%032x", 100+kind+1), kind
		if kind != 1 && kind != 5 && kind != 6 {
			args[9], args[10] = nil, nil
		}
		if kind >= 3 && kind <= 6 {
			args[11] = catalogSchemaID("rev", kind)
		}
		catalogSchemaExec(t, db.db, fullInsert, args...)
	}
	t.Run("global review identity", func(t *testing.T) {
		args := append([]any(nil), base...)
		args[0], args[1], args[2], args[3], args[4] = catalogSchemaID("asn", 2), catalogSchemaID("tag", 2), 2, fmt.Sprintf("%032x", 202), 3
		args[9], args[10], args[11] = nil, nil, catalogSchemaID("rev", 3)
		assignmentSchemaReject(t, db.db, 2067, fullInsert, args...)
	})
	for _, n := range []int{1, 2} {
		catalogSchemaExec(t, db.db, assignmentSchemaEvidenceInsert, assignmentSchemaEvidenceArgs(n)...)
		catalogSchemaExec(t, db.db, assignmentSchemaCommandInsert, assignmentSchemaCommandArgs(n, 1)...)
	}
	args := assignmentSchemaEvidenceArgs(1)
	args[1], args[2] = catalogSchemaID("asn", 2), catalogSchemaID("tag", 2)
	assignmentSchemaReject(t, db.db, 1555, assignmentSchemaEvidenceInsert, args...)
	assignmentSchemaReject(t, db.db, 0, assignmentSchemaCommandInsert, assignmentSchemaCommandArgs(1, 1)...)
	args = assignmentSchemaCommandArgs(1, 2)
	args[0] = fmt.Sprintf("%032x", 201) // existing ID on the other aggregate
	assignmentSchemaReject(t, db.db, 0, assignmentSchemaCommandInsert, args...)
	asn, tag, evd := catalogSchemaID("asn", 1), catalogSchemaID("tag", 1), catalogSchemaID("evd", 1)
	catalogSchemaExec(t, db.db, `INSERT INTO tc_assignment_support VALUES (?,?,1,?)`, asn, tag, evd)
	assignmentSchemaReject(t, db.db, 1555, `INSERT INTO tc_assignment_support VALUES (?,?,1,?)`, asn, tag, evd)
	t.Run("bounded usage", func(t *testing.T) {
		query := `INSERT INTO tc_assignment_heads VALUES (?,?,1,0,?,?,?,?)`
		for _, values := range [][]any{{0, 0, 0, 1}, {2, 0, 0, 1}, {1, -1, 0, 1}, {1, 4097, 0, 1}, {1, 0, -1, 1}, {1, 0, 16385, 1}, {1, 0, 0, 0}, {1, 0, 0, 16777217}} {
			args := append([]any{asn, tag}, values...)
			assignmentSchemaReject(t, db.db, 275, query, args...)
		}
		// These are SQL column ceilings, not claims of independently reachable
		// combined usage under the complete canonical accounting contract.
		catalogSchemaExec(t, db.db, query, asn, tag, 1, 4096, 16384, 16777216)
	})
	// Heads and projections can be rebuilt/advanced; history remains untouched.
	catalogSchemaExec(t, db.db, `INSERT INTO tc_assignment_projections VALUES (?,?,1,1,?,?)`, asn, tag, []byte("projection"), bytes.Repeat([]byte{1}, 32))
	catalogSchemaExec(t, db.db, `UPDATE tc_assignment_heads SET head_revision=8,event_count=8,retired=1 WHERE assignment_id=?`, asn)
	catalogSchemaExec(t, db.db, `UPDATE tc_assignment_projections SET head_revision=8,canonical_projection=? WHERE assignment_id=?`, []byte("rebuilt"), asn)
	for _, tc := range []struct{ table, column string }{
		{"tc_assignments", "assignment_id"}, {"tc_assignment_events", "canonical_event"}, {"tc_assignment_evidence", "canonical_evidence"},
		{"tc_assignment_support", "evidence_id"}, {"tc_assignment_commands", "canonical_result"},
	} {
		t.Run("immutable/"+tc.table, func(t *testing.T) {
			assignmentSchemaReject(t, db.db, 1811, "UPDATE "+tc.table+" SET "+tc.column+"="+tc.column)
			assignmentSchemaReject(t, db.db, 1811, "DELETE FROM "+tc.table)
		})
	}
	if err := verifyForeignKeys(t.Context(), db.db); err != nil {
		t.Fatal(err)
	}
}

func TestAssignmentSchemaBlobEnvelopes(t *testing.T) {
	db := assignmentSchemaFixture(t)
	assignmentSchemaAggregate(t, db.db, 1)
	assignmentSchemaEvent(t, db.db, 1, 1)
	// These independently rolled-back SQL envelopes are NOT complete, reachable
	// aggregate boundary fixtures. A2b must charge every retained copy together.
	large := bytes.Repeat([]byte{1}, (16<<20)+1)
	for _, tc := range []struct {
		name, query string
		args        []any
		index       int
	}{
		{"event", assignmentSchemaEventInsert, assignmentSchemaEventArgs(1, 2), 6},
		{"evidence", assignmentSchemaEvidenceInsert, assignmentSchemaEvidenceArgs(1), 4},
		{"command", assignmentSchemaCommandInsert, assignmentSchemaCommandArgs(1, 1), 4},
		{"result", assignmentSchemaCommandInsert, assignmentSchemaCommandArgs(1, 1), 5},
		{"projection", `INSERT INTO tc_assignment_projections VALUES (?,?,1,1,?,?)`,
			[]any{catalogSchemaID("asn", 1), catalogSchemaID("tag", 1), []byte("projection"), bytes.Repeat([]byte{1}, 32)}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, payload := range []any{large[:16<<20], large, []byte{}, "text"} {
				tx, err := db.db.BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				args := append([]any(nil), tc.args...)
				args[tc.index] = payload
				_, writeErr := tx.ExecContext(t.Context(), tc.query, args...)
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				b, isBytes := payload.([]byte)
				valid := isBytes && len(b) == 16<<20
				if valid && writeErr != nil {
					t.Fatal("16 MiB SQL envelope rejected", writeErr)
				}
				if !valid {
					var coded interface{ Code() int }
					want := 275
					if !isBytes {
						want = 3091
					}
					if !errors.As(writeErr, &coded) || coded.Code() != want {
						t.Fatalf("invalid envelope: want constraint %d, got %v", want, writeErr)
					}
				}
			}
		})
	}
}

func TestAssignmentSchemaMigrationFailure(t *testing.T) {
	db := registrationSchemaFixtureMigrations(t, SchemaMigrations()[:9])
	before := registrationSchemaRows(t, db.db)
	objects := catalogSchemaRows(t, db.db, catalogRecoveryObjects)
	path := dbPathForSupersessionTest(t, db)
	db.Close()
	broken := assignmentSchemaMigrations()
	broken[9].Statements = append(broken[9].Statements, `INSERT INTO no_such_assignment_table VALUES (1)`)
	opened, err := Open(t.Context(), Config{Path: path, UpgradeBackupPath: filepath.Join(t.TempDir(), "pre-failed-v10.sqlite")}, broken)
	if opened != nil {
		opened.Close()
	}
	var upgrade *UpgradeError
	if !errors.As(err, &upgrade) {
		t.Fatal("expected failed upgrade with retained backup", err)
	}
	if err := VerifyBackup(t.Context(), upgrade.Backup, SchemaMigrations()); err != nil {
		t.Fatal(err)
	}
	// Inspection uses the same adapter connection setup, without retrying migration.
	dsn, _, err := connectionDSN(Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if got := catalogSchemaRows(t, raw, catalogRecoveryObjects); !reflect.DeepEqual(got, objects) {
		t.Fatal("failed candidate left partial schema")
	}
	for table, want := range before {
		query := "SELECT * FROM " + table + " ORDER BY 1,2"
		if table == "tc_schema_migrations" {
			query = "SELECT * FROM tc_schema_migrations WHERE version<=9 ORDER BY 1,2"
		}
		if !reflect.DeepEqual(catalogSchemaRows(t, raw, query), want) {
			t.Fatal("failed candidate changed v9 data", table)
		}
	}
	var dirty int
	if err := raw.QueryRowContext(t.Context(), `SELECT dirty FROM tc_schema_migrations WHERE version=10`).Scan(&dirty); err != nil || dirty != 1 {
		t.Fatal("missing dirty candidate ledger entry", dirty, err)
	}
	if opened, err := Open(t.Context(), Config{Path: path}, assignmentSchemaMigrations()); !errors.Is(err, ErrDirtyMigration) {
		if opened != nil {
			opened.Close()
		}
		t.Fatal("dirty candidate accepted", err)
	}
}
