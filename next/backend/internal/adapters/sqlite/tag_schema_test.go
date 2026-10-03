package sqlite

import (
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	applicationcatalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
)

func TestTagSchemaUpgradePreservesContentAndChecksum(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "content.sqlite")
	backupPath := filepath.Join(directory, "before-tags.sqlite")
	migrations := SchemaMigrations()
	content := testContent(t, "Retained content", true)

	versionOne, err := Open(t.Context(), Config{Path: path}, migrations[:1])
	if err != nil {
		t.Fatal(err)
	}
	if err := versionOne.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		return repository.Save(t.Context(), content)
	}); err != nil {
		t.Fatal(err)
	}
	before := tagSchemaLedgerChecksum(t, versionOne, 1)
	if before != migrationChecksum(migrations[0]) {
		t.Fatal("version 1 checksum differs from canonical migration")
	}
	if err := versionOne.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(t.Context(), Config{Path: path, UpgradeBackupPath: backupPath}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if got := currentVersion(t, upgraded); got != len(migrations) {
		t.Fatalf("upgraded version = %d, want %d", got, len(migrations))
	}
	snapshot, ok := upgraded.UpgradeBackup()
	if !ok || snapshot.Path != backupPath || snapshot.SchemaVersion != 1 {
		t.Fatalf("upgrade backup = %+v, present %t", snapshot, ok)
	}
	if err := VerifyBackup(t.Context(), snapshot, migrations); err != nil {
		t.Fatalf("verify pre-upgrade backup: %v", err)
	}
	if got := tagSchemaLedgerChecksum(t, upgraded, 1); got != before {
		t.Fatalf("version 1 checksum changed: %q -> %q", before, got)
	}
	if got := tagSchemaLedgerChecksum(t, upgraded, 2); got != migrationChecksum(migrations[1]) {
		t.Fatal("version 2 checksum differs from canonical migration")
	}
	backup := openRawDatabase(t, backupPath)
	defer backup.Close()
	var backupChecksum, backupTitle string
	if err := backup.QueryRowContext(t.Context(), `SELECT checksum FROM tc_schema_migrations WHERE version = 1 AND dirty = 0`).Scan(&backupChecksum); err != nil {
		t.Fatal(err)
	}
	if err := backup.QueryRowContext(t.Context(), `SELECT title FROM tc_catalog_content WHERE content_id = ?`, content.ID().String()).Scan(&backupTitle); err != nil {
		t.Fatal(err)
	}
	if backupChecksum != before || backupTitle != content.Facts().Title() {
		t.Fatalf("pre-upgrade backup lost content or checksum: %q, %q", backupChecksum, backupTitle)
	}

	var retainedTitle string
	if err := upgraded.db.QueryRowContext(t.Context(), `SELECT title FROM tc_catalog_content WHERE content_id = ?`, content.ID().String()).Scan(&retainedTitle); err != nil {
		t.Fatal(err)
	}
	if retainedTitle != content.Facts().Title() {
		t.Fatalf("upgraded content title = %q, want %q", retainedTitle, content.Facts().Title())
	}

	var retainedContent = content
	if err := upgraded.WithinTransaction(t.Context(), func(repository applicationcatalog.ContentRepository) error {
		var found bool
		var err error
		retainedContent, found, err = repository.FindByID(t.Context(), content.ID())
		if err == nil && !found {
			t.Fatal("content absent after upgrade")
		}
		return err
	}); err != nil || !reflect.DeepEqual(retainedContent, content) {
		t.Fatalf("content after upgrade = %#v, %v", retainedContent, err)
	}
}

func TestTagSchemaFreshReopenAndConstraints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tags.sqlite")
	migrations := SchemaMigrations()
	database, err := Open(t.Context(), Config{Path: path}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if got := currentVersion(t, database); got != len(migrations) {
		t.Fatalf("fresh version = %d, want %d", got, len(migrations))
	}
	id := "tag_" + strings.Repeat("0", 26)
	uid := []byte{0, 1, 2, 3, 4, 5, 6, 7}
	if _, err := database.db.ExecContext(t.Context(),
		`INSERT INTO tc_tags (tag_id, uid, revision) VALUES (?, ?, ?)`, id, uid, 1,
	); err != nil {
		t.Fatalf("insert valid identity: %v", err)
	}
	if _, err := database.db.ExecContext(t.Context(),
		`INSERT INTO tc_tags (tag_id, uid, revision) VALUES (?, ?, ?)`,
		"tag_"+strings.Repeat("1", 26), []byte{8, 9, 10, 11, 12, 13, 14, 15}, int64(math.MaxInt64),
	); err != nil {
		t.Fatalf("insert maximum signed revision: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(t.Context(), Config{Path: path}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	assertTagSchemaShape(t, reopened)
	var storedUID []byte
	var uidType string
	var revision int64
	if err := reopened.db.QueryRowContext(t.Context(),
		`SELECT uid, typeof(uid), revision FROM tc_tags WHERE tag_id = ?`, id,
	).Scan(&storedUID, &uidType, &revision); err != nil {
		t.Fatal(err)
	}
	if uidType != "blob" || len(storedUID) != 8 || string(storedUID) != string(uid) || revision != 1 {
		t.Fatalf("reopened identity = (%q, %x, %d)", uidType, storedUID, revision)
	}
	for _, tc := range []struct {
		name string
		id   any
		uid  any
		rev  any
	}{
		{"duplicate UID", "tag_" + strings.Repeat("2", 26), uid, 1},
		{"duplicate TagID", id, []byte{20, 21, 22, 23, 24, 25, 26, 27}, 1},
		{"short UID", "tag_" + strings.Repeat("3", 26), []byte{1, 2, 3, 4, 5, 6, 7}, 1},
		{"long UID", "tag_" + strings.Repeat("4", 26), []byte{1, 2, 3, 4, 5, 6, 7, 8, 9}, 1},
		{"text UID", "tag_" + strings.Repeat("5", 26), "12345678", 1},
		{"null UID", "tag_" + strings.Repeat("6", 26), nil, 1},
		{"zero revision", "tag_" + strings.Repeat("7", 26), []byte{7, 7, 7, 7, 7, 7, 7, 7}, 0},
		{"negative revision", "tag_" + strings.Repeat("8", 26), []byte{8, 8, 8, 8, 8, 8, 8, 8}, -1},
		{"null revision", "tag_" + strings.Repeat("9", 26), []byte{9, 9, 9, 9, 9, 9, 9, 9}, nil},
		{"short TagID", "tag_0", []byte{10, 10, 10, 10, 10, 10, 10, 10}, 1},
		{"wrong prefix", "cnt_" + strings.Repeat("0", 26), []byte{11, 11, 11, 11, 11, 11, 11, 11}, 1},
		{"uppercase TagID", "tag_" + strings.Repeat("A", 26), []byte{12, 12, 12, 12, 12, 12, 12, 12}, 1},
		{"invalid alphabet", "tag_" + strings.Repeat("u", 26), []byte{13, 13, 13, 13, 13, 13, 13, 13}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := reopened.db.ExecContext(t.Context(),
				`INSERT INTO tc_tags (tag_id, uid, revision) VALUES (?, ?, ?)`,
				tc.id, tc.uid, tc.rev,
			); err == nil {
				t.Fatal("invalid identity inserted")
			}
		})
	}
}

func TestTagSchemaMigrationsDefensiveCopy(t *testing.T) {
	first := SchemaMigrations()
	if len(first) != 3 {
		t.Fatalf("migration count = %d, want 3", len(first))
	}
	first[1].ID = "modified"
	first[1].Statements[0] = "modified"
	first = append(first, Migration{Version: 3, ID: "caller-only"})
	second := SchemaMigrations()
	if len(second) != 3 || second[1].ID != "0002-tag-identity" || !strings.Contains(second[1].Statements[0], "CREATE TABLE tc_tags") {
		t.Fatal("caller changed canonical tag migration")
	}
}

func tagSchemaLedgerChecksum(t *testing.T, database *Database, version int) string {
	t.Helper()
	var checksum string
	if err := database.db.QueryRowContext(t.Context(),
		`SELECT checksum FROM tc_schema_migrations WHERE version = ? AND dirty = 0`, version,
	).Scan(&checksum); err != nil {
		t.Fatal(err)
	}
	return checksum
}

func assertTagSchemaShape(t *testing.T, database *Database) {
	t.Helper()
	rows, err := database.db.QueryContext(t.Context(), `PRAGMA table_info(tc_tags)`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var index, notNull, primary int
		var name, typeName string
		var defaultValue any
		if err := rows.Scan(&index, &name, &typeName, &notNull, &defaultValue, &primary); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, []string{"tag_id", "uid", "revision"}) {
		t.Fatalf("tag columns = %v", names)
	}
	foreignKeys, err := database.db.QueryContext(t.Context(), `PRAGMA foreign_key_list(tc_tags)`)
	if err != nil {
		t.Fatal(err)
	}
	if foreignKeys.Next() {
		foreignKeys.Close()
		t.Fatal("tag identity has a foreign key")
	}
	if err := foreignKeys.Err(); err != nil {
		foreignKeys.Close()
		t.Fatal(err)
	}
	if err := foreignKeys.Close(); err != nil {
		t.Fatal(err)
	}
	var extraTables int
	if err := database.db.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM sqlite_schema WHERE type = 'table' AND name LIKE 'tc_tag_%' AND name <> 'tc_tags'`,
	).Scan(&extraTables); err != nil {
		t.Fatal(err)
	}
	if extraTables != 3 {
		t.Fatalf("unexpected tag metadata/evidence tables = %d", extraTables)
	}
}
