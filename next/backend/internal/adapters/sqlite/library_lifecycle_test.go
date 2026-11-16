package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	parser "github.com/shentschel/teddycloud/next/backend/internal/adapters/catalogimport"
	catalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	lib "github.com/shentschel/teddycloud/next/backend/internal/application/library"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	sqliteDriver "modernc.org/sqlite"
)

type libraryTrace struct {
	mu            sync.Mutex
	sql           []string
	visits        map[string]int
	hook          func(string)
	rollbackError bool
}

func (t *libraryTrace) record(q string) {
	t.mu.Lock()
	t.sql = append(t.sql, q)
	hook := t.hook
	t.mu.Unlock()
	if hook != nil {
		hook(q)
	}
}
func (t *libraryTrace) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sql = nil
	t.visits = map[string]int{}
}

type libraryTraceConnector struct {
	driver.Connector
	trace *libraryTrace
}

func (c *libraryTraceConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &libraryTraceConn{Conn: conn, trace: c.trace}, nil
}

type libraryTraceConn struct {
	driver.Conn
	trace *libraryTrace
}

func (c *libraryTraceConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	c.trace.record(q)
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}
func (c *libraryTraceConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	c.trace.record(q)
	rows, err := c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
	if err != nil {
		return nil, err
	}
	return &libraryTraceRows{Rows: rows, trace: c.trace, query: q}, nil
}
func (c *libraryTraceConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	c.trace.record("BEGIN")
	tx, err := c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &libraryTraceTx{Tx: tx, trace: c.trace}, nil
}
func (c *libraryTraceConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	c.trace.record("PREPARE " + q)
	return c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, q)
}
func (c *libraryTraceConn) ResetSession(ctx context.Context) error {
	return c.Conn.(driver.SessionResetter).ResetSession(ctx)
}
func (c *libraryTraceConn) IsValid() bool                  { return c.Conn.(driver.Validator).IsValid() }
func (c *libraryTraceConn) Ping(ctx context.Context) error { return c.Conn.(driver.Pinger).Ping(ctx) }

type libraryTraceTx struct {
	driver.Tx
	trace *libraryTrace
}

func (t *libraryTraceTx) Commit() error { t.trace.record("COMMIT"); return t.Tx.Commit() }
func (t *libraryTraceTx) Rollback() error {
	t.trace.record("ROLLBACK")
	err := t.Tx.Rollback()
	if t.trace.rollbackError {
		return errors.New("private cleanup diagnostic")
	}
	return err
}

type libraryTraceRows struct {
	driver.Rows
	trace *libraryTrace
	query string
}

func (r *libraryTraceRows) Next(dest []driver.Value) error {
	err := r.Rows.Next(dest)
	if err == nil {
		r.trace.mu.Lock()
		r.trace.visits[r.query]++
		r.trace.mu.Unlock()
	}
	return err
}

func libraryTraceSource(t *testing.T, o *LifecycleOwner) *libraryTrace {
	t.Helper()
	dsn, _, err := connectionDSN(o.config)
	if err != nil {
		t.Fatal(err)
	}
	base, err := sqliteDriver.NewConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := o.database.db.Close(); err != nil {
		t.Fatal(err)
	}
	trace := &libraryTrace{}
	trace.reset()
	o.database.db = sql.OpenDB(&incarnationConnector{Connector: &libraryTraceConnector{Connector: base, trace: trace}, generation: &o.database.physicalGeneration})
	o.database.db.SetMaxOpenConns(1)
	o.database.db.SetMaxIdleConns(1)
	return trace
}
func libraryTraceIndex(t *testing.T, o *LifecycleOwner) *libraryTrace {
	t.Helper()
	old := o.library.index
	rows, err := old.Query("SELECT content,version,payload FROM items ORDER BY content,version")
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		content, version string
		payload          []byte
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.content, &r.version, &r.payload); err != nil {
			t.Fatal(err)
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	base, err := sqliteDriver.NewConnector(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	trace := &libraryTrace{}
	trace.reset()
	index := sql.OpenDB(&libraryTraceConnector{Connector: base, trace: trace})
	index.SetMaxOpenConns(1)
	index.SetMaxIdleConns(1)
	if _, err := index.Exec(libraryIndexSchema); err != nil {
		t.Fatal(err)
	}
	tx, err := index.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range all {
		item, err := libraryDecodeItem(r.payload, r.content, r.version)
		if err != nil {
			t.Fatal("trace payload", err)
		}
		keys, err := libraryItemKeys(item)
		if err != nil {
			t.Fatal(err)
		}
		if err := libraryInsertItem(t.Context(), tx, item, keys); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	o.library.index = index
	trace.reset()
	return trace
}
func TestLibraryReadOnlyBounds(t *testing.T) {
	o := catalogQueryOwner(t)
	libraryUnmanaged(t, o, 1, 1, 130, "baseline")
	trace := libraryTraceSource(t, o)
	s := lib.NewService(o)
	scope := o.LibraryReadScope()
	libraryReady(t, o, s, scope)
	index := libraryTraceIndex(t, o)
	before := catalogRecoveryRows(t, o.database.db)
	trace.reset()
	for _, tc := range []struct {
		scope lib.ReadScope
		p     lib.Page
		want  error
	}{{lib.ReadScope{}, lib.Page{}, lib.ErrUnauthorized}, {scope, lib.Page{}, lib.ErrInvalidInput}, {scope, lib.Page{Limit: 129}, lib.ErrLimitExceeded}} {
		if _, err := s.ListLibrary(t.Context(), lib.Query{}, tc.p, tc.scope); err != tc.want {
			t.Fatal(err)
		}
		if len(trace.sql) != 0 || len(index.sql) != 0 {
			t.Fatal("preflight performed SQL", trace.sql, index.sql)
		}
	}
	var firstCount, continuationCount int
	for _, limit := range []uint64{1, 16, 128} {
		trace.reset()
		index.reset()
		first, err := s.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: limit}, scope)
		if err != nil {
			t.Fatal(err)
		}
		count := len(trace.sql) + len(index.sql)
		if count > 12 || firstCount != 0 && count != firstCount {
			t.Fatal("nonconstant ready statements", limit, count, firstCount, trace.sql, index.sql)
		}
		firstCount = count
		visits := 0
		for q, n := range index.visits {
			if strings.Contains(q, "FROM memberships") {
				visits += n
			}
		}
		if visits != int(limit+1) || first.Inspected != uint64(visits) {
			t.Fatal("candidate visits", limit, visits)
		}
		trace.reset()
		index.reset()
		next, err := s.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: limit, Cursor: first.Next, ExpectedSnapshot: first.Snapshot}, scope)
		if err != nil {
			t.Fatal(err)
		}
		count = len(trace.sql) + len(index.sql)
		if count > 12 || continuationCount != 0 && count != continuationCount {
			t.Fatal("continuation statements", limit, count, trace.sql, index.sql)
		}
		continuationCount = count
		for q, n := range index.visits {
			if strings.Contains(q, "FROM memberships") && n > int(limit+1) {
				t.Fatal("candidate scan")
			}
			if strings.Contains(q, "FROM items WHERE content=") && n != 1 {
				t.Fatal("identity visits")
			}
		}
		for _, q := range append(append([]string{}, trace.sql...), index.sql...) {
			if strings.Contains(q, "tc_content_versions") || strings.Contains(q, "OFFSET") || strings.Contains(q, "tc_catalog_content") {
				t.Fatal("source/global fallback", q)
			}
		}
		t.Logf("limit=%d first statements=%d continuation=%d next inspected=%d", limit, firstCount, continuationCount, next.Inspected)
	}
	// Query plan covers payload and identity in the primary B-tree; no temp sort.
	statement, args := libraryRange(lib.Query{Sort: lib.IdentityAsc, Versions: lib.All}, 128, nil)
	rows, err := o.library.index.Query("EXPLAIN QUERY PLAN "+statement, args...)
	if err != nil {
		t.Fatal(err)
	}
	var plan string
	for rows.Next() {
		var a, b, c int
		var detail string
		if err := rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail
	}
	rows.Close()
	if !strings.Contains(plan, "COVERING INDEX library_identity") || strings.Contains(plan, "TEMP B-TREE") {
		t.Fatal(plan)
	}
	if !reflect.DeepEqual(before, catalogRecoveryRows(t, o.database.db)) {
		t.Fatal("authorized page changed source/schema/receipts")
	}
	// Real owner callback runs once, nesting is rejected, handle is revoked.
	var escaped lib.Reader
	calls := 0
	err = o.WithinLibraryRead(t.Context(), scope, func(c context.Context, r lib.Reader) error {
		calls++
		escaped = r
		if err := o.Close(c); err == nil {
			t.Fatal("nested close")
		}
		if err := o.WithinCatalogTransaction(c, func(catalog.CatalogRepository) error { return nil }); err != catalog.ErrRepositoryContention {
			t.Fatal("nested catalog", err)
		}
		if _, err := s.Prepare(c, scope); err != lib.ErrBusy {
			t.Fatal("nested library", err)
		}
		_, err := r.ListLibrary(c, lib.Query{}, lib.Page{Limit: 1})
		return err
	})
	if err != nil || calls != 1 {
		t.Fatal(calls, err)
	}
	if _, err := escaped.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 1}); err != lib.ErrUnauthorized {
		t.Fatal("escaped reader", err)
	}
	// Busy is immediate under the same real lifecycle gate.
	if err := o.gate.enterContentOperation(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, err = s.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 1}, scope)
	o.gate.leave()
	if err != lib.ErrBusy {
		t.Fatal(err)
	}
	// A leased physical connection causes bounded acquisition contention; earlier
	// caller deadline wins and cannot release a successful partial response.
	conn, err := o.database.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	c, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	got, err := s.ListLibrary(c, lib.Query{}, lib.Page{Limit: 1}, scope)
	cancel()
	conn.Close()
	if !errors.Is(err, context.DeadlineExceeded) || len(got.Items) != 0 {
		t.Fatal(got, err)
	}
}
func TestLibraryEpochABA(t *testing.T) {
	for _, order := range []string{lib.IdentityAsc, lib.TitleAsc, lib.TitleDesc} {
		q := lib.Query{Sort: order}
		for _, kind := range []string{"local-write-undo", "external-commit-undo", "unmanaged-save", "review", "order", "import", "registration", "supersession", "source-revocation", "media"} {
			t.Run(order+"/"+kind, func(t *testing.T) {
				var o *LifecycleOwner
				var mutate func()
				if kind == "supersession" {
					f := newSupersessionFixture(t)
					o = f.owner
					mutate = func() {
						if _, err := f.sup.SupersedeVersion(t.Context(), f.command); err != nil {
							t.Fatal(err)
						}
					}
				} else if kind == "review" || kind == "order" || kind == "import" || kind == "registration" || kind == "source-revocation" {
					f := newRegistrationIntegrationFixture(t)
					o = f.owner
					switch kind {
					case "review":
						mutate = func() { f.correct(t, 0) }
					case "order":
						mutate = func() { f.decidePosition(t, f.versions[0], 1) }
					case "import":
						mutate = func() {
							report, err := parser.Parse(t.Context(), []byte(`{"format":"catalog-offline-v1","records":[{"record":"later","kind":"public","title":"additional staged evidence"}]}`))
							if err != nil {
								t.Fatal(err)
							}
							service, err := catalog.NewImportService(o, f.operator.source, f.operator.ingest)
							if err != nil {
								t.Fatal(err)
							}
							batch, err := domain.NewBatchID()
							if err != nil {
								t.Fatal(err)
							}
							if _, err := service.Import(t.Context(), batch, report); err != nil {
								t.Fatal(err)
							}
						}
					case "registration":
						mutate = func() { f.addVersion(t, 0) }
					case "source-revocation":
						mutate = func() {
							catalogSchemaExec(t, o.database.db, "UPDATE tc_catalog_sources SET revoked=1 WHERE source_id=?", f.public.source.ID().String())
						}
					}
				} else {
					o = catalogQueryOwner(t)
					libraryUnmanaged(t, o, 1, 1, 2, "baseline")
					mutate = func() {
						catalogSchemaExec(t, o.database.db, "UPDATE tc_catalog_content SET title='changed' WHERE content_id=?", catalogSchemaID("cnt", 1))
						catalogSchemaExec(t, o.database.db, "UPDATE tc_catalog_content SET title='baseline' WHERE content_id=?", catalogSchemaID("cnt", 1))
					}
				}
				s := lib.NewService(o)
				scope := o.LibraryReadScope()
				libraryReady(t, o, s, scope)
				first, err := s.ListLibrary(t.Context(), q, lib.Page{Limit: 1}, scope)
				if err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "external-commit-undo":
					dsn, _, err := connectionDSN(o.config)
					if err != nil {
						t.Fatal(err)
					}
					connector, err := sqliteDriver.NewConnector(dsn)
					if err != nil {
						t.Fatal(err)
					}
					db := sql.OpenDB(connector)
					defer db.Close()
					if _, err := db.Exec("UPDATE tc_catalog_content SET title='changed'"); err != nil {
						t.Fatal(err)
					}
					if _, err := db.Exec("UPDATE tc_catalog_content SET title='baseline'"); err != nil {
						t.Fatal(err)
					}
				case "unmanaged-save":
					// Actual generic repository Save, including same-value ABA.
					c := testContent(t, "unmanaged", false)
					if err := o.WithinTransaction(t.Context(), func(r catalog.ContentRepository) error { return r.Save(t.Context(), c) }); err != nil {
						t.Fatal(err)
					}
				case "media":
					catalogSchemaExec(t, o.database.db, "INSERT INTO tc_blobs VALUES (?,1,4097,1)", make([]byte, 32))
					catalogSchemaExec(t, o.database.db, "INSERT INTO tc_version_blobs VALUES (?,?)", catalogSchemaID("ver", 1), make([]byte, 32))
				default:
					mutate()
				}
				before := catalogRecoveryRows(t, o.database.db)
				old := lib.Page{Limit: 1, Cursor: first.Next, ExpectedSnapshot: first.Snapshot}
				if r, err := s.ListLibrary(t.Context(), q, old, scope); err != lib.ErrStaleCursor || len(r.Items) != 0 {
					t.Fatal(r, err)
				}
				if _, err := s.ListLibrary(t.Context(), q, lib.Page{Limit: 1}, scope); err != lib.ErrIndexNotReady {
					t.Fatal(err)
				}
				libraryReady(t, o, s, scope)
				fresh, err := s.ListLibrary(t.Context(), q, lib.Page{Limit: 1}, scope)
				if err != nil || fresh.Snapshot == first.Snapshot {
					t.Fatal("ABA revived snapshot", err)
				}
				if _, err := s.ListLibrary(t.Context(), q, old, scope); err != lib.ErrStaleCursor {
					t.Fatal("ABA revived cursor", err)
				}
				if !reflect.DeepEqual(before, catalogRecoveryRows(t, o.database.db)) {
					t.Fatal("read/refresh altered source")
				}
			})
		}
	}
}
func TestLibraryLifecycleFence(t *testing.T) {
	t.Run("close-reopen", func(t *testing.T) {
		o := catalogQueryOwner(t)
		libraryUnmanaged(t, o, 1, 1, 2, "")
		s := lib.NewService(o)
		scope := o.LibraryReadScope()
		libraryReady(t, o, s, scope)
		first, err := s.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 1}, scope)
		if err != nil {
			t.Fatal(err)
		}
		path := o.config.Path
		if err := o.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if o.library.index != nil {
			t.Fatal("index survives close")
		}
		if _, err := s.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 1}, scope); err != lib.ErrUnauthorized {
			t.Fatal(err)
		}
		reopened, err := OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close(context.Background())
		next := lib.NewService(reopened)
		scope = reopened.LibraryReadScope()
		libraryReady(t, reopened, next, scope)
		if _, err := next.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 1, Cursor: first.Next, ExpectedSnapshot: first.Snapshot}, scope); err != lib.ErrStaleCursor {
			t.Fatal(err)
		}
	})
	for _, version := range []int{8, 9, 10} {
		t.Run("restore-v"+string(rune('0'+version)), func(t *testing.T) {
			snapshotOwner, err := OpenLifecycleOwner(t.Context(), Config{Path: filepath.Join(t.TempDir(), "source.sqlite")}, SchemaMigrations()[:version])
			if err != nil {
				t.Fatal(err)
			}
			defer snapshotOwner.Close(context.Background())
			libraryUnmanaged(t, snapshotOwner, 1, 1, 2, "")
			snapshot, err := snapshotOwner.database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "backup.sqlite"), SchemaMigrations())
			if err != nil {
				t.Fatal(err)
			}
			o := catalogQueryOwner(t)
			libraryUnmanaged(t, o, 1, 1, 2, "")
			s := lib.NewService(o)
			scope := o.LibraryReadScope()
			libraryReady(t, o, s, scope)
			first, err := s.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 1}, scope)
			if err != nil {
				t.Fatal(err)
			}
			if err := o.Restore(t.Context(), snapshot, filepath.Join(t.TempDir(), "restored.sqlite")); err != nil {
				t.Fatal(err)
			}
			if o.library.index != nil {
				t.Fatal("restore retained index")
			}
			if _, err := s.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 1}, scope); err != lib.ErrUnauthorized {
				t.Fatal(err)
			}
			scope = o.LibraryReadScope()
			if _, err := s.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 1, Cursor: first.Next, ExpectedSnapshot: first.Snapshot}, scope); err != lib.ErrStaleCursor {
				t.Fatal(err)
			}
			_, err = s.Prepare(t.Context(), scope)
			if version == 8 {
				if err != lib.ErrUnsupported {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("physical-discard-replacement", func(t *testing.T) {
		o := catalogQueryOwner(t)
		libraryUnmanaged(t, o, 1, 1, 2, "")
		path := o.config.Path
		if err := o.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		var reopenErr error
		o, reopenErr = OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
		if reopenErr != nil {
			t.Fatal(reopenErr)
		}
		defer o.Close(context.Background())
		s := lib.NewService(o)
		scope := o.LibraryReadScope()
		libraryReady(t, o, s, scope)
		first, err := s.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 1}, scope)
		if err != nil {
			t.Fatal(err)
		}
		generation := o.database.physicalGeneration.Load()
		oldEpoch := o.library.epoch
		c, err := o.database.db.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		_ = c.Raw(func(any) error { return driver.ErrBadConn })
		_ = c.Close()
		replacement, err := o.database.db.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		epoch, err := o.library.sample(t.Context(), replacement, o.database)
		replacement.Close()
		if err != nil {
			t.Fatal(err)
		}
		if epoch.changes != oldEpoch.changes || epoch.data != oldEpoch.data {
			t.Fatal("fixture did not reproduce equal physical counters", oldEpoch, epoch)
		}
		if _, err := s.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 1, Cursor: first.Next, ExpectedSnapshot: first.Snapshot}, scope); err != lib.ErrStaleCursor {
			t.Fatal(err)
		}
		if o.database.physicalGeneration.Load() <= generation {
			t.Fatal("physical reconnect uncounted")
		}
	})
}
func TestLibraryLifecycleFenceLateFailure(t *testing.T) {
	for _, kind := range []string{"cancel", "epoch-final", "uncertain-cleanup", "revoke"} {
		t.Run(kind, func(t *testing.T) {
			o := catalogQueryOwner(t)
			libraryUnmanaged(t, o, 1, 1, 2, "")
			trace := libraryTraceSource(t, o)
			s := lib.NewService(o)
			scope := o.LibraryReadScope()
			libraryReady(t, o, s, scope)
			first, err := s.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 1}, scope)
			if err != nil {
				t.Fatal(err)
			}
			c, cancel := context.WithCancel(t.Context())
			defer cancel()
			samples := 0
			trace.hook = func(q string) {
				if !strings.Contains(q, "pragma_data_version") {
					return
				}
				samples++
				if samples != 4 {
					return
				}
				switch kind {
				case "cancel":
					cancel()
				case "revoke":
					o.RevokeLibraryReadScopes()
				case "epoch-final":
					dsn, _, _ := connectionDSN(o.config)
					connector, _ := sqliteDriver.NewConnector(dsn)
					other := sql.OpenDB(connector)
					defer other.Close()
					if _, err := other.Exec("UPDATE tc_catalog_content SET title='racing'; UPDATE tc_catalog_content SET title=''"); err != nil {
						t.Error(err)
					}
				}
			}
			if kind == "uncertain-cleanup" {
				trace.rollbackError = true
			}
			before := catalogRecoveryRows(t, o.database.db)
			r, err := s.ListLibrary(c, lib.Query{}, lib.Page{Limit: 1, Cursor: first.Next, ExpectedSnapshot: first.Snapshot}, scope)
			want := map[string]error{"cancel": context.Canceled, "epoch-final": lib.ErrStaleCursor, "uncertain-cleanup": lib.ErrUnavailable, "revoke": lib.ErrUnauthorized}[kind]
			if !errors.Is(err, want) || len(r.Items) != 0 || r.Next != "" {
				t.Fatal(r, err, want)
			}
			trace.hook = nil
			if kind != "revoke" && o.library.index != nil {
				t.Fatal("failure retained index")
			}
			if !reflect.DeepEqual(before, catalogRecoveryRows(t, o.database.db)) {
				t.Fatal("failure changed values/receipts")
			}
		})
	}
}
func TestLibraryLifecycleFenceProcessRestart(t *testing.T) {
	o := catalogQueryOwner(t)
	libraryUnmanaged(t, o, 1, 1, 2, "")
	s := lib.NewService(o)
	scope := o.LibraryReadScope()
	libraryReady(t, o, s, scope)
	first, err := s.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 1}, scope)
	if err != nil {
		t.Fatal(err)
	}
	page := lib.Page{Limit: 1, Cursor: first.Next, ExpectedSnapshot: first.Snapshot}
	raw, _ := json.Marshal(page)
	path := o.config.Path
	if _, err := s.ListLibrary(t.Context(), lib.Query{}, page, scope); err != nil {
		t.Fatal("old token was never valid", err)
	}
	if err := o.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), executable, "-test.run=^TestLibraryLifecycleFenceProcessHelper$", "-test.v")
	command.Env = append(os.Environ(), "TC_LIBRARY_RESTART_PATH="+path, "TC_LIBRARY_OLD_PAGE="+string(raw))
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "library restart boundary checked") {
		t.Fatal(err, string(output))
	}
}
func TestLibraryLifecycleFenceProcessHelper(t *testing.T) {
	path := os.Getenv("TC_LIBRARY_RESTART_PATH")
	if path == "" {
		return
	}
	var p lib.Page
	if err := json.Unmarshal([]byte(os.Getenv("TC_LIBRARY_OLD_PAGE")), &p); err != nil {
		t.Fatal(err)
	}
	o, err := OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close(context.Background())
	s := lib.NewService(o)
	scope := o.LibraryReadScope()
	if _, err := s.ListLibrary(t.Context(), lib.Query{}, p, scope); err != lib.ErrStaleCursor {
		t.Fatal("token survived restart", err)
	}
	libraryReady(t, o, s, scope)
	if _, err := s.ListLibrary(t.Context(), lib.Query{}, p, scope); err != lib.ErrStaleCursor {
		t.Fatal("token revived after restart rebuild", err)
	}
	t.Log("library restart boundary checked")
}

func TestLibraryLifecycleFenceDrains(t *testing.T) {
	o := catalogQueryOwner(t)
	libraryUnmanaged(t, o, 1, 1, 2, "")
	scope := o.LibraryReadScope()
	s := lib.NewService(o)
	libraryReady(t, o, s, scope)
	entered := make(chan struct{})
	release := make(chan struct{})
	readDone := make(chan error, 1)
	closeDone := make(chan error, 1)
	go func() {
		readDone <- o.WithinLibraryRead(t.Context(), scope, func(c context.Context, r lib.Reader) error {
			if _, err := r.ListLibrary(c, lib.Query{}, lib.Page{Limit: 1}); err != nil {
				return err
			}
			close(entered)
			<-release
			return nil
		})
	}()
	select {
	case <-entered:
	case err := <-readDone:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("read admission")
	}
	go func() { closeDone <- o.Close(t.Context()) }()
	deadline := time.After(time.Second)
	for {
		o.gate.mutex.Lock()
		waiting := o.gate.lifecycleWaiters > 0
		o.gate.mutex.Unlock()
		if waiting {
			break
		}
		select {
		case err := <-closeDone:
			close(release)
			t.Fatal("close crossed active reader", err)
		case <-deadline:
			close(release)
			t.Fatal("close never reached lifecycle fence")
		case <-time.After(time.Millisecond):
		}
	}
	select {
	case err := <-closeDone:
		close(release)
		t.Fatal("premature close", err)
	default:
	}
	close(release)
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	if o.library.index != nil || scope.Check() != lib.ErrUnauthorized {
		t.Fatal("close did not revoke drained capability")
	}
}

func TestLibraryReadOnlyBoundsErrorCategories(t *testing.T) {
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	deadline, stop := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer stop()
	for _, ctx := range []context.Context{canceled, deadline} {
		for _, err := range []error{nil, catalog.ErrUnsupported, catalog.ErrLimitExceeded, catalog.ErrRepositoryContention, lib.ErrUnavailable, errors.New("private SQL path")} {
			if got := libraryMapError(ctx, err); got != ctx.Err() {
				t.Fatal("earlier transaction cancellation/deadline lost", got, ctx.Err())
			}
		}
	}
}

func TestLibraryReadOnlyBoundsNoRetry(t *testing.T) {
	o := catalogQueryOwner(t)
	libraryUnmanaged(t, o, 1, 1, 2, "")
	s := lib.NewService(o)
	scope := o.LibraryReadScope()
	libraryReady(t, o, s, scope)
	before := catalogRecoveryRows(t, o.database.db)
	calls := 0
	err := o.WithinLibraryRead(t.Context(), scope, func(c context.Context, r lib.Reader) error {
		calls++
		if _, err := r.ListLibrary(c, lib.Query{}, lib.Page{Limit: 1}); err != nil {
			return err
		}
		if _, err := r.ListLibrary(c, lib.Query{}, lib.Page{Limit: 1}); err != lib.ErrBusy {
			t.Fatal("repository admitted a retry", err)
		}
		// The owner must retain a repository failure even if callback code ignores it.
		return nil
	})
	if err != lib.ErrBusy || calls != 1 {
		t.Fatal(calls, err)
	}
	if !reflect.DeepEqual(before, catalogRecoveryRows(t, o.database.db)) {
		t.Fatal("retry rejection changed source")
	}
}
