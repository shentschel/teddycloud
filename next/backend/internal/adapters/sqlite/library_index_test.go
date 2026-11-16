package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
	"math"
	sqliteDriver "modernc.org/sqlite"
	"reflect"
	"strings"
	"testing"
	"time"

	lib "github.com/shentschel/teddycloud/next/backend/internal/application/library"
)

func TestLibraryIndexPreparation(t *testing.T) {
	t.Run("atomic-resumable-no-read-build", func(t *testing.T) {
		o := catalogQueryOwner(t)
		s := lib.NewService(o)
		scope := o.LibraryReadScope()
		for i := 1; i <= 129; i++ {
			libraryUnmanaged(t, o, i, i, 1, "baseline")
		}
		before := catalogRecoveryRows(t, o.database.db)
		if r, err := s.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 1}, scope); err != lib.ErrIndexNotReady || len(r.Items) != 0 || o.library.index != nil {
			t.Fatal(r, err)
		}
		if _, err := s.Prepare(t.Context(), scope); err != nil {
			t.Fatal(err)
		}
		p, err := s.Advance(t.Context(), scope)
		if err != nil || p.Ready || p.Contents != 128 || p.Items != 128 {
			t.Fatal(p, err)
		}
		if _, err = s.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 1}, scope); err != lib.ErrIndexNotReady || o.library.progress != p {
			t.Fatal(err)
		}
		p, err = s.Advance(t.Context(), scope)
		if err != nil || !p.Ready || p.Contents != 129 {
			t.Fatal(p, err)
		}
		first, err := s.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 1}, scope)
		if err != nil {
			t.Fatal(err)
		}
		old := o.library.index
		if _, err = s.Prepare(t.Context(), scope); err != nil {
			t.Fatal(err)
		}
		if old == o.library.index || old.Ping() == nil || o.library.progress.Ready {
			t.Fatal("old storage retained during replacement")
		}
		if _, err = s.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 1, Cursor: first.Next, ExpectedSnapshot: first.Snapshot}, scope); err != lib.ErrStaleCursor {
			t.Fatal(err)
		}
		// Complete a second build without any source write. An identical epoch and
		// summary inventory must still get a fresh generation and reject old tokens.
		var rebuilt lib.Preparation
		for !rebuilt.Ready {
			rebuilt, err = s.Advance(t.Context(), scope)
			if err != nil {
				t.Fatal(err)
			}
		}
		fresh, err := s.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 1}, scope)
		if err != nil || fresh.Snapshot == first.Snapshot {
			t.Fatal("identical rebuild reused generation", err)
		}
		if _, err := s.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 1, Cursor: first.Next, ExpectedSnapshot: first.Snapshot}, scope); err != lib.ErrStaleCursor {
			t.Fatal("old token revived after identical rebuild", err)
		}
		if !reflect.DeepEqual(before, catalogRecoveryRows(t, o.database.db)) {
			t.Fatal("build mutated source")
		}
	})
	t.Run("empty", func(t *testing.T) {
		o := catalogQueryOwner(t)
		s := lib.NewService(o)
		p := libraryReady(t, o, s, o.LibraryReadScope())
		if p.Items != 0 || p.Contents != 0 {
			t.Fatal(p)
		}
	})
	t.Run("epoch-between-chunks", func(t *testing.T) {
		o := catalogQueryOwner(t)
		s := lib.NewService(o)
		scope := o.LibraryReadScope()
		for i := 1; i <= 129; i++ {
			libraryUnmanaged(t, o, i, i, 1, "")
		}
		if _, err := s.Prepare(t.Context(), scope); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Advance(t.Context(), scope); err != nil {
			t.Fatal(err)
		}
		catalogSchemaExec(t, o.database.db, "UPDATE tc_catalog_content SET title=title")
		before := catalogRecoveryRows(t, o.database.db)
		if p, err := s.Advance(t.Context(), scope); err != lib.ErrStaleCursor || p != (lib.Preparation{}) || o.library.index != nil {
			t.Fatal(p, err)
		}
		if !reflect.DeepEqual(before, catalogRecoveryRows(t, o.database.db)) {
			t.Fatal("failure changed source")
		}
	})
	for _, version := range []int{8, 9, 10} {
		t.Run(strings.Repeat("v", version), func(t *testing.T) {
			o, err := OpenLifecycleOwner(t.Context(), Config{Path: t.TempDir() + "/schema.sqlite"}, SchemaMigrations()[:version])
			if err != nil {
				t.Fatal(err)
			}
			defer o.Close(context.Background())
			s := lib.NewService(o)
			scope := o.LibraryReadScope()
			before := catalogRecoveryRows(t, o.database.db)
			_, err = s.Prepare(t.Context(), scope)
			if version == 8 {
				if err != lib.ErrUnsupported || o.library.index != nil {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, catalogRecoveryRows(t, o.database.db)) {
				t.Fatal("implicit migration")
			}
		})
	}
	for _, change := range []string{"UPDATE tc_schema_migrations SET dirty=1 WHERE version=10", "UPDATE tc_schema_migrations SET version=11 WHERE version=10", "UPDATE tc_schema_migrations SET checksum='wrong' WHERE version=10"} {
		t.Run(change, func(t *testing.T) {
			o := catalogQueryOwner(t)
			catalogSchemaExec(t, o.database.db, change)
			before := catalogRecoveryRows(t, o.database.db)
			if _, err := lib.NewService(o).Prepare(t.Context(), o.LibraryReadScope()); err != lib.ErrUnsupported {
				t.Fatal(err)
			}
			if o.library.index != nil || !reflect.DeepEqual(before, catalogRecoveryRows(t, o.database.db)) {
				t.Fatal("unsupported changed source or retained staging")
			}
		})
	}
	t.Run("storage-failure", func(t *testing.T) {
		o := catalogQueryOwner(t)
		s := lib.NewService(o)
		scope := o.LibraryReadScope()
		libraryUnmanaged(t, o, 1, 1, 1, "")
		if _, err := s.Prepare(t.Context(), scope); err != nil {
			t.Fatal(err)
		}
		if _, err := o.library.index.ExecContext(t.Context(), "DROP TABLE items"); err != nil {
			t.Fatal(err)
		}
		before := catalogRecoveryRows(t, o.database.db)
		if _, err := s.Advance(t.Context(), scope); err != lib.ErrUnavailable || o.library.index != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, catalogRecoveryRows(t, o.database.db)) {
			t.Fatal("failed build changed source")
		}
	})
}
func TestLibraryIndexPreparationAdmissionCaps(t *testing.T) {
	// These are real source cardinalities at the preflight admission gate, not a
	// 100k completed-index/performance claim (that belongs to T).
	for _, kind := range []string{"contents", "items"} {
		t.Run(kind, func(t *testing.T) {
			o := catalogQueryOwner(t)
			s := lib.NewService(o)
			scope := o.LibraryReadScope()
			if kind == "contents" {
				catalogSchemaExec(t, o.database.db, `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<100000) INSERT INTO tc_catalog_content SELECT 'cnt_'||printf('%026d',x),'',NULL,NULL FROM n`)
			} else {
				catalogSchemaExec(t, o.database.db, `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<100) INSERT INTO tc_catalog_content SELECT 'cnt_'||printf('%026d',x),'',NULL,NULL FROM n`)
				catalogSchemaExec(t, o.database.db, `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<100000) INSERT INTO tc_content_versions SELECT 'ver_'||printf('%026d',x),'cnt_'||printf('%026d',1+(x-1)/1000),123,zeroblob(20),0,NULL,NULL FROM n`)
			}
			if _, err := s.Prepare(t.Context(), scope); err != nil {
				t.Fatal("reachable exact 100000", err)
			}
			if kind == "contents" {
				libraryUnmanaged(t, o, 100001, 100001, 0, "")
			} else {
				catalogSchemaExec(t, o.database.db, "INSERT INTO tc_content_versions VALUES (?,?,123,zeroblob(20),0,NULL,NULL)", catalogSchemaID("ver", 100001), "cnt_00000000000000000000000001")
			}
			// Count/schema/receipt snapshot at the rejected admission, without retaining
			// two 100k-row application copies in addition to the index budget.
			var before int64
			if err := o.database.db.QueryRow("SELECT total_changes()").Scan(&before); err != nil {
				t.Fatal(err)
			}
			if p, err := s.Prepare(t.Context(), scope); err != lib.ErrLimitExceeded || p != (lib.Preparation{}) || o.library.index != nil {
				t.Fatal(p, err)
			}
			var after int64
			if err := o.database.db.QueryRow("SELECT total_changes()").Scan(&after); err != nil || after != before {
				t.Fatal(before, after, err)
			}
		})
	}
	for _, tc := range []struct {
		items, bytes, added uint64
		want                error
	}{{99999, 0, 1, nil}, {100000, 0, 1, lib.ErrLimitExceeded}, {0, lib.MaxStagingBytes - 1, 1, nil}, {0, lib.MaxStagingBytes - 1, 2, lib.ErrLimitExceeded}, {0, math.MaxUint64, 1, lib.ErrLimitExceeded}, {0, 1, math.MaxUint64, lib.ErrLimitExceeded}} {
		if err := libraryCapacity(tc.items, tc.bytes, tc.added); !errors.Is(err, tc.want) {
			t.Fatal(tc, err)
		}
	}
	// 100k rich legal summaries can exceed 128 MiB while remaining below the item
	// cap. Check reachability from actual canonical bytes rather than raw text.
	item := lib.Item{ContentID: catalogSchemaID("cnt", 1), VersionID: catalogSchemaID("ver", 1), Baseline: lib.Baseline{Title: strings.Repeat("a", 4096)}}
	raw, _ := lib.CanonicalJSON(item)
	unit := uint64(len(raw) + 60)
	if unit*lib.MaxVersions <= lib.MaxStagingBytes {
		t.Fatal("staging cap unreachable with rich summaries")
	}
	t.Logf("staging reachability: %d bytes/summary+identity; first capacity crossing at %d items", unit, lib.MaxStagingBytes/unit+1)
}
func TestLibraryInventoryIdentityCorruption(t *testing.T) {
	for _, corruption := range []string{
		"PRAGMA foreign_keys=OFF; DELETE FROM tc_catalog_registration_support WHERE claim_key=5",
		"PRAGMA foreign_keys=OFF; DELETE FROM tc_catalog_observations WHERE observation_id=(SELECT observation_id FROM tc_catalog_registration_support LIMIT 1)",
		"PRAGMA ignore_check_constraints=ON; UPDATE tc_content_versions SET audio_id=0 WHERE version_id=(SELECT max(version_id) FROM tc_content_versions)",
	} {
		t.Run(corruption, func(t *testing.T) {
			f := newSupersessionFixture(t)
			o := f.owner
			s := lib.NewService(o)
			scope := o.LibraryReadScope()
			libraryUnmanaged(t, o, 1, 1, 1, "would be the first page")
			guards := catalogSchemaRows(t, o.database.db, "SELECT name FROM sqlite_schema WHERE type='trigger' AND tbl_name IN ('tc_catalog_registration_support','tc_catalog_observations')")
			for _, guard := range guards {
				catalogSchemaExec(t, o.database.db, "DROP TRIGGER "+guard[0].(string))
			}
			if _, err := o.database.db.Exec(corruption); err != nil {
				t.Fatal(err)
			}
			before := catalogRecoveryRows(t, o.database.db)
			_, err := s.Prepare(t.Context(), scope)
			if err == nil {
				_, err = s.Advance(t.Context(), scope)
			}
			if err != lib.ErrUnavailable || o.library.index != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, catalogRecoveryRows(t, o.database.db)) {
				t.Fatal("corruption was rewritten")
			}
		})
	}
}

func TestLibraryIndexPreparationPageCap(t *testing.T) {
	// Medium-size WITHOUT ROWID cells overflow into separate 4096-byte pages.
	// 65536 valid source versions leave both the 100k-item and 128MiB staging
	// caps out of the way. Two individually owned short summaries make the last
	// admitted prefix land on exactly 65536 pages, not merely somewhere below it.
	o := catalogQueryOwner(t)
	s := lib.NewService(o)
	scope := o.LibraryReadScope()
	catalogSchemaExec(t, o.database.db, `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<66) INSERT INTO tc_catalog_content SELECT 'cnt_'||printf('%026d',x),CASE WHEN x IN (56,57) THEN '' ELSE ? END,NULL,NULL FROM n`, strings.Repeat("a", 700))
	catalogSchemaExec(t, o.database.db, `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<65536) INSERT INTO tc_content_versions SELECT 'ver_'||printf('%026d',x),'cnt_'||printf('%026d',CASE WHEN x<=56320 THEN 1+(x-1)/1024 WHEN x=56321 THEN 56 WHEN x=56322 THEN 57 WHEN x<=57344 THEN 58 ELSE 3+(x-1)/1024 END),123,zeroblob(20),0,NULL,NULL FROM n`)
	before := catalogRecoveryRows(t, o.database.db)
	if _, err := s.Prepare(t.Context(), scope); err != nil {
		t.Fatal(err)
	}

	// Fixture construction is separate from the capacity operation under test.
	// Advance normally batches >=4096 versions; under -race those bulk reads can
	// exhaust the normative transaction deadline before reaching the page cap.
	// Seed the same valid, unpublished prefix one complete Content at a time.
	// Every seed retains the real owner lease, 10s/5s deadlines, complete PI-08
	// reconstruction and source epoch checks. No foreign key/provenance check is
	// suppressed, no row is trimmed, and no ready generation is manufactured.
	setupStart := time.Now()
	var maxSeed time.Duration
	for content := 1; content <= 58; content++ {
		start := time.Now()
		err := o.WithinLibraryRead(t.Context(), scope, func(ctx context.Context, reader lib.Reader) error {
			r := reader.(*libraryRead)
			return r.use(ctx, func(call context.Context) error {
				return r.source(call, true, func(call context.Context, tx *sql.Tx, version int, _ libraryEpoch) error {
					repo := &catalogRepository{transaction: tx, ctx: call, owner: o, database: o.database, generation: o.generation.Load(), schemaVersion: version, statements: make(map[string]*sql.Stmt)}
					defer repo.closeStatements()
					defer repo.revoke()
					id, err := identity.ParseContentID(fmt.Sprintf("cnt_%026d", content))
					if err != nil {
						return err
					}
					entries, revision, err := repo.libraryAggregate(call, id)
					if err != nil {
						return err
					}
					if len(entries) == 0 || len(entries) > 1024 {
						return lib.ErrUnavailable
					}
					index := o.library.index
					write, err := index.BeginTx(call, nil)
					if err != nil {
						return err
					}
					defer write.Rollback()
					for _, entry := range entries {
						item := libraryProject(entry, revision.Uint64())
						if err := lib.ValidateItem(item); err != nil {
							return err
						}
						raw, err := lib.CanonicalJSON(item)
						if err != nil {
							return err
						}
						added := uint64(len(raw) + len(item.ContentID) + len(item.VersionID))
						if err := libraryCapacity(o.library.progress.Items, o.library.progress.Bytes, added); err != nil {
							return err
						}
						if _, err := write.ExecContext(call, "INSERT INTO items VALUES (?,?,?)", item.ContentID, item.VersionID, raw); err != nil {
							return err
						}
						o.library.progress.Items++
						o.library.progress.Bytes += added
					}
					var pages uint64
					if err := write.QueryRowContext(call, "PRAGMA page_count").Scan(&pages); err != nil {
						return err
					}
					if err := write.Commit(); err != nil {
						return err
					}
					o.library.progress.Pages = pages
					o.library.progress.Contents++
					o.library.after = id.String()
					return nil
				})
			})
		})
		elapsed := time.Since(start)
		if elapsed > maxSeed {
			maxSeed = elapsed
		}
		if err != nil {
			t.Fatalf("seed Content %d after %s: %v", content, elapsed, err)
		}
	}
	t.Logf("validated fixture prefix: %s total, maximum owner operation %s", time.Since(setupStart), maxSeed)

	var pages, pageSize, maxPages, freePages uint64
	if err := o.library.index.QueryRowContext(t.Context(), "SELECT page_count,page_size,max_page_count,freelist_count FROM pragma_page_count,pragma_page_size,pragma_max_page_count,pragma_freelist_count").Scan(&pages, &pageSize, &maxPages, &freePages); err != nil {
		t.Fatal(err)
	}
	previous := o.library.progress
	if pages != 65536 || maxPages != pages || pageSize != 4096 || pages*pageSize != lib.MaxIndexBytes || freePages != 0 {
		t.Fatal("not exactly the physical page cap", pages, pageSize, maxPages, freePages)
	}
	if previous.Ready || !o.library.building || previous.Contents != 58 || previous.Items != 57344 || previous.Bytes >= lib.MaxStagingBytes || previous.Items >= lib.MaxVersions {
		t.Fatal("fixture prefix/cap reachability", previous)
	}
	// Source Content 59 still has 1024 overflow-sized versions. The next real
	// Advance must request a new page (#65537); there is no free page to reuse.
	// The only exhausted bound is the real private database's 256MiB page cap.
	start := time.Now()
	result, err := s.Advance(t.Context(), scope)
	elapsed := time.Since(start)
	if err != lib.ErrLimitExceeded || result != (lib.Preparation{}) {
		t.Fatal("physical +1 page was not rejected", result, err)
	}
	if o.library.index != nil || o.library.building || o.library.progress != (lib.Preparation{}) || o.library.snapshot != ([32]byte{}) {
		t.Fatal("failed staging was retained or published")
	}
	if page, err := s.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 1}, scope); err != lib.ErrIndexNotReady || len(page.Items) != 0 || page.Next != "" {
		t.Fatal("capacity failure exposed a partial generation", page, err)
	}
	if !reflect.DeepEqual(before, catalogRecoveryRows(t, o.database.db)) {
		t.Fatal("capacity evidence changed source/schema/receipts")
	}
	t.Logf("exact capacity: %d pages x %d bytes; items=%d staging=%d; real Advance +1 rejected and cleaned up in %s", pages, pageSize, previous.Items, previous.Bytes, elapsed)
}

func TestLibraryIndexPreparationStagingBytes(t *testing.T) {
	o := catalogQueryOwner(t)
	s := lib.NewService(o)
	scope := o.LibraryReadScope()
	// Plan exact source cardinalities from the canonical, valid minimal D1
	// summary. Two singly-owned final versions let us tune UTF-8 byte lengths
	// without changing identities, violating per-Content bounds or adding keys.
	item := lib.Item{ContentID: catalogSchemaID("cnt", 1), VersionID: catalogSchemaID("ver", 1), AudioID: 123, Hash: strings.Repeat("0", 40), ContentRevision: 1, Media: 1}
	raw, _ := lib.CanonicalJSON(item)
	minimum := uint64(len(raw) + 60)
	rich := minimum + 4096
	n := lib.MaxStagingBytes / rich
	remaining := int(lib.MaxStagingBytes - (n-1)*rich - 2*minimum)
	firstTitle := remaining
	if firstTitle > 4096 {
		firstTitle = 4096
	}
	lastTitle := remaining - firstTitle
	if firstTitle < 0 || lastTitle < 0 || lastTitle > 4096 {
		t.Fatal("unreachable staging arithmetic", minimum, n, remaining)
	}
	// Rich rows share bounded Contents of at most 1024 versions.
	owners := (n - 1 + 1023) / 1024
	catalogSchemaExec(t, o.database.db, `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO tc_catalog_content SELECT 'cnt_'||printf('%026d',x),?,NULL,NULL FROM n`, owners, strings.Repeat("a", 4096))
	catalogSchemaExec(t, o.database.db, `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO tc_content_versions SELECT 'ver_'||printf('%026d',x),'cnt_'||printf('%026d',1+(x-1)/1024),123,zeroblob(20),0,NULL,NULL FROM n`, n-1)
	for i, size := range []int{firstTitle, lastTitle} {
		id := 10000 + i
		libraryUnmanaged(t, o, id, 200000+i, 1, strings.Repeat("a", size))
		// helper uses byte 1 fingerprints, whereas planned minimum uses zero hash;
		// hex strings have the same canonical length, so accounting is identical.
	}
	// No application copy of the rich source is retained beside the private DB.
	if _, err := s.Prepare(t.Context(), scope); err != nil {
		t.Fatal(err)
	}
	var p lib.Preparation
	for i := 0; i < 100; i++ {
		var err error
		p, err = s.Advance(t.Context(), scope)
		if err != nil {
			t.Fatal(p, err)
		}
		if p.Ready {
			break
		}
	}
	if !p.Ready || p.Bytes != lib.MaxStagingBytes || p.Items != n+1 {
		t.Fatal("exact staging boundary", p, n, minimum)
	}
	catalogSchemaExec(t, o.database.db, "UPDATE tc_catalog_content SET title=title||'a' WHERE content_id=?", catalogSchemaID("cnt", 10001))
	if _, err := s.Prepare(t.Context(), scope); err != nil {
		t.Fatal(err)
	}
	var failed bool
	for i := 0; i < 100; i++ {
		p, err := s.Advance(t.Context(), scope)
		if err == lib.ErrLimitExceeded {
			failed = true
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if p.Ready {
			t.Fatal("+1 staging accepted", p)
		}
	}
	if !failed || o.library.index != nil {
		t.Fatal("staging +1 did not fail closed")
	}
	t.Logf("reachable 128MiB exact/+1 canonical staging at %d versions and %d Contents", n+1, owners+2)
}

func TestLibraryIndexPreparationCanceled(t *testing.T) {
	for _, phase := range []string{"prepare", "advance", "epoch-during-advance"} {
		t.Run(phase, func(t *testing.T) {
			o := catalogQueryOwner(t)
			libraryUnmanaged(t, o, 1, 1, 2, "")
			trace := libraryTraceSource(t, o)
			s := lib.NewService(o)
			scope := o.LibraryReadScope()
			if phase != "prepare" {
				if _, err := s.Prepare(t.Context(), scope); err != nil {
					t.Fatal(err)
				}
			}
			before := catalogRecoveryRows(t, o.database.db)
			c, cancel := context.WithCancel(t.Context())
			defer cancel()
			fired := false
			trace.hook = func(q string) {
				if fired {
					return
				}
				if phase == "prepare" && strings.Contains(q, "pragma_foreign_key_check") || phase != "prepare" && q == "BEGIN" {
					fired = true
					if phase == "epoch-during-advance" {
						dsn, _, _ := connectionDSN(o.config)
						connector, _ := sqliteDriver.NewConnector(dsn)
						other := sql.OpenDB(connector)
						defer other.Close()
						if _, err := other.Exec("UPDATE tc_catalog_content SET title='x'; UPDATE tc_catalog_content SET title=''"); err != nil {
							t.Error(err)
						}
					} else {
						cancel()
					}
				}
			}
			var err error
			if phase == "prepare" {
				_, err = s.Prepare(c, scope)
			} else {
				_, err = s.Advance(c, scope)
			}
			trace.hook = nil
			want := error(context.Canceled)
			if phase == "epoch-during-advance" {
				want = lib.ErrStaleCursor
			}
			if !fired || !errors.Is(err, want) || o.library.index != nil {
				t.Fatal(fired, err, want)
			}
			if !reflect.DeepEqual(before, catalogRecoveryRows(t, o.database.db)) {
				t.Fatal("cancellation/epoch failure mutated source")
			}
		})
	}
}

func TestLibraryInventoryIdentityBrokenGraphs(t *testing.T) {
	for _, shape := range []string{"cycle", "branch", "merge"} {
		t.Run(shape, func(t *testing.T) {
			f, versions, commands, results := supersessionQueryFixture(t)
			db := f.owner.database.db
			libraryUnmanaged(t, f.owner, 1, 1, 1, "first page would be unrelated")
			c, result := commands[2], results[2]
			switch shape {
			case "cycle":
				c.Old, c.New = versions[2], versions[0]
			case "branch":
				c.Old = versions[0]
			case "merge":
				c.New = versions[2]
			}
			c.Evidence = append([]domain.CatalogSupersessionEvidence(nil), c.Evidence...)
			for i, e := range c.Evidence {
				input := e.Input()
				input.OldVersion, input.NewVersion = c.Old.ID(), c.New.ID()
				var err error
				c.Evidence[i], err = domain.NewCatalogSupersessionEvidence(f.ingest, f.source, input)
				if err != nil {
					t.Fatal(err)
				}
			}
			result.Old, result.New = c.Old, c.New
			commandRaw, err := f.op(c).Command.CanonicalEncoding(f.cap)
			if err != nil {
				t.Fatal(err)
			}
			resultRaw, err := result.CanonicalEncoding()
			if err != nil {
				t.Fatal(err)
			}
			commandHash, resultHash := sha256.Sum256(commandRaw), sha256.Sum256(resultRaw)
			// Disable FKs only across the parent/evidence pair rewrite; every
			// relation is valid again before any repository read is attempted.
			catalogSchemaExec(t, db, "PRAGMA foreign_keys=OFF")
			if shape == "branch" {
				supersessionRemoveEndpointGuard(t, f, "old_version_id")
			} else if shape == "merge" {
				supersessionRemoveEndpointGuard(t, f, "new_version_id")
			}
			guards := catalogSchemaRows(t, db, "SELECT name,sql FROM sqlite_schema WHERE name IN ('tc_catalog_supersessions_no_update','tc_catalog_supersession_evidence_no_update') ORDER BY name")
			if len(guards) != 2 {
				t.Fatal("missing immutable guards")
			}
			for _, guard := range guards {
				catalogSchemaExec(t, db, "DROP TRIGGER "+guard[0].(string))
			}
			catalogSchemaExec(t, db, "UPDATE tc_catalog_supersessions SET old_version_id=?,new_version_id=?,canonical_command=?,canonical_result=?,command_sha256=?,result_sha256=? WHERE command_id=?", c.Old.ID().String(), c.New.ID().String(), commandRaw, resultRaw, commandHash[:], resultHash[:], c.ID.String())
			catalogSchemaExec(t, db, "UPDATE tc_catalog_supersession_evidence SET old_version_id=?,new_version_id=? WHERE command_id=?", c.Old.ID().String(), c.New.ID().String(), c.ID.String())
			for _, guard := range guards {
				catalogSchemaExec(t, db, guard[1].(string))
			}
			catalogSchemaExec(t, db, "PRAGMA foreign_keys=ON")
			if err := verifyForeignKeys(t.Context(), db); err != nil {
				t.Fatal("fixture has unrelated FK corruption", err)
			}
			before := catalogRecoveryRows(t, db)
			s := lib.NewService(f.owner)
			scope := f.owner.LibraryReadScope()
			_, err = s.Prepare(t.Context(), scope)
			if err == nil {
				_, err = s.Advance(t.Context(), scope)
			}
			if err != lib.ErrUnavailable || f.owner.library.index != nil {
				t.Fatal("broken graph published an index", shape, err)
			}
			if !reflect.DeepEqual(before, catalogRecoveryRows(t, db)) {
				t.Fatal("prepare rewrote broken graph/source")
			}
		})
	}
}
