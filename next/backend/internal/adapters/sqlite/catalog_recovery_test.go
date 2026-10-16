package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/application/contentstore"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
)

// This is the schema-upgrade slice only; snapshot restore and process recovery
// deliberately remain separate acceptance work.
func TestCatalogSchemaRecovery(t *testing.T) {
	migrations := SchemaMigrations()
	if len(migrations) != 6 || migrations[5].ID != "0006-catalog-aggregate-state" {
		t.Fatal("expected the admitted v6 schema")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "v4.sqlite")
	owner := catalogRecoveryV4(t, path)
	before := catalogV4Rows(t, owner.database.db)
	for table, rows := range before {
		if len(rows) == 0 {
			t.Fatalf("v4 fixture does not exercise %s", table)
		}
	}
	objects := catalogSchemaRows(t, owner.database.db, catalogRecoveryObjects)
	if err := verifyForeignKeys(t.Context(), owner.database.db); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	// Compare the complete upgraded object definitions (including triggers and
	// indexes) with a fresh schema built by the same real ordered migrations.
	fresh, err := Open(t.Context(), Config{Path: filepath.Join(dir, "fresh-v6.sqlite")}, migrations)
	if err != nil {
		t.Fatal(err)
	}
	wantObjects := catalogSchemaRows(t, fresh.db, catalogRecoveryObjects)
	if err := fresh.Close(); err != nil {
		t.Fatal(err)
	}

	var ledger [][]any
	for attempt := 0; attempt < 3; attempt++ {
		config := Config{Path: path}
		if attempt == 0 {
			config.UpgradeBackupPath = filepath.Join(dir, "required-pre-upgrade.sqlite")
		}
		upgraded, err := OpenLifecycleOwner(t.Context(), config, migrations)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = upgraded.Close(context.Background()) })
		if version, err := upgraded.CurrentVersion(t.Context()); err != nil || version != 6 {
			t.Fatalf("open %d version: %d %v", attempt, version, err)
		}
		if got := catalogV4Rows(t, upgraded.database.db); !reflect.DeepEqual(got, before) {
			t.Fatalf("open %d changed v4 values, storage types or ledger bytes", attempt)
		}
		gotObjects := catalogSchemaRows(t, upgraded.database.db, catalogRecoveryObjects)
		if !reflect.DeepEqual(gotObjects, wantObjects) {
			t.Fatal("upgraded tables, indexes or triggers differ from fresh v6")
		}
		for _, object := range objects {
			if !catalogSchemaContains(gotObjects, object) {
				t.Fatal("preexisting schema object changed")
			}
		}
		applied, err := readAppliedMigrations(t.Context(), upgraded.database.db)
		if err != nil || len(applied) != 6 {
			t.Fatalf("migration ledger: %+v %v", applied, err)
		}
		for i, row := range applied {
			if row.version != i+1 || row.id != migrations[i].ID || row.dirty || row.checksum != migrationChecksum(migrations[i]) {
				t.Fatalf("migration %d identity, checksum or clean state changed: %+v", i+1, row)
			}
		}
		gotLedger := catalogSchemaRows(t, upgraded.database.db, "SELECT * FROM tc_schema_migrations ORDER BY version")
		if attempt == 0 {
			ledger = gotLedger
		} else if !reflect.DeepEqual(gotLedger, ledger) {
			t.Fatal("same-version open rewrote the migration ledger")
		}
		if err := verifyForeignKeys(t.Context(), upgraded.database.db); err != nil {
			t.Fatal(err)
		}
		for n := 1; n <= 2; n++ {
			command := blobCommand(t, n, n == 1)
			if result, found, err := blobLookup(t, upgraded, blobKey(t, n), command); err != nil || !found || result != importResult(command) {
				t.Fatalf("retained receipt %d: %v %v", n, found, err)
			}
		}
		content := testContent(t, "", false).ID()
		result, err := catalogRead(t, upgraded, app.Query{Kind: app.GetContent, Content: content, Page: app.PageRequest{Limit: 1}})
		if err != nil || result.Kind != app.Found || len(result.Entries) != 1 || result.Entries[0].Review != app.CatalogUnmanaged || result.Entries[0].Title.Status != catalog.CatalogFactUnknown || result.Entries[0].ProtectedCustom {
			t.Fatalf("upgrade invented review authority: %+v %v", result, err)
		}
		err = upgraded.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
			subject, err := catalog.ContentSubject(content)
			if err != nil {
				return err
			}
			if _, found, err := r.LoadReview(t.Context(), subject); err != nil || found {
				t.Fatalf("migration invented review state: %t %v", found, err)
			}
			if _, found, err := r.LoadOrder(t.Context(), content); err != nil || found {
				t.Fatalf("migration invented order state: %t %v", found, err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, table := range append(append([]string{}, catalogFoundationTables...), "tc_catalog_review_states", "tc_catalog_order_states", "tc_catalog_order_state_versions") {
			var count int
			if err := upgraded.database.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
				t.Fatalf("migration/read fabricated %s: %d %v", table, count, err)
			}
		}
		if got := catalogV4Rows(t, upgraded.database.db); !reflect.DeepEqual(got, before) {
			t.Fatal("readback changed retained v4 rows")
		}
		if err := upgraded.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCatalogSchemaRecoveryRejectsInvalidV4(t *testing.T) {
	for _, tc := range []struct {
		name, mutation string
		want           error
	}{
		{"dirty", "UPDATE tc_schema_migrations SET dirty=1 WHERE version=4", ErrDirtyMigration},
		{"checksum", "UPDATE tc_schema_migrations SET checksum='wrong' WHERE version=3", ErrChecksumDrift},
		{"corrupt", "", ErrDatabaseInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "original-v4.sqlite")
			owner := catalogRecoveryV4(t, path)
			if tc.mutation != "" {
				catalogSchemaExec(t, owner.database.db, tc.mutation)
			}
			// Lifecycle owners preserve WAL sidecars. Materialize the v4 fixture
			// before corrupting its header, so a valid WAL cannot repair it.
			catalogSchemaExec(t, owner.database.db, "PRAGMA wal_checkpoint(TRUNCATE)")
			if err := owner.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "corrupt" {
				before = bytes.Clone(before)
				copy(before[:16], "not a sqlite db!")
				if err := os.WriteFile(path, before, 0600); err != nil {
					t.Fatal(err)
				}
			}
			backup := filepath.Join(dir, "must-not-publish.sqlite")
			for attempt := 0; attempt < 2; attempt++ {
				config := Config{Path: path, UpgradeBackupPath: backup}
				want := tc.want
				if attempt == 0 {
					opened, openErr := Open(t.Context(), config, SchemaMigrations())
					err = openErr
					if opened != nil {
						_ = opened.Close()
						t.Fatal("invalid v4 fixture returned a usable database")
					}
				} else {
					opened, openErr := OpenLifecycleOwner(t.Context(), config, SchemaMigrations())
					err = openErr
					want = ErrLifecycleOpen // Public owner deliberately sanitizes errors.
					if opened != nil {
						_ = opened.Close(t.Context())
						t.Fatal("invalid v4 fixture returned a usable owner")
					}
				}
				if !errors.Is(err, want) {
					t.Fatalf("open %d error: %v, want %v", attempt, err, want)
				}
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(after, before) {
					t.Fatalf("rejected upgrade changed original bytes: %v", err)
				}
				if _, err := os.Stat(backup); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("invalid fixture published an upgrade backup: %v", err)
				}
			}
		})
	}
}

const catalogRecoveryObjects = "SELECT type,name,tbl_name,sql FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%' ORDER BY name"

func catalogRecoveryV4(t *testing.T, path string) *LifecycleOwner {
	t.Helper()
	owner, err := OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations()[:4])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	catalogRecoverySeedLegacy(t, owner, 1)
	return owner
}

func catalogRecoverySeedLegacy(t *testing.T, owner *LifecycleOwner, first int) {
	t.Helper()
	if err := owner.WithinTransaction(t.Context(), func(r app.ContentRepository) error {
		return r.Save(t.Context(), testContent(t, "Retained Ω title", true))
	}); err != nil {
		t.Fatal(err)
	}
	for n := first; n < first+2; n++ {
		if _, err := blobRecord(t, owner, blobKey(t, n), blobCommand(t, n, n == first)); err != nil {
			t.Fatal(err)
		}
	}
	db := owner.database.db
	catalogSchemaExec(t, db, "INSERT INTO tc_catalog_content VALUES (?, ?, '', NULL)", catalogSchemaID("cnt", 2), "Baseline\x00bytes")
	catalogSchemaExec(t, db, "INSERT INTO tc_content_versions VALUES (?,?,4294967295,?,0,NULL,NULL)", catalogSchemaID("ver", 99), catalogSchemaID("cnt", 2), bytes.Repeat([]byte{255}, 20))
	tag, observation, decision := catalogSchemaID("tag", 1), catalogSchemaID("obs", 1), catalogSchemaID("dec", 1)
	catalogSchemaExec(t, db, "INSERT INTO tc_tags VALUES (?,?,3)", tag, []byte{0, 255, 1, 2, 3, 4, 5, 6})
	catalogSchemaExec(t, db, "INSERT INTO tc_tag_observations VALUES (?,?,2,0,1,'source Ω','opaque-r1','record:1','2000-01-01T00:00:00.000000001Z',0,1)", observation, tag)
	catalogSchemaExec(t, db, "INSERT INTO tc_tag_decisions VALUES (?,?,0,2,3)", decision, tag)
	catalogSchemaExec(t, db, "INSERT INTO tc_tag_decision_support VALUES (?,?,?)", tag, decision, observation)
}

// Capture every application table, including exact command/state/digest bytes,
// NULLs, normalized support and all migration checksums. Sort by all columns so
// repeated subject IDs cannot hide an ordering-dependent comparison.
func catalogRecoveryRows(t *testing.T, db *sql.DB) map[string][][]any {
	t.Helper()
	result := map[string][][]any{"sqlite_schema": catalogSchemaRows(t, db, catalogRecoveryObjects)}
	for _, row := range catalogSchemaRows(t, db, "SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name") {
		table := row[0].(string)
		columns := catalogSchemaRows(t, db, "PRAGMA table_info("+table+")")
		order := make([]string, len(columns))
		for i := range order {
			order[i] = fmt.Sprint(i + 1)
		}
		result[table] = catalogSchemaRows(t, db, "SELECT * FROM "+table+" ORDER BY "+strings.Join(order, ","))
	}
	return result
}

type catalogRecoveryFixture struct {
	catalogStateFixture
	reviewOp     app.ReviewOperation
	reviewState  catalog.CatalogReviewState
	reviewResult app.ReviewResult
	orderOp      app.OrderOperation
	orderState   catalog.CatalogOrderState
	orderResult  app.OrderResult
}

func catalogRecoveryCurrent(t *testing.T) catalogRecoveryFixture {
	t.Helper()
	f, op, next, result := reviewWriteFixture(t, false)
	catalogRecoverySeedLegacy(t, f.owner, 81)
	if err := commitReviewFixture(t, f, op, next, result); err != nil {
		t.Fatal(err)
	}
	accept := op
	accept.Command.ID, _ = catalog.NewCatalogCommandID()
	accept.ExpectedAggregate, accept.Command.Expected = result.AggregateRevision, next.Revision()
	accept.Command.Kind, accept.Command.Key = catalog.AcceptSupport, catalog.TitleClaimKey
	accept.Command.Observations = nil
	accept.Command.Support = []evidence.ID{op.Command.Observations[0].ID()}
	accepted, decision, err := next.Apply(f.review, accept.Command)
	if err != nil {
		t.Fatal(err)
	}
	revision, _ := catalog.NewCatalogRevision(3)
	if err := commitReviewFixture(t, f, accept, accepted, app.ReviewResult{AggregateRevision: revision, Decision: decision}); err != nil {
		t.Fatal(err)
	}
	versions := []catalog.ContentVersion{f.versions[0].Version, f.versions[1].Version}
	order, err := catalog.NewCatalogOrderState(f.subject.ContentID(), versions)
	if err != nil {
		t.Fatal(err)
	}
	f.storeOrder(t, order, 3)
	id, _ := evidence.ParseID(catalogSchemaID("obs", 9))
	item, err := catalog.NewCatalogSequenceEvidence(f.ingest, f.namespace, catalog.CatalogSequenceInput{ID: id, Content: f.subject.ContentID(), Version: versions[0].ID(), Position: 7, Coordinate: f.coordinate(t)})
	if err != nil {
		t.Fatal(err)
	}
	commandID, _ := catalog.NewCatalogCommandID()
	orderOp := app.OrderOperation{ExpectedAggregate: revision, Capability: f.order, Command: catalog.CatalogOrderCommand{ID: commandID, Content: f.subject.ContentID(), Namespace: f.namespace, Expected: order.Revision(), Reason: "snapshot order", Evidence: []catalog.CatalogSequenceEvidence{item}, Entries: []catalog.CatalogOrderEntry{{Version: versions[0].ID(), Position: 7, Support: []evidence.ID{id}}}}}
	ordered, orderDecision, err := order.Apply(f.order, orderOp.Command)
	if err != nil {
		t.Fatal(err)
	}
	revision, _ = catalog.NewCatalogRevision(4)
	orderResult := app.OrderResult{AggregateRevision: revision, Decision: orderDecision}
	if err := commitOrderFixture(t, f, orderOp, ordered, orderResult); err != nil {
		t.Fatal(err)
	}
	later := orderOp
	later.Command.ID, _ = catalog.NewCatalogCommandID()
	later.ExpectedAggregate, later.Command.Expected = revision, ordered.Revision()
	later.Command.Evidence = nil
	later.Command.Reason = "later order before snapshot"
	latest, latestDecision, err := ordered.Apply(f.order, later.Command)
	if err != nil {
		t.Fatal(err)
	}
	revision, _ = catalog.NewCatalogRevision(5)
	if err := commitOrderFixture(t, f, later, latest, app.OrderResult{AggregateRevision: revision, Decision: latestDecision}); err != nil {
		t.Fatal(err)
	}
	return catalogRecoveryFixture{f, op, next, result, orderOp, ordered, orderResult}
}

func catalogRecoveryReplays(t *testing.T, f catalogRecoveryFixture) {
	t.Helper()
	before := catalogRecoveryRows(t, f.owner.database.db)
	err := f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
		for _, load := range []func() error{
			func() error {
				got, found, err := r.LookupReview(t.Context(), f.reviewOp)
				if err != nil || !found || !reflect.DeepEqual(got, f.reviewResult) {
					t.Fatalf("old review replay: %t %v", found, err)
				}
				return r.CommitReview(t.Context(), f.reviewOp, f.reviewState, f.reviewResult)
			},
			func() error {
				got, found, err := r.LookupOrder(t.Context(), f.orderOp)
				if err != nil || !found || !reflect.DeepEqual(got, f.orderResult) {
					t.Fatalf("old order replay: %t %v", found, err)
				}
				return r.CommitOrder(t.Context(), f.orderOp, f.orderState, f.orderResult)
			},
		} {
			if err := load(); err != nil {
				return err
			}
		}
		if state, found, err := r.LoadReview(t.Context(), f.subject); err != nil || !found || state.AggregateRevision.Uint64() != 5 || state.State.Revision().Uint64() != 3 {
			t.Fatalf("review state: %+v %t %v", state, found, err)
		}
		if state, found, err := r.LoadOrder(t.Context(), f.subject.ContentID()); err != nil || !found || state.AggregateRevision.Uint64() != 5 || state.State.Revision().Uint64() != 3 {
			t.Fatalf("order state: %+v %t %v", state, found, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for n := 81; n <= 82; n++ {
		command := blobCommand(t, n, n == 81)
		if got, found, err := blobLookup(t, f.owner, blobKey(t, n), command); err != nil || !found || got != importResult(command) {
			t.Fatalf("receipt %d: %t %v", n, found, err)
		}
	}
	if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
		t.Fatal("replay rewrote restored state")
	}
}

func TestCatalogSnapshotRestoreCurrentV6(t *testing.T) {
	f := catalogRecoveryCurrent(t)
	owner := f.owner
	dir := filepath.Dir(owner.config.Path)
	source := owner.config.Path
	before := catalogRecoveryRows(t, owner.database.db)
	for _, table := range []string{"tc_catalog_review_states", "tc_catalog_order_states", "tc_catalog_decision_support", "tc_tags", "tc_tag_decision_support", "tc_blob_imports", "tc_version_blobs"} {
		if len(before[table]) == 0 {
			t.Fatal("empty recovery fixture", table)
		}
	}
	query := app.Query{Kind: app.ListVersions, Content: testContent(t, "", false).ID(), Page: app.PageRequest{Limit: 1}}
	page, err := catalogRead(t, owner, query)
	if err != nil || page.Next == "" {
		t.Fatal("cursor fixture", err)
	}
	snapshot, err := owner.database.CreateBackup(t.Context(), filepath.Join(dir, "current-snapshot.sqlite"), SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.WithinTransaction(t.Context(), func(r app.ContentRepository) error {
		return r.Save(t.Context(), testContent(t, "committed after snapshot", true))
	}); err != nil {
		t.Fatal(err)
	}
	originalRows := catalogRecoveryRows(t, owner.database.db)
	if reflect.DeepEqual(before, originalRows) {
		t.Fatal("post-snapshot mutation missing")
	}
	page, err = catalogRead(t, owner, query)
	if err != nil || page.Next == "" {
		t.Fatal("current cursor fixture", err)
	}
	continuation := query
	continuation.Page.Cursor, continuation.Page.ExpectedRevision = page.Next, page.Revision
	if _, err := catalogRead(t, owner, continuation); err != nil {
		t.Fatal("cursor invalid before restore", err)
	}
	if err := VerifyBackup(t.Context(), snapshot, SchemaMigrations()); err != nil {
		t.Fatal(err)
	}
	snapshotBytes, err := os.ReadFile(snapshot.Path)
	if err != nil {
		t.Fatal(err)
	}
	// A real in-flight catalog callback must finish before the old handle closes.
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	var escaped app.CatalogRepository
	go func() {
		done <- owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
			escaped = r
			close(entered)
			<-release
			_, _, err := r.LoadReview(t.Context(), f.subject)
			return err
		})
	}()
	<-entered
	restored := make(chan error, 1)
	old, generation := owner.database, owner.generation.Load()
	go func() { restored <- owner.Restore(t.Context(), snapshot, filepath.Join(dir, "selected-v6.sqlite")) }()
	waitForLifecycleWaiter(t, owner)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	err = owner.WithinCatalogTransaction(ctx, func(app.CatalogRepository) error { t.Error("second callback entered during restore"); return nil })
	cancel()
	close(release)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("drain fence", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-restored; err != nil {
		t.Fatal(err)
	}
	if owner.generation.Load() != generation+1 || old.db.PingContext(t.Context()) == nil {
		t.Fatal("old owner handle remains usable")
	}
	if _, _, err := escaped.LoadReview(t.Context(), f.subject); !errors.Is(err, app.ErrRepositoryUnavailable) {
		t.Fatal("escaped repository", err)
	}
	query.Page.Cursor, query.Page.ExpectedRevision = page.Next, page.Revision
	if _, err := catalogRead(t, owner, query); !errors.Is(err, app.ErrStaleCursor) {
		t.Fatal("restored cursor accepted", err)
	}
	if !reflect.DeepEqual(before, catalogRecoveryRows(t, owner.database.db)) {
		t.Fatal("v6 restore changed schema, state bytes or relations")
	}
	if err := verifyForeignKeys(t.Context(), owner.database.db); err != nil {
		t.Fatal(err)
	}
	catalogRecoveryReplays(t, f)
	raw := openRawDatabase(t, source)
	if !reflect.DeepEqual(originalRows, catalogRecoveryRows(t, raw)) {
		t.Fatal("restore changed original source")
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(snapshot.Path); err != nil || !bytes.Equal(got, snapshotBytes) {
		t.Fatal("restore/replay changed snapshot", err)
	}
	query.Page = app.PageRequest{Limit: 2}
	if got, err := catalogRead(t, owner, query); err != nil || len(got.Entries) != 2 || got.Entries[0].Media != app.MediaBoundUnchecked {
		t.Fatalf("restored catalog-only query: %+v %v", got, err)
	}
}

func TestCatalogSnapshotRestoreExactV4(t *testing.T) {
	dir := t.TempDir()
	v4 := catalogRecoveryV4(t, filepath.Join(dir, "pre-pi08-v4.sqlite"))
	before := catalogRecoveryRows(t, v4.database.db)
	snapshot, err := v4.database.CreateBackup(t.Context(), filepath.Join(dir, "v4-snapshot.sqlite"), SchemaMigrations())
	if err != nil || snapshot.SchemaVersion != 4 {
		t.Fatal(snapshot, err)
	}
	if err := v4.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	f := catalogRecoveryCurrent(t)
	media := filepath.Join(dir, "external-generated-media.taf")
	mediaBytes := []byte("generated external media must survive exact-schema rollback")
	if err := os.WriteFile(media, mediaBytes, 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.owner.Restore(t.Context(), snapshot, filepath.Join(dir, "selected-v4.sqlite")); err != nil {
		t.Fatal(err)
	}
	if version, err := f.owner.CurrentVersion(t.Context()); err != nil || version != 4 {
		t.Fatal("implicit migration", version, err)
	}
	if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
		t.Fatal("v4 schema, legacy values or checksums changed")
	}
	called := false
	err = f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
		called = true
		return r.CommitReview(t.Context(), f.reviewOp, f.reviewState, f.reviewResult)
	})
	if !errors.Is(err, app.ErrUnsupported) || called {
		t.Fatal("catalog admitted on v4", called, err)
	}
	err = f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
		called = true
		return r.CommitOrder(t.Context(), f.orderOp, f.orderState, f.orderResult)
	})
	if !errors.Is(err, app.ErrUnsupported) || called {
		t.Fatal("order admitted on v4", called, err)
	}
	for n := 1; n <= 2; n++ {
		command := blobCommand(t, n, n == 1)
		if got, found, err := blobLookup(t, f.owner, blobKey(t, n), command); err != nil || !found || got != importResult(command) {
			t.Fatal("v4 receipt", n, found, err)
		}
	}
	if err := verifyForeignKeys(t.Context(), f.owner.database.db); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, catalogRecoveryRows(t, f.owner.database.db)) {
		t.Fatal("unsupported calls changed v4")
	}
	if got, err := os.ReadFile(media); err != nil || !bytes.Equal(got, mediaBytes) {
		t.Fatal("restore removed external media", err)
	}
}

// The accepted inventory invalidation seam runs after snapshot preflight and
// before closing the old database. Inject filesystem races at that exact point
// rather than add a production-only test hook.
type catalogRestoreInjection struct{ run func() error }

func (i catalogRestoreInjection) InvalidateInventory() error { return i.run() }

func TestCatalogSnapshotRestoreFailuresKeepOriginal(t *testing.T) {
	for _, failure := range []string{"backup verification", "digest", "broken support", "missing binding", "dangling receipt", "invalidation", "replacement", "post-preflight verification"} {
		t.Run(failure, func(t *testing.T) {
			f := catalogRecoveryCurrent(t)
			owner := f.owner
			dir, source := filepath.Dir(owner.config.Path), owner.config.Path
			before := catalogRecoveryRows(t, owner.database.db)
			snapshot, err := owner.database.CreateBackup(t.Context(), filepath.Join(dir, "snapshot.sqlite"), SchemaMigrations())
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(dir, "must-not-select.sqlite")
			want, closed := error(ErrBackupInvalid), false
			switch failure {
			case "backup verification":
				catalogSchemaExec(t, owner.database.db, "PRAGMA foreign_keys=OFF")
				catalogSchemaExec(t, owner.database.db, "DELETE FROM tc_version_blobs WHERE version_id=?", blobCommand(t, 81, true).Version().ID().String())
				catalogSchemaExec(t, owner.database.db, "PRAGMA foreign_keys=ON")
				before = catalogRecoveryRows(t, owner.database.db)
				result, err := owner.database.CreateBackup(t.Context(), target, SchemaMigrations())
				if !errors.Is(err, ErrBackupInvalid) || result != (BackupFile{}) {
					t.Fatal("unverified backup published", result, err)
				}
			case "digest":
				snapshot.SHA256 = strings.Repeat("0", 64)
			case "broken support", "missing binding", "dangling receipt":
				raw := openRawDatabase(t, snapshot.Path)
				catalogSchemaExec(t, raw, "PRAGMA foreign_keys=OFF")
				switch failure {
				case "broken support":
					catalogSchemaExec(t, raw, "DROP TRIGGER tc_catalog_decision_support_no_update")
					catalogSchemaExec(t, raw, "UPDATE tc_catalog_decision_support SET observation_id=?", catalogSchemaID("obs", 99))
				case "missing binding":
					catalogSchemaExec(t, raw, "DELETE FROM tc_version_blobs WHERE version_id=?", blobCommand(t, 81, true).Version().ID().String())
				case "dangling receipt":
					catalogSchemaExec(t, raw, "UPDATE tc_blob_imports SET version_id=?", catalogSchemaID("ver", 98))
				}
				if err := raw.Close(); err != nil {
					t.Fatal(err)
				}
				snapshot.SHA256, err = fileDigest(snapshot.Path)
				if err != nil {
					t.Fatal(err)
				}
			case "invalidation":
				want = contentstore.ErrUnavailable
				owner.contentInventory = catalogRestoreInjection{func() error { return contentstore.ErrUnavailable }}
			case "replacement":
				want, closed = ErrLifecycleRestore, true
				owner.contentInventory = catalogRestoreInjection{func() error { return os.WriteFile(target, []byte("concurrent destination: keep"), 0600) }}
			case "post-preflight verification":
				want, closed = ErrLifecycleRestore, true
				owner.contentInventory = catalogRestoreInjection{func() error { return os.WriteFile(snapshot.Path, []byte("changed after verification"), 0600) }}
			}
			if failure != "backup verification" {
				if err := owner.Restore(t.Context(), snapshot, target); !errors.Is(err, want) {
					t.Fatal("restore error", err, "want", want)
				}
			}
			if closed {
				if _, err := owner.CurrentVersion(t.Context()); !errors.Is(err, ErrLifecycleClosed) {
					t.Fatal("unverified handle selected", err)
				}
				entered := false
				if err := owner.WithinCatalogTransaction(t.Context(), func(app.CatalogRepository) error { entered = true; return nil }); !errors.Is(err, app.ErrRepositoryUnavailable) || entered {
					t.Fatal("failed switch admits catalog", entered, err)
				}
				raw := openRawDatabase(t, source)
				if !reflect.DeepEqual(before, catalogRecoveryRows(t, raw)) {
					t.Fatal("failed replacement changed original")
				}
				if err := raw.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				if !reflect.DeepEqual(before, catalogRecoveryRows(t, owner.database.db)) {
					t.Fatal("preflight failure changed original")
				}
				if version, err := owner.CurrentVersion(t.Context()); err != nil || version != 6 {
					t.Fatal("preflight failure closed owner", version, err)
				}
			}
			if failure == "replacement" {
				if got, err := os.ReadFile(target); err != nil || string(got) != "concurrent destination: keep" {
					t.Fatal("replacement overwrote competing target", err)
				}
			} else if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unconfirmed destination published", err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".tc-sqlite-") {
					t.Fatal("failed restore retained staging", entry.Name())
				}
			}
		})
	}
}
