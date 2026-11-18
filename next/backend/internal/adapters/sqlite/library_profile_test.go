package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	catalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	lib "github.com/shentschel/teddycloud/next/backend/internal/application/library"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
	"modernc.org/libc"
	sqliteDriver "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Unlike driver.Rows.Next counts, VM_STEP includes rejected rows, subqueries
// and correlated loops. Counters are read before the driver finalizes the real
// statement. This test-only adapter deliberately fails on driver layout drift;
// unavailable instrumentation must never silently turn into a passing gate.
type libraryProfileWork struct{ rows, steps, scans, sorts, executions int }
type libraryProfileMeter struct {
	mu      sync.Mutex
	enabled bool
	trace   []string
	work    map[string]libraryProfileWork
}

func (m *libraryProfileMeter) reset(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled, m.trace, m.work = enabled, nil, make(map[string]libraryProfileWork)
}
func (m *libraryProfileMeter) record(q string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.enabled {
		m.trace = append(m.trace, q)
	}
	return m.enabled
}

type libraryProfileConnector struct {
	driver.Connector
	meter *libraryProfileMeter
}

func (c *libraryProfileConnector) Connect(ctx context.Context) (driver.Conn, error) {
	raw, e := c.Connector.Connect(ctx)
	if e != nil {
		return nil, e
	}
	return &libraryProfileConn{Conn: raw, meter: c.meter}, nil
}

type libraryProfileConn struct {
	driver.Conn
	meter *libraryProfileMeter
}

func (c *libraryProfileConn) QueryContext(ctx context.Context, q string, a []driver.NamedValue) (driver.Rows, error) {
	on := c.meter.record(q)
	r, e := c.Conn.(driver.QueryerContext).QueryContext(ctx, q, a)
	if e != nil || !on {
		return r, e
	}
	return &libraryProfileRows{Rows: r, meter: c.meter, query: q}, nil
}
func (c *libraryProfileConn) ExecContext(ctx context.Context, q string, a []driver.NamedValue) (driver.Result, error) {
	c.meter.record(q)
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, a)
}
func (c *libraryProfileConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	if c.meter.record("PREPARE " + q) {
		return nil, fmt.Errorf("unmetered prepared execution: %s", q)
	}
	return c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, q)
}
func (c *libraryProfileConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	c.meter.record("BEGIN")
	r, e := c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
	if e != nil {
		return nil, e
	}
	return &libraryProfileTx{Tx: r, meter: c.meter}, nil
}
func (c *libraryProfileConn) ResetSession(ctx context.Context) error {
	return c.Conn.(driver.SessionResetter).ResetSession(ctx)
}
func (c *libraryProfileConn) IsValid() bool                  { return c.Conn.(driver.Validator).IsValid() }
func (c *libraryProfileConn) Ping(ctx context.Context) error { return c.Conn.(driver.Pinger).Ping(ctx) }

// The test observer must preserve the physical driver's backup capability for
// the composed lifecycle proof, just as it preserves reset and cancellation.
func (c *libraryProfileConn) NewBackup(path string) (*sqliteDriver.Backup, error) {
	return c.Conn.(sqliteBackuper).NewBackup(path)
}
func (c *libraryProfileConn) NewRestore(path string) (*sqliteDriver.Backup, error) {
	return c.Conn.(sqliteBackuper).NewRestore(path)
}

type libraryProfileTx struct {
	driver.Tx
	meter *libraryProfileMeter
}

func (t *libraryProfileTx) Commit() error   { t.meter.record("COMMIT"); return t.Tx.Commit() }
func (t *libraryProfileTx) Rollback() error { t.meter.record("ROLLBACK"); return t.Tx.Rollback() }

type libraryProfileRows struct {
	driver.Rows
	meter  *libraryProfileMeter
	query  string
	rows   int
	closed bool
}

func (r *libraryProfileRows) Next(v []driver.Value) error {
	e := r.Rows.Next(v)
	if e == nil {
		r.rows++
	}
	return e
}
func (r *libraryProfileRows) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	v := reflect.ValueOf(r.Rows)
	if v.Kind() != reflect.Pointer || v.Elem().Type().PkgPath() != "modernc.org/sqlite" || v.Elem().Type().Name() != "rows" {
		return fmt.Errorf("VM instrumentation: unexpected rows %T", r.Rows)
	}
	v = v.Elem()
	p, c := v.FieldByName("pstmt"), v.FieldByName("c")
	if !p.IsValid() || p.Kind() != reflect.Uintptr || p.Uint() == 0 || !c.IsValid() || c.IsNil() {
		return fmt.Errorf("VM instrumentation: driver layout changed")
	}
	tlsField := c.Elem().FieldByName("tls")
	if !tlsField.IsValid() || tlsField.Type() != reflect.TypeFor[*libc.TLS]() || tlsField.IsNil() {
		return fmt.Errorf("VM instrumentation: TLS unavailable")
	}
	tls := (*libc.TLS)(tlsField.UnsafePointer())
	status := func(op int32) int { return int(sqlite3.Xsqlite3_stmt_status(tls, uintptr(p.Uint()), op, 0)) }
	w := libraryProfileWork{rows: r.rows, steps: status(sqlite3.SQLITE_STMTSTATUS_VM_STEP), scans: status(sqlite3.SQLITE_STMTSTATUS_FULLSCAN_STEP), sorts: status(sqlite3.SQLITE_STMTSTATUS_SORT), executions: 1}
	runtime.KeepAlive(r.Rows)
	r.meter.mu.Lock()
	old := r.meter.work[r.query]
	w.rows += old.rows
	w.steps += old.steps
	w.scans += old.scans
	w.sorts += old.sorts
	w.executions += old.executions
	r.meter.work[r.query] = w
	r.meter.mu.Unlock()
	return r.Rows.Close()
}

type libraryProfileFixture struct {
	owner         *LifecycleOwner
	service       *lib.Service
	scope         lib.ReadScope
	truth         []libraryTruth
	source, index *libraryProfileMeter
}

func libraryProfileSource(t testing.TB, o *LifecycleOwner) *libraryProfileMeter {
	t.Helper()
	dsn, _, e := connectionDSN(o.config)
	if e != nil {
		t.Fatal(e)
	}
	base, e := sqliteDriver.NewConnector(dsn)
	if e != nil {
		t.Fatal(e)
	}
	if e = o.database.db.Close(); e != nil {
		t.Fatal(e)
	}
	m := &libraryProfileMeter{}
	m.reset(false)
	o.database.db = sql.OpenDB(&incarnationConnector{Connector: &libraryProfileConnector{Connector: base, meter: m}, generation: &o.database.physicalGeneration})
	o.database.db.SetMaxOpenConns(1)
	o.database.db.SetMaxIdleConns(1)
	return m
}
func libraryProfileIndex(t testing.TB, f *libraryProfileFixture) {
	t.Helper()
	// Install on the empty staging index, before Advance. No copy/rebuild of a
	// ready inventory, altered query, or synthetic ready state is timed as a page.
	base, e := sqliteDriver.NewConnector(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	if e = f.owner.library.index.Close(); e != nil {
		t.Fatal(e)
	}
	f.index = &libraryProfileMeter{}
	f.index.reset(false)
	db := sql.OpenDB(&libraryProfileConnector{Connector: base, meter: f.index})
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	f.owner.library.index = db
	if _, e := db.ExecContext(t.Context(), libraryIndexSchema); e != nil {
		t.Fatal(e)
	}
}

// Small managed Contents exercise accepted/equal/conflicted/unknown titles,
// owner/current lanes and identical fingerprints. The large, independent
// unmanaged inventory uses four valid versions per Content. No managed review
// state or ready index is fabricated to reach the requested cardinality.
func libraryProfileNew(t *testing.T, n int) *libraryProfileFixture {
	t.Helper()
	var memStart, memEnd runtime.MemStats
	runtime.ReadMemStats(&memStart)
	start := time.Now()
	o, truth := librarySearchFixture(t, nil)
	for i := range truth {
		if truth[i].item.ContentID < catalogSchemaID("cnt", 90) {
			truth[i].item.Supersession.Status = catalog.Current
		}
	}
	// The searchable/current population scales 10x with each cardinality,
	// rather than holding a tiny title partition constant under a large total.
	managedContents := n / 1000
	reviewer, e := domain.NewIssuerPrincipal()
	if e != nil {
		t.Fatal(e)
	}
	source := newRegistrationIntegrationSource(t, reviewer, domain.CoreOperator)
	registration := registrationFixture{owner: o, source: source.source, ingest: source.ingest, review: source.review, namespace: source.space}
	registration.capability, e = domain.NewCatalogRegistrationCapability(source.review, []domain.ReviewCapability{source.review})
	if e != nil {
		t.Fatal(e)
	}
	registration.bind(t)
	seedSubject, _ := domain.ContentSubject(mustProfileContent(1000000))
	seedClaim, _ := domain.TitleClaim("ALPHA")
	sharedCoordinate := registration.observation(t, seedSubject, seedClaim).Input().Coordinate
	shareCoordinate := func(o domain.CatalogObservation) domain.CatalogObservation {
		input := o.Input()
		input.Coordinate = sharedCoordinate
		shared, e := domain.NewCatalogObservation(registration.ingest, registration.source, input)
		if e != nil {
			t.Fatal(e)
		}
		return shared
	}
	for i := 0; i < managedContents; i++ {
		subject, _ := domain.ContentSubject(mustProfileContent(1000000 + i))
		claim, _ := domain.TitleClaim("ALPHA")
		observation := shareCoordinate(registration.observation(t, subject, claim))
		registration.command = catalog.RegistrationCommand{ID: registrationIntegrationCommandID(t), Subject: subject, ExpectedKind: domain.CatalogExpectedAbsent, Observations: []domain.CatalogObservation{observation}, Selections: []domain.CatalogRegistrationSelection{{Key: domain.TitleClaimKey, Observation: observation.ID()}}, Reason: "profile equal-title small Content"}
		if _, e = registration.service.RegisterContent(t.Context(), registration.command); e != nil {
			t.Fatal("managed profile content", i, e)
		}
		for j := 1; j <= 4; j++ {
			id := 1000000 + i*4 + j
			command := registration.version(t, id, uint64(j))
			for k := range command.Observations {
				command.Observations[k] = shareCoordinate(command.Observations[k])
			}
			if _, e = registration.service.RegisterCatalogVersion(t.Context(), command); e != nil {
				t.Fatal("managed profile version", i, j, e)
			}
			truth = append(truth, libraryTruth{item: lib.Item{ContentID: subject.ContentID().String(), VersionID: catalogSchemaID("ver", id), Supersession: lib.Supersession{Status: catalog.Current}}, key: "alpha"})
		}
	}
	f := &libraryProfileFixture{owner: o, service: lib.NewService(o), scope: o.LibraryReadScope(), truth: truth}
	tx, e := o.database.db.BeginTx(t.Context(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	content, e := tx.PrepareContext(t.Context(), "INSERT INTO tc_catalog_content VALUES (?,?,NULL,NULL)")
	if e != nil {
		t.Fatal(e)
	}
	defer content.Close()
	version, e := tx.PrepareContext(t.Context(), "INSERT INTO tc_content_versions VALUES (?,?,123,?,0,NULL,NULL)")
	if e != nil {
		t.Fatal(e)
	}
	defer version.Close()
	for i := len(truth); i < n; i++ {
		owner := catalogSchemaID("cnt", 1000+(i-len(truth))/4)
		if (i-len(truth))%4 == 0 {
			if _, e = content.ExecContext(t.Context(), owner, "same fingerprint"); e != nil {
				t.Fatal(e)
			}
		}
		id := catalogSchemaID("ver", 10000+i)
		if _, e = version.ExecContext(t.Context(), id, owner, bytes.Repeat([]byte{1}, 20)); e != nil {
			t.Fatal(e)
		}
		f.truth = append(f.truth, libraryTruth{item: lib.Item{ContentID: owner, VersionID: id}})
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	f.source = libraryProfileSource(t, o)
	t.Logf("fixture items=%d contents=%d managed_contents=%d managed_versions=%d unmanaged_versions=%d construction=%s SQL-seeded unmanaged rows validated by complete Prepare/Advance", n, 12+managedContents+(n-len(truth)+3)/4, 11+managedContents, 33+4*managedContents, n-33-4*managedContents, time.Since(start))
	runtime.ReadMemStats(&memEnd)
	t.Logf("fixture allocs=%d allocated_bytes=%d heap_after=%d process_peak=%s", memEnd.Mallocs-memStart.Mallocs, memEnd.TotalAlloc-memStart.TotalAlloc, memEnd.HeapAlloc, libraryProfilePeak())
	return f
}

// Independent oracle: fixture keys and a direct tuple comparator, never the
// production normalizer, key encoder, SQL range, projection or comparator.
func libraryProfileOracle(truth []libraryTruth, q lib.Query) []string {
	selected := make([]libraryTruth, 0)
	for _, r := range truth {
		managed := r.item.Supersession.Status == catalog.Current
		if q.OwnerContentID != nil && r.item.ContentID != q.OwnerContentID.String() || q.Versions == lib.Current && !managed || q.Search != nil && (r.key == "" || !strings.HasPrefix(r.key, *q.Search)) {
			continue
		}
		selected = append(selected, r)
	}
	sort.Slice(selected, func(i, j int) bool {
		a, b := selected[i], selected[j]
		if q.Sort != lib.IdentityAsc {
			if (a.key == "") != (b.key == "") {
				return a.key != ""
			}
			if a.key != b.key {
				if q.Sort == lib.TitleDesc {
					return a.key > b.key
				}
				return a.key < b.key
			}
		}
		return a.item.ContentID+a.item.VersionID < b.item.ContentID+b.item.VersionID
	})
	out := make([]string, len(selected))
	for i, r := range selected {
		out[i] = r.item.ContentID + r.item.VersionID
	}
	return out
}
func libraryProfileShapes() []lib.Query {
	var out []lib.Query
	for _, lane := range []string{lib.All, lib.Current} {
		for _, order := range []string{lib.IdentityAsc, lib.TitleAsc, lib.TitleDesc} {
			for _, owner := range []*identity.ContentID{nil, ptrContent(mustProfileContent(1)), ptrContent(mustProfileContent(1000)), ptrContent(mustProfileContent(999999))} {
				q := lib.Query{Versions: lane, Sort: order, OwnerContentID: owner}
				out = append(out, q)
				if order != lib.IdentityAsc {
					for _, prefix := range []string{"a", "alphabet", "no-match"} {
						copy := q
						p := prefix
						copy.Search = &p
						out = append(out, copy)
					}
				}
			}
		}
	}
	return out
}
func mustProfileContent(n int) identity.ContentID {
	id, e := identity.ParseContentID(catalogSchemaID("cnt", n))
	if e != nil {
		panic(e)
	}
	return id
}

func libraryProfileBuild(t *testing.T, f *libraryProfileFixture, label string) {
	t.Helper()
	f.source.reset(false)
	if f.index != nil {
		f.index.reset(false)
	}
	buildStart := time.Now()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	p, e := f.service.Prepare(t.Context(), f.scope)
	prepareElapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	if e != nil {
		t.Fatal(label, "prepare", e)
	}
	t.Logf("build=%s prepare=%s allocs=%d allocated_bytes=%d heap_after=%d process_peak=%s cache=new-private-index/source-OS-cache-uncontrolled", label, prepareElapsed, after.Mallocs-before.Mallocs, after.TotalAlloc-before.TotalAlloc, after.HeapAlloc, libraryProfilePeak())
	libraryProfileIndex(t, f)
	var advances []time.Duration
	for i := 0; !p.Ready; i++ {
		if i > 10000 {
			t.Fatal("unbounded advance")
		}
		runtime.ReadMemStats(&before)
		start = time.Now()
		p, e = f.service.Advance(t.Context(), f.scope)
		elapsed := time.Since(start)
		runtime.ReadMemStats(&after)
		if e != nil {
			t.Fatal(label, "advance", i, elapsed, e)
		}
		advances = append(advances, elapsed)
		t.Logf("build=%s advance=%d elapsed=%s contents=%d items=%d staging=%d pages=%d allocs=%d allocated_bytes=%d heap_after=%d process_peak=%s", label, i, elapsed, p.Contents, p.Items, p.Bytes, p.Pages, after.Mallocs-before.Mallocs, after.TotalAlloc-before.TotalAlloc, after.HeapAlloc, libraryProfilePeak())
	}
	if int(p.Items) != len(f.truth) || p.Bytes > lib.MaxStagingBytes || p.Pages*4096 > lib.MaxIndexBytes {
		t.Fatal("build bounds", p, len(f.truth))
	}
	t.Logf("build=%s total=%s advance_raw=%v", label, time.Since(buildStart), advances)
	start = time.Now()
	first, cost := libraryProfileRequest(t, f, lib.Query{}, lib.Page{Limit: 1})
	firstElapsed := time.Since(start)
	t.Logf("build=%s first-ready-page=%s items=%d work=%+v source_trace=%v index_trace=%v source_VM=%v index_VM=%v", label, firstElapsed, len(first.Items), cost, f.source.trace, f.index.trace, f.source.work, f.index.work)
}

func libraryProfilePeak() string {
	data, e := os.ReadFile("/proc/self/status")
	if e != nil {
		return "unavailable"
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmHWM:") {
			return line
		}
	}
	return "unavailable"
}

type libraryProfileCost struct{ statements, candidates, identities, scalar, steps, scans int }

func libraryProfileCheckCost(t testing.TB, f *libraryProfileFixture, p lib.Page, r lib.Result) libraryProfileCost {
	t.Helper()
	out := libraryProfileCost{statements: len(f.source.trace) + len(f.index.trace)}
	if out.statements > 12 {
		t.Fatal("D7 statements", out, f.source.trace, f.index.trace)
	}
	for q, w := range f.source.work {
		out.scalar += w.rows
		out.steps += w.steps
		out.scans += w.scans
		// The schema ledger is fixed (9/10 rows), not inventory dependent.
		if w.steps <= 0 || w.steps > 256 || w.scans > 10 || w.sorts != 0 {
			t.Fatal("unbounded source VM work", q, w)
		}
	}
	for q, w := range f.index.work {
		out.steps += w.steps
		out.scans += w.scans
		if strings.Contains(q, "FROM memberships") {
			out.candidates += w.rows
			if w.steps > 64+20*int(p.Limit+1) {
				t.Fatal("candidate/correlated VM bound", q, w)
			}
		} else if strings.Contains(q, "FROM items WHERE content=") {
			out.identities += w.rows
			if w.steps > 64 {
				t.Fatal("identity VM bound", q, w)
			}
		} else {
			t.Fatal("unexpected index work", q, w)
		}
		if w.steps <= 0 || w.scans != 0 || w.sorts != 0 {
			t.Fatal("full scan/sort or missing VM counter", q, w)
		}
	}
	if out.scalar > 8 || out.candidates != int(r.Inspected) || out.candidates > int(p.Limit+1) || out.identities > 1 || p.Cursor != "" && out.identities != 1 {
		t.Fatal("D7 row bounds", out, p, r.Inspected)
	}
	for _, q := range append(append([]string{}, f.source.trace...), f.index.trace...) {
		if strings.Contains(q, "tc_catalog_content") || strings.Contains(q, "tc_content_versions") || strings.Contains(q, "OFFSET") || strings.HasPrefix(q, "PREPARE ") {
			t.Fatal("source reconstruction/unobserved execution", q)
		}
	}
	// All SELECTs must have their VM counters observed, including zero-row seeks.
	for _, m := range []*libraryProfileMeter{f.source, f.index} {
		for _, q := range m.trace {
			if strings.HasPrefix(q, "SELECT ") && m.work[q].executions == 0 {
				t.Fatal("unobservable VM work", q)
			}
		}
	}
	return out
}
func libraryProfileRequest(t testing.TB, f *libraryProfileFixture, q lib.Query, p lib.Page) (lib.Result, libraryProfileCost) {
	t.Helper()
	f.source.reset(true)
	f.index.reset(true)
	r, e := f.service.ListLibrary(t.Context(), q, p, f.scope)
	if e != nil {
		t.Fatal("ready request failed", q, p, e)
	}
	c := libraryProfileCheckCost(t, f, p, r)
	return r, c
}
func libraryProfilePlan(t *testing.T, f *libraryProfileFixture, q lib.Query, p lib.Page, last *lib.Item) {
	t.Helper()
	f.source.reset(false)
	f.index.reset(false)
	var key []byte
	if last != nil {
		keys, e := libraryItemKeys(*last)
		if e != nil {
			t.Fatal(e)
		}
		key = libraryOrderKey(q.Sort, keys)
	}
	statement, args := libraryRange(q, p.Limit, key)
	rows, e := f.owner.library.index.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+statement, args...)
	if e != nil {
		t.Fatal(e)
	}
	var plan []string
	for rows.Next() {
		var a, b, c int
		var s string
		if e = rows.Scan(&a, &b, &c, &s); e != nil {
			t.Fatal(e)
		}
		plan = append(plan, s)
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	rows.Close()
	text := strings.Join(plan, "; ")
	if len(plan) != 1 || !strings.Contains(text, "SEARCH memberships USING COVERING INDEX library_") || !strings.Contains(text, ">?") || strings.Contains(text, "TEMP B-TREE") || strings.Contains(text, "SCAN ") {
		t.Fatal("unbounded plan", q, text)
	}
	t.Logf("EXPLAIN items=%d q=%+v prefix=%s continuation=%v plan=%s", len(f.truth), q, profilePrefix(q), last != nil, text)
}
func profilePrefix(q lib.Query) string {
	if q.Search == nil {
		return "<none>"
	}
	return *q.Search
}

func libraryProfileRunner(t testing.TB, f *libraryProfileFixture) {
	t.Helper()
	f.source.reset(false)
	f.index.reset(false)
	var version string
	if e := f.owner.library.index.QueryRow("SELECT sqlite_version()").Scan(&version); e != nil {
		t.Fatal(e)
	}
	cpu, _ := os.ReadFile("/proc/cpuinfo")
	memory, _ := os.ReadFile("/proc/meminfo")
	osRelease, _ := os.ReadFile("/etc/os-release")
	cpuName := "unavailable"
	for _, line := range strings.Split(string(cpu), "\n") {
		if strings.HasPrefix(line, "model name") {
			cpuName = line
			break
		}
	}
	if len(memory) > 200 {
		memory = memory[:200]
	}
	if len(osRelease) > 200 {
		osRelease = osRelease[:200]
	}
	t.Logf("runner=%s/%s go=%s sqlite=%s CPUs=%d CPU=%s RAM=%q OS=%q schema=10 profile=%s layout=%s command=go test -count=1 -v -run 'TestLibrary(QueryCost|NoNPlusOne|ProfileGate|Milestone|CompatibilityAudit)$' ./backend/internal/adapters/sqlite", runtime.GOOS, runtime.GOARCH, runtime.Version(), version, runtime.NumCPU(), cpuName, memory, osRelease, lib.Profile, libraryIndexLayout)
}

func libraryProfilePositions(t *testing.T, f *libraryProfileFixture, q lib.Query, limit uint64, want []string) []struct {
	name   string
	page   lib.Page
	offset int
} {
	t.Helper()
	type position = struct {
		name   string
		page   lib.Page
		offset int
	}
	out := []position{{"early", lib.Page{Limit: limit}, 0}}
	// Traverse the actual service for snapshot-bound middle/final cursors and
	// check every page against independent truth. Traversal is outside timings.
	p := lib.Page{Limit: limit}
	offset := 0
	middle := false
	for {
		r, _ := libraryProfileRequest(t, f, q, p)
		if offset+len(r.Items) > len(want) {
			t.Fatal("oracle overflow", q, offset)
		}
		got := make([]string, len(r.Items))
		for i, item := range r.Items {
			got[i] = item.ContentID + item.VersionID
		}
		if !reflect.DeepEqual(got, want[offset:offset+len(got)]) {
			t.Fatal("independent sorted oracle", q, offset, got, want[offset:offset+len(got)])
		}
		if r.Complete != (offset+len(got) == len(want)) {
			t.Fatal("completion/oracle", q, offset)
		}
		if !middle && offset >= len(want)/2 && offset > 0 {
			out = append(out, position{"middle", p, offset})
			middle = true
		}
		if r.Complete {
			if offset > 0 {
				out = append(out, position{"final", p, offset})
			}
			break
		}
		if len(got) == 0 {
			t.Fatal("no progress")
		}
		offset += len(got)
		p.Cursor = r.Next
		p.ExpectedSnapshot = r.Snapshot
	}
	return out
}
func libraryProfileSamples(t *testing.T, f *libraryProfileFixture, q lib.Query, p lib.Page, label string, want []string, offset int) {
	t.Helper()
	var samples, warm []time.Duration
	var costs []libraryProfileCost
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	peak := before.HeapAlloc
	for i := 0; i < 35; i++ {
		if i == 5 {
			runtime.ReadMemStats(&before)
			peak = before.HeapAlloc
		}
		start := time.Now()
		r, c := libraryProfileRequest(t, f, q, p)
		elapsed := time.Since(start)
		if offset+len(r.Items) > len(want) {
			t.Fatal("sample oracle length")
		}
		for j, item := range r.Items {
			if item.ContentID+item.VersionID != want[offset+j] {
				t.Fatal("repeated oracle", label, offset, j)
			}
		}
		if i < 5 {
			warm = append(warm, elapsed)
		} else {
			samples = append(samples, elapsed)
			costs = append(costs, c)
		}
		runtime.ReadMemStats(&after)
		peak = max(peak, after.HeapAlloc)
	}
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	for _, c := range costs {
		if c != costs[0] {
			t.Fatal("repeat work changed", costs)
		}
	}
	t.Logf("profile items=%d lane=%s sort=%s owner=%v prefix=%q limit=%d position=%s cache=prepared-index/source-OS-warm-uncontrolled warmups=5 warm_raw=%v n=30 raw=%v p50=%s p95=%s max=%s allocs_total_30=%d allocated_bytes_30=%d sampled_heap_peak=%d process_peak=%s sql_visits_VM=%+v timeouts=0 failures=0", len(f.truth), q.Versions, q.Sort, q.OwnerContentID, profilePrefix(q), p.Limit, label, warm, samples, sorted[14], sorted[28], sorted[29], after.Mallocs-before.Mallocs, after.TotalAlloc-before.TotalAlloc, peak, libraryProfilePeak(), costs[0])
}

func libraryProfileMatrix(t *testing.T, f *libraryProfileFixture) {
	t.Helper()
	libraryProfileBuild(t, f, "cold")
	libraryProfileRunner(t, f)
	counts := map[bool]int{}
	for _, q := range libraryProfileShapes() {
		want := libraryProfileOracle(f.truth, q)
		for _, limit := range []uint64{1, 16, 128} {
			p := lib.Page{Limit: limit}
			libraryProfilePlan(t, f, q, p, nil)
			// Full 100k oracle traversal is once per shape at 128. Limits 1/16
			// use authenticated cursors derived from independent identity facts,
			// and their actual last-item lookup/membership is still checked.
			positions := []struct {
				name   string
				page   lib.Page
				offset int
			}{{"early", p, 0}}
			if limit == 128 {
				positions = libraryProfilePositions(t, f, q, limit, want)
			} else if len(want) > int(limit) {
				for _, pos := range []struct {
					name   string
					offset int
				}{{"middle", len(want) / 2}, {"final", len(want) - 1}} {
					last := want[pos.offset-1]
					digest := libraryQueryDigest(q, p, f.scope, f.owner.library.incarnation)
					page := p
					page.ExpectedSnapshot = librarySnapshot(f.owner.library)
					page.Cursor = libraryEncodeCursor(f.owner.library.secret, f.owner.library.snapshot, digest, last[:30], last[30:])
					positions = append(positions, struct {
						name   string
						page   lib.Page
						offset int
					}{pos.name, page, pos.offset})
				}
			}
			for _, pos := range positions {
				r, c := libraryProfileRequest(t, f, q, pos.page)
				continuation := pos.page.Cursor != ""
				if counts[continuation] != 0 && counts[continuation] != c.statements {
					t.Fatal("SQL count grows with Limit/shape", counts, c)
				}
				counts[continuation] = c.statements
				if continuation {
					// Recover the actual last item for its title key, outside timing.
					last := want[pos.offset-1]
					var raw []byte
					f.index.reset(false)
					if e := f.owner.library.index.QueryRow("SELECT payload FROM items WHERE content=? AND version=?", last[:30], last[30:]).Scan(&raw); e != nil {
						t.Fatal(e)
					}
					item, e := libraryDecodeItem(raw, last[:30], last[30:])
					if e != nil {
						t.Fatal(e)
					}
					libraryProfilePlan(t, f, q, pos.page, &item)
				}
				if pos.offset == 0 && len(r.Items) > 0 {
					t.Logf("trace source=%v index=%v VM source=%v index=%v", f.source.trace, f.index.trace, f.source.work, f.index.work)
				}
				libraryProfileSamples(t, f, q, pos.page, pos.name, want, pos.offset)
			}
		}
	}
	t.Logf("D7 items=%d statements first=%d continuation=%d: constant across Limits 1/16/128, all shapes/positions; scans limited to fixed schema ledger; no source reconstruction/media/network capability", len(f.truth), counts[false], counts[true])
	// A real source write revokes the generation; refresh is measured separately.
	f.source.reset(false)
	f.index.reset(false)
	first, e := f.service.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 1}, f.scope)
	if e != nil {
		t.Fatal(e)
	}
	catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_catalog_content SET title=title WHERE content_id=?", catalogSchemaID("cnt", 90))
	if _, e = f.service.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 1, Cursor: first.Next, ExpectedSnapshot: first.Snapshot}, f.scope); e != lib.ErrStaleCursor {
		t.Fatal("mutation cursor", e)
	}
	libraryProfileBuild(t, f, "refresh-after-write")
	if _, e = f.service.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 1, Cursor: first.Next, ExpectedSnapshot: first.Snapshot}, f.scope); e != lib.ErrStaleCursor {
		t.Fatal("rebuild revived cursor", e)
	}
}

func TestLibraryQueryCost(t *testing.T) {
	f := libraryProfileNew(t, 1000)
	libraryProfileBuild(t, f, "cost")
	// Prove the observer catches hidden correlated scans even when the outer
	// query returns a single row (and Rows.Next alone would pass).
	f.index.reset(true)
	rows, e := f.owner.library.index.Query("SELECT (SELECT sum(length(b.payload)) FROM items b WHERE b.version>=a.version) FROM items a LIMIT 1")
	if e != nil {
		t.Fatal(e)
	}
	for rows.Next() {
		var n int
		if e = rows.Scan(&n); e != nil {
			t.Fatal(e)
		}
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	if e = rows.Close(); e != nil {
		t.Fatal(e)
	}
	observed := false
	for _, w := range f.index.work {
		if w.rows == 1 && w.steps > 1000 && w.scans > 0 {
			observed = true
		}
	}
	if !observed {
		t.Fatal("correlated work is unobservable", f.index.work)
	}
	counts := map[bool]int{}
	for _, q := range libraryProfileShapes() {
		for _, limit := range []uint64{1, 16, 128} {
			p := lib.Page{Limit: limit}
			libraryProfilePlan(t, f, q, p, nil)
			r, c := libraryProfileRequest(t, f, q, p)
			if counts[false] != 0 && counts[false] != c.statements {
				t.Fatal("nonconstant first SQL", c)
			}
			counts[false] = c.statements
			if r.Next != "" {
				p.Cursor = r.Next
				p.ExpectedSnapshot = r.Snapshot
				_, c = libraryProfileRequest(t, f, q, p)
				if counts[true] != 0 && counts[true] != c.statements {
					t.Fatal("nonconstant continuation SQL", c)
				}
				counts[true] = c.statements
			}
		}
	}
	f.source.reset(true)
	f.index.reset(true)
	for _, call := range []func() error{
		func() error {
			_, e := f.service.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 1}, lib.ReadScope{})
			return e
		},
		func() error { _, e := f.service.ListLibrary(t.Context(), lib.Query{}, lib.Page{}, f.scope); return e },
		func() error {
			_, e := f.service.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 129}, f.scope)
			return e
		},
		func() error {
			p := "a"
			_, e := f.service.ListLibrary(t.Context(), lib.Query{Search: &p}, lib.Page{Limit: 1}, f.scope)
			return e
		},
	} {
		if call() == nil {
			t.Fatal("preflight accepted")
		}
	}
	if len(f.source.trace)+len(f.index.trace) != 0 {
		t.Fatal("preflight SQL")
	}
	t.Logf("D7 trace source=%v index=%v counts=%v; correlated-scan observer positive control passed", f.source.trace, f.index.trace, counts)
}

func TestLibraryProfileGate(t *testing.T) {
	for _, n := range []int{1000, 10000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) { libraryProfileMatrix(t, libraryProfileNew(t, n)) })
	}
	// The 100k leg is owned by TestLibraryMilestone, so the large matrix runs
	// once in the required five-test command, never as a repeated/race workload.
	t.Run("max-version-byte-short", func(t *testing.T) {
		o := catalogQueryOwner(t)
		libraryUnmanaged(t, o, 1, 1, 1024, strings.Repeat("\"", 4096))
		f := &libraryProfileFixture{owner: o, service: lib.NewService(o), scope: o.LibraryReadScope(), source: libraryProfileSource(t, o)}
		for i := 1; i <= 1024; i++ {
			f.truth = append(f.truth, libraryTruth{item: lib.Item{ContentID: catalogSchemaID("cnt", 1), VersionID: catalogSchemaID("ver", i)}})
		}
		libraryProfileBuild(t, f, "max-versions")
		q := lib.Query{Versions: lib.All, Sort: lib.IdentityAsc}
		want := libraryProfileOracle(f.truth, q)
		positions := libraryProfilePositions(t, f, q, 128, want)
		r, _ := libraryProfileRequest(t, f, q, lib.Page{Limit: 128})
		if r.Complete || len(r.Items) >= 128 || r.EncodedBytes > lib.MaxResponseBytes {
			t.Fatal("not byte-short", len(r.Items), r.EncodedBytes)
		}
		for _, pos := range positions {
			libraryProfileSamples(t, f, q, pos.page, "byte-short-"+pos.name, want, pos.offset)
		}
	})
	libraryProfileMaxEvidence(t)
}

var _ driver.Rows = (*libraryProfileRows)(nil)

func TestLibraryProfilePartitionAccounting(t *testing.T) {
	// The byte cap includes escaping, UTF-8, a varying cursor envelope and the
	// decimal size field. Check maximality by independently encoding the result
	// and its next larger prefix, rather than reproducing the size algorithm.
	for _, text := range []string{strings.Repeat("\"", 4096), strings.Repeat("\\", 4096), strings.Repeat("é", 2048)} {
		items := make([]lib.Item, 129)
		for i := range items {
			items[i] = lib.Item{ContentID: catalogSchemaID("cnt", 1), VersionID: catalogSchemaID("ver", i+1), AudioID: 123, Hash: strings.Repeat("1", 40), ContentRevision: 1, Baseline: lib.Baseline{Title: text}, Media: catalog.CatalogOnly}
		}
		cursor := func(item lib.Item) string { return item.VersionID + "\"\u2028\\" }
		p := lib.Page{Limit: 128}
		r, e := libraryPartition(items, p, strings.Repeat("A", 43), cursor)
		if e != nil {
			t.Fatal(e)
		}
		raw, e := lib.CanonicalJSON(r)
		if e != nil || uint64(len(raw)) != r.EncodedBytes || r.EncodedBytes > lib.MaxResponseBytes {
			t.Fatal("canonical bytes", len(raw), r.EncodedBytes, e)
		}
		if len(r.Items) == 0 || len(r.Items) > 128 || r.Inspected != 129 || r.Complete || r.Next != cursor(r.Items[len(r.Items)-1]) {
			t.Fatal("partition envelope", r)
		}
		if len(r.Items) < 128 {
			larger, e := lib.Account(lib.Result{Items: items[:len(r.Items)+1], Snapshot: r.Snapshot, Next: cursor(items[len(r.Items)]), Inspected: 129})
			if e != nil || larger.EncodedBytes <= lib.MaxResponseBytes {
				t.Fatal("prefix was not maximal", larger.EncodedBytes, e)
			}
		}
	}
}

func TestLibraryProfileForeignKeyLease(t *testing.T) {
	countChecks := func(trace *libraryTrace) int {
		count := 0
		for _, q := range trace.sql {
			if strings.Contains(q, "pragma_foreign_key_check") {
				count++
			}
		}
		return count
	}
	t.Run("multi-content-advance", func(t *testing.T) {
		o := catalogQueryOwner(t)
		for i := 1; i <= 130; i++ {
			libraryUnmanaged(t, o, i, i, 1, "small Content")
		}
		trace := libraryTraceSource(t, o)
		s := lib.NewService(o)
		scope := o.LibraryReadScope()
		if _, e := s.Prepare(t.Context(), scope); e != nil {
			t.Fatal(e)
		}
		var previous uint64
		for advance := 0; advance < 2; advance++ {
			trace.reset()
			p, e := s.Advance(t.Context(), scope)
			if e != nil {
				t.Fatal(e)
			}
			if p.Contents-previous < 2 || countChecks(trace) != 1 {
				t.Fatal("global FK check repeated per Content", p.Contents-previous, countChecks(trace), trace.sql)
			}
			t.Logf("advance=%d Contents=%d global_FK_checks=%d", advance, p.Contents-previous, countChecks(trace))
			previous = p.Contents
			if p.Ready != (advance == 1) {
				t.Fatal("unexpected build progress", p)
			}
		}
	})
	t.Run("fresh-transaction-and-schema-cookie", func(t *testing.T) {
		o := catalogQueryOwner(t)
		libraryUnmanaged(t, o, 1, 1, 1, "first")
		libraryUnmanaged(t, o, 2, 2, 1, "second")
		trace := libraryTraceSource(t, o)
		for lease := 0; lease < 2; lease++ {
			trace.reset()
			checked := false
			e := o.WithinCatalogTransaction(t.Context(), func(repository catalog.CatalogRepository) error {
				r := repository.(*catalogRepository)
				for _, id := range []int{1, 2} {
					if _, _, _, e := r.catalogContent(t.Context(), mustProfileContent(id)); e != nil {
						return e
					}
				}
				if countChecks(trace) != 1 {
					t.Fatal("unchanged lease did not reuse FK result", trace.sql)
				}
				if _, e := r.transaction.ExecContext(t.Context(), "PRAGMA query_only=OFF"); e != nil {
					return e
				}
				var before, after int64
				if e := r.transaction.QueryRow("SELECT total_changes()").Scan(&before); e != nil {
					return e
				}
				if _, e := r.transaction.ExecContext(t.Context(), "CREATE TABLE tc_library_profile_fk_probe(value INTEGER)"); e != nil {
					return e
				}
				if e := r.transaction.QueryRow("SELECT total_changes()").Scan(&after); e != nil {
					return e
				}
				if before != after {
					t.Fatal("DDL changed total_changes; test would not isolate schema invalidation")
				}
				if _, _, _, e := r.catalogContent(t.Context(), mustProfileContent(1)); e != nil {
					return e
				}
				if countChecks(trace) != 2 {
					t.Fatal("DDL did not force a fresh database-wide FK check", trace.sql)
				}
				if _, e := r.transaction.ExecContext(t.Context(), "UPDATE tc_catalog_content SET title=title WHERE content_id=?", catalogSchemaID("cnt", 1)); e != nil {
					return e
				}
				if _, _, _, e := r.catalogContent(t.Context(), mustProfileContent(1)); e != nil {
					return e
				}
				if countChecks(trace) != 3 {
					t.Fatal("write did not force a fresh database-wide FK check", trace.sql)
				}
				checked = true
				return fmt.Errorf("rollback schema probe")
			})
			if e == nil || !checked {
				t.Fatal("schema probe did not complete and roll back", e)
			}
		}
	})
}
