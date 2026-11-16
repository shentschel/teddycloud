package sqlite

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	catalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	lib "github.com/shentschel/teddycloud/next/backend/internal/application/library"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

type libraryTruth struct {
	item lib.Item
	key  string
}

// Real registration/review commands establish searchable titles. Expected keys
// and identities are fixture facts, independent of the production normalizer,
// comparator, range encoder, projection and SQL insertion order.
func librarySearchFixture(t *testing.T, permutation []int) (*LifecycleOwner, []libraryTruth) {
	t.Helper()
	f := newRegistrationFixture(t)
	titles := []string{" Beta ", "  ALPHA  ", "alpha", "alphabet", "Ä", "ä", "é", "e\u0301", "%_\\literal", "   ", "conflicted"}
	keys := []string{"beta", "alpha", "alpha", "alphabet", "Ä", "ä", "é", "e\u0301", "%_\\literal", "", ""}
	if permutation == nil {
		for i := range titles {
			permutation = append(permutation, i)
		}
	}
	var truth []libraryTruth
	for _, i := range permutation {
		subject, _ := domain.ContentSubject(catalogQueryContent(t, i+1))
		claim, err := domain.TitleClaim(titles[i])
		if err != nil {
			t.Fatal(err)
		}
		selected := f.observation(t, subject, claim)
		var dissent domain.CatalogObservation
		observations := []domain.CatalogObservation{selected}
		if i == 10 {
			other, _ := domain.TitleClaim("different alternative")
			dissent = f.observation(t, subject, other)
			observations = append(observations, dissent)
		}
		f.command = catalog.RegistrationCommand{ID: registrationIntegrationCommandID(t), Subject: subject, ExpectedKind: domain.CatalogExpectedAbsent, Observations: observations, Selections: []domain.CatalogRegistrationSelection{{Key: domain.TitleClaimKey, Observation: selected.ID()}}, Reason: "search fixture"}
		if _, err := f.service.RegisterContent(t.Context(), f.command); err != nil {
			t.Fatal(err)
		}
		for n := 1; n <= 3; n++ {
			if _, err := f.service.RegisterCatalogVersion(t.Context(), f.version(t, (i+1)*10+n, uint64(n))); err != nil {
				t.Fatal(err)
			}
			truth = append(truth, libraryTruth{item: lib.Item{ContentID: subject.ContentID().String(), VersionID: catalogSchemaID("ver", (i+1)*10+n)}, key: keys[i]})
		}
		if i == 10 {
			var review catalog.ReviewSnapshot
			if err := f.owner.WithinCatalogTransaction(t.Context(), func(r catalog.CatalogRepository) error {
				var e error
				review, _, e = r.LoadReview(t.Context(), subject)
				return e
			}); err != nil {
				t.Fatal(err)
			}
			command := catalog.ReviewCommand{ID: registrationIntegrationCommandID(t), Subject: subject, ExpectedAggregate: review.AggregateRevision, ExpectedReview: review.State.Revision(), Key: domain.TitleClaimKey, Support: []evidence.ID{dissent.ID()}, Reason: "conflict stays missing"}
			if _, err := f.base.AcceptSupport(t.Context(), command); err != nil {
				t.Fatal(err)
			}
		}
	}
	libraryUnmanaged(t, f.owner, 90, 900, 2, "ALPHA baseline is not accepted")
	for n := 900; n < 902; n++ {
		truth = append(truth, libraryTruth{item: lib.Item{ContentID: catalogSchemaID("cnt", 90), VersionID: catalogSchemaID("ver", n)}})
	}
	return f.owner, truth
}
func libraryOracle(truth []libraryTruth, q lib.Query) []string {
	var selected []libraryTruth
	for _, r := range truth {
		if q.OwnerContentID != nil && r.item.ContentID != q.OwnerContentID.String() || q.Versions == lib.Current && r.item.ContentID == catalogSchemaID("cnt", 90) || q.Search != nil && (r.key == "" || !strings.HasPrefix(r.key, *q.Search)) {
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
	out := []string{}
	for _, r := range selected {
		out = append(out, r.item.ContentID+r.item.VersionID)
	}
	return out
}
func libraryCollect(t *testing.T, s *lib.Service, scope lib.ReadScope, q lib.Query, limit uint64) ([]string, []lib.Result) {
	t.Helper()
	p := lib.Page{Limit: limit}
	out := []string{}
	var pages []lib.Result
	for i := 0; i < 1000; i++ {
		r, e := s.ListLibrary(t.Context(), q, p, scope)
		if e != nil {
			t.Fatal(q, p, e)
		}
		pages = append(pages, r)
		if r.Inspected > limit+1 {
			t.Fatal("lookahead", r.Inspected)
		}
		for _, item := range r.Items {
			out = append(out, item.ContentID+item.VersionID)
		}
		if r.Complete {
			return out, pages
		}
		p.Cursor = r.Next
		p.ExpectedSnapshot = r.Snapshot
	}
	t.Fatal("unbounded pages")
	return nil, nil
}
func libraryShapes() []lib.Query {
	owner := func(n int) *identity.ContentID { v, _ := identity.ParseContentID(catalogSchemaID("cnt", n)); return &v }
	var out []lib.Query
	for _, lane := range []string{lib.All, lib.Current} {
		for _, sort := range []string{lib.IdentityAsc, lib.TitleAsc, lib.TitleDesc} {
			for _, o := range []*identity.ContentID{nil, owner(1), owner(2), owner(90), owner(999)} {
				out = append(out, lib.Query{OwnerContentID: o, Versions: lane, Sort: sort})
				if sort != lib.IdentityAsc {
					for _, s := range []string{"a", "alpha", "alphabet", "%_\\", "%", "_", "\\", "Ä", "ä", "é", "e\u0301", "absent", "conflicted", "ALPHA baseline"} {
						v := s
						out = append(out, lib.Query{OwnerContentID: o, Versions: lane, Sort: sort, Search: &v})
					}
				}
			}
		}
	}
	return out
}
func TestLibraryTotalOrder(t *testing.T) {
	for _, permutation := range [][]int{nil, {10, 9, 8, 7, 6, 5, 4, 3, 2, 1, 0}, {3, 7, 1, 10, 2, 4, 0, 9, 5, 8, 6}} {
		o, truth := librarySearchFixture(t, permutation)
		s := lib.NewService(o)
		scope := o.LibraryReadScope()
		libraryReady(t, o, s, scope)
		for _, order := range []string{lib.IdentityAsc, lib.TitleAsc, lib.TitleDesc} {
			q := lib.Query{Versions: lib.All, Sort: order}
			got, _ := libraryCollect(t, s, scope, q, 1)
			if !reflect.DeepEqual(got, libraryOracle(truth, q)) {
				t.Fatal(order, got, libraryOracle(truth, q))
			}
		}
		// Reinsert membership rows in reverse physical rowid order, keeping all
		// immutable keys/payloads. Results must remain byte-for-byte identical.
		if _, err := o.library.index.Exec(`CREATE TEMP TABLE reversed AS SELECT * FROM memberships ORDER BY rowid DESC; DELETE FROM memberships; INSERT INTO memberships SELECT * FROM reversed; DROP TABLE reversed`); err != nil {
			t.Fatal(err)
		}
		for _, order := range []string{lib.TitleAsc, lib.TitleDesc} {
			q := lib.Query{Versions: lib.All, Sort: order}
			got, _ := libraryCollect(t, s, scope, q, 16)
			if !reflect.DeepEqual(got, libraryOracle(truth, q)) {
				t.Fatal("rowid dependence", order)
			}
		}
	}
}
func TestLibraryFilterPagination(t *testing.T) {
	o, truth := librarySearchFixture(t, nil)
	s := lib.NewService(o)
	scope := o.LibraryReadScope()
	libraryReady(t, o, s, scope)
	before := catalogRecoveryRows(t, o.database.db)
	for _, q := range libraryShapes() {
		for limit := uint64(1); limit <= lib.MaxItems; limit++ {
			got, _ := libraryCollect(t, s, scope, q, limit)
			if !reflect.DeepEqual(got, libraryOracle(truth, q)) {
				t.Fatal(q, limit, got, libraryOracle(truth, q))
			}
		}
	}
	search := "  ALPHA  "
	q := lib.Query{Sort: lib.TitleDesc, Search: &search}
	first, e := s.ListLibrary(t.Context(), q, lib.Page{Limit: 1}, scope)
	if e != nil || first.Next == "" {
		t.Fatal(first, e)
	}
	p := lib.Page{Limit: 1, Cursor: first.Next, ExpectedSnapshot: first.Snapshot}
	a, e := s.ListLibrary(t.Context(), q, p, scope)
	search = "alpha"
	b, e2 := s.ListLibrary(t.Context(), q, p, scope)
	if e != nil || e2 != nil || !reflect.DeepEqual(a, b) {
		t.Fatal("normalization replay", e, e2)
	}
	owner := catalogQueryContent(t, 2)
	other := "a"
	for _, changed := range []lib.Query{{Sort: lib.TitleAsc, Search: &search}, {Sort: lib.TitleDesc, Search: &other}, {Sort: lib.TitleDesc, Search: &search, Versions: lib.Current}, {Sort: lib.TitleDesc, Search: &search, OwnerContentID: &owner}} {
		if r, e := s.ListLibrary(t.Context(), changed, p, scope); e != lib.ErrStaleCursor || len(r.Items) != 0 {
			t.Fatal(changed, r, e)
		}
	}
	if _, e := s.ListLibrary(t.Context(), lib.Query{Search: &search}, lib.Page{Limit: 1}, scope); e != lib.ErrUnsupported {
		t.Fatal(e)
	}
	// The unchanged A wire codec cannot revive a cursor after a B index rebuild.
	oldSnapshot := first.Snapshot
	libraryReady(t, o, s, scope)
	if oldSnapshot == librarySnapshot(o.library) {
		t.Fatal("generation reused")
	}
	if _, e := s.ListLibrary(t.Context(), q, p, scope); e != lib.ErrStaleCursor {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, catalogRecoveryRows(t, o.database.db)) {
		t.Fatal("source changed")
	}
}
func TestLibraryOwnershipVisibility(t *testing.T) {
	f := newRegistrationIntegrationFixture(t)
	o := f.owner
	libraryUnmanaged(t, o, 90, 900, 1, "Public A")
	s := lib.NewService(o)
	scope := o.LibraryReadScope()
	libraryReady(t, o, s, scope)
	for _, lane := range []string{lib.All, lib.Current} {
		for _, owner := range []*identity.ContentID{nil, ptrContent(f.contents[0].ContentID()), ptrContent(f.contents[1].ContentID()), ptrContent(catalogQueryContent(t, 90)), ptrContent(catalogQueryContent(t, 999))} {
			q := lib.Query{Versions: lane, Sort: lib.TitleDesc, OwnerContentID: owner}
			got, _ := libraryCollect(t, s, scope, q, 1)
			want := 0
			for _, v := range f.versions {
				if owner == nil || v.ContentID() == *owner {
					want++
				}
			}
			if lane == lib.All && (owner == nil || *owner == catalogQueryContent(t, 90)) {
				want++
			}
			if len(got) != want {
				t.Fatal("custom/public changed structural ownership", q, len(got), want)
			}
		}
	}
	for _, v := range []string{"mine", "shared"} {
		if _, e := s.ListLibrary(t.Context(), lib.Query{Versions: v}, lib.Page{Limit: 1}, scope); e != lib.ErrUnsupported {
			t.Fatal(v, e)
		}
	}
	for _, q := range []lib.Query{{Sort: lib.TitleAsc}, {Versions: lib.Current}, {OwnerContentID: ptrContent(f.contents[1].ContentID())}} {
		if _, e := s.ListLibrary(t.Context(), q, lib.Page{}, lib.ReadScope{}); e != lib.ErrUnauthorized {
			t.Fatal(e)
		}
	}
	// Current includes the validated terminal of a chain and excludes its old
	// predecessor without discarding either from all.
	sf := newSupersessionFixture(t)
	if _, e := sf.sup.SupersedeVersion(t.Context(), sf.command); e != nil {
		t.Fatal(e)
	}
	ss := lib.NewService(sf.owner)
	sc := sf.owner.LibraryReadScope()
	libraryReady(t, sf.owner, ss, sc)
	owner := sf.registrationFixture.command.Subject.ContentID()
	for _, lane := range []string{lib.All, lib.Current} {
		r, e := ss.ListLibrary(t.Context(), lib.Query{Versions: lane, Sort: lib.TitleAsc, OwnerContentID: &owner}, lib.Page{Limit: 128}, sc)
		want := 2
		if lane == lib.Current {
			want = 1
		}
		if e != nil || len(r.Items) != want {
			t.Fatal(r, e)
		}
		if lane == lib.Current && r.Items[0].Supersession.Status != catalog.Current {
			t.Fatal(r)
		}
	}
}
func ptrContent(v identity.ContentID) *identity.ContentID { return &v }

func TestLibrarySearchPlan(t *testing.T) {
	o, _ := librarySearchFixture(t, nil)
	source := libraryTraceSource(t, o)
	s := lib.NewService(o)
	scope := o.LibraryReadScope()
	libraryReady(t, o, s, scope)
	index := libraryTraceIndex(t, o)
	var memberships, items, maxMembership int
	if e := o.library.index.QueryRow("SELECT count(*) FROM memberships").Scan(&memberships); e != nil {
		t.Fatal(e)
	}
	if e := o.library.index.QueryRow("SELECT count(*) FROM items").Scan(&items); e != nil {
		t.Fatal(e)
	}
	if e := o.library.index.QueryRow("SELECT max(n) FROM (SELECT count(*) n FROM memberships GROUP BY identity_key)").Scan(&maxMembership); e != nil {
		t.Fatal(e)
	}
	if memberships > 4*items || maxMembership > 4 {
		t.Fatal(memberships, items, maxMembership)
	}
	counts := map[bool]int{}
	for _, q := range libraryShapes() {
		for _, limit := range []uint64{1, 16, 128} {
			statement, args := libraryRange(q, limit, nil)
			rows, e := o.library.index.Query("EXPLAIN QUERY PLAN "+statement, args...)
			if e != nil {
				t.Fatal(e)
			}
			plan := ""
			for rows.Next() {
				var a, b, c int
				var detail string
				if e := rows.Scan(&a, &b, &c, &detail); e != nil {
					t.Fatal(e)
				}
				plan += detail
			}
			if e := rows.Err(); e != nil {
				t.Fatal(e)
			}
			rows.Close()
			if !strings.Contains(plan, "SEARCH memberships USING COVERING INDEX library_") || !strings.Contains(plan, ">?") || strings.Contains(plan, "TEMP B-TREE") {
				t.Fatal(q, plan)
			}
			source.reset()
			index.reset()
			r, e := s.ListLibrary(t.Context(), q, lib.Page{Limit: limit}, scope)
			if e != nil {
				t.Fatal(q, e)
			}
			check := func(continuation bool, result lib.Result) {
				t.Helper()
				count := len(source.sql) + len(index.sql)
				if count > 12 || counts[continuation] != 0 && counts[continuation] != count {
					t.Fatal("SQL count", continuation, count, source.sql, index.sql)
				}
				counts[continuation] = count
				scalar := 0
				for _, n := range source.visits {
					scalar += n
				}
				if scalar > 8 {
					t.Fatal("scalar metadata row bound", scalar)
				}
				candidates, identities := 0, 0
				for sql, n := range index.visits {
					if strings.Contains(sql, "FROM memberships") {
						candidates += n
					}
					if strings.Contains(sql, "FROM items WHERE content=") {
						identities += n
					}
				}
				if uint64(candidates) != result.Inspected || candidates > int(limit+1) || identities > 1 || continuation && identities != 1 {
					t.Fatal("bounded lookahead/lookup", candidates, identities, result.Inspected)
				}
				for _, sql := range append(append([]string{}, source.sql...), index.sql...) {
					if strings.Contains(sql, "tc_catalog_content") || strings.Contains(sql, "tc_content_versions") || strings.Contains(sql, "OFFSET") {
						t.Fatal("source scan or OFFSET", sql)
					}
				}
			}
			check(false, r)
			if r.Next != "" {
				source.reset()
				index.reset()
				next, e := s.ListLibrary(t.Context(), q, lib.Page{Limit: limit, Cursor: r.Next, ExpectedSnapshot: r.Snapshot}, scope)
				if e != nil {
					t.Fatal(e)
				}
				check(true, next)
			}
		}
	}
	source.reset()
	index.reset()
	bad := "a"
	for _, q := range []lib.Query{{Search: &bad}, {Versions: "mine"}, {Sort: "wrong"}} {
		if _, e := s.ListLibrary(t.Context(), q, lib.Page{Limit: 1}, scope); e == nil {
			t.Fatal(q)
		}
	}
	if _, e := s.ListLibrary(t.Context(), lib.Query{Sort: lib.TitleAsc}, lib.Page{Limit: 1}, lib.ReadScope{}); e != lib.ErrUnauthorized {
		t.Fatal(e)
	}
	if len(source.sql)+len(index.sql) != 0 {
		t.Fatal("preflight SQL")
	}
	t.Logf("ready statements first=%d continuation=%d; candidates <= Limit+1; exact identity <=1; memberships=%d/%d", counts[false], counts[true], memberships, items)
}

// Storage-free deterministic fuzzing exercises normalization and the real
// authenticated codec, including noncanonical and adversarial token bytes.
func FuzzLibraryQueryAndCursor(f *testing.F) {
	for _, s := range []string{"  WOOZLE  Test ", "%_\\", "é", "e\u0301", "\xff", strings.Repeat("a", 257), ""} {
		f.Add(s, uint8(1), uint8(0), "bad")
	}
	f.Fuzz(func(t *testing.T, text string, limitByte, shape uint8, token string) {
		var secret, snapshot, id, incarnation [32]byte
		for i := range secret {
			secret[i] = byte(i)
			snapshot[i] = byte(i + 32)
		}
		orders := []string{lib.IdentityAsc, lib.TitleAsc, lib.TitleDesc, "wrong"}
		q := lib.Query{Sort: orders[shape%4], Search: &text}
		p := lib.Page{Limit: uint64(limitByte)}
		a, e := lib.NormalizeQuery(q, p)
		b, e2 := lib.NormalizeQuery(q, p)
		if e != e2 || !reflect.DeepEqual(a, b) {
			t.Fatal("query nondeterminism")
		}
		if e == nil {
			c, e := lib.NormalizeQuery(a, p)
			if e != nil || !reflect.DeepEqual(a, c) {
				t.Fatal("normalization not idempotent")
			}
			d := libraryDigest(lib.Profile, a, p.Limit, id, 0, incarnation)
			good := libraryEncodeCursor(secret, snapshot, d, catalogSchemaID("cnt", 1), catalogSchemaID("ver", 1))
			position, e := libraryDecodeCursor(good, secret, d)
			if e != nil || position.content != catalogSchemaID("cnt", 1) {
				t.Fatal(e)
			}
			pa, ea := libraryDecodeCursor(token, secret, d)
			pb, eb := libraryDecodeCursor(token, secret, d)
			if ea != eb || pa != pb {
				t.Fatal("codec nondeterminism")
			}
		}
	})
}

func TestLibrarySearchTimingBaseline(t *testing.T) {
	fixtureStart := time.Now()
	o, _ := librarySearchFixture(t, nil)
	fixture := time.Since(fixtureStart)
	s := lib.NewService(o)
	scope := o.LibraryReadScope()
	build := func(label string) {
		start := time.Now()
		p, e := s.Prepare(t.Context(), scope)
		if e != nil {
			t.Fatal(e)
		}
		t.Logf("%s prepare=%s", label, time.Since(start))
		for n := 0; !p.Ready; n++ {
			start = time.Now()
			p, e = s.Advance(t.Context(), scope)
			if e != nil {
				t.Fatal(e)
			}
			t.Logf("%s advance=%d duration=%s items=%d staging=%d pages=%d", label, n, time.Since(start), p.Items, p.Bytes, p.Pages)
		}
	}
	build("cold")
	var sqliteVersion string
	if e := o.library.index.QueryRow("SELECT sqlite_version()").Scan(&sqliteVersion); e != nil {
		t.Fatal(e)
	}
	t.Logf("B baseline runner=%s/%s go=%s sqlite=%s layout=%s items=35 contents=12 fixture=%s; software baseline, not T acceptance", runtime.GOOS, runtime.GOARCH, runtime.Version(), sqliteVersion, libraryIndexLayout, fixture)
	for _, order := range []string{lib.TitleAsc, lib.TitleDesc} {
		for _, lane := range []string{lib.All, lib.Current} {
			for _, prefix := range []string{"", "a", "absent", "%_\\"} {
				q := lib.Query{Versions: lane, Sort: order}
				if prefix != "" {
					q.Search = &prefix
				}
				for _, continuation := range []bool{false, true} {
					page := lib.Page{Limit: 16}
					if continuation {
						first, e := s.ListLibrary(t.Context(), q, page, scope)
						if e != nil {
							t.Fatal(e)
						}
						if first.Next == "" {
							continue
						}
						page.Cursor = first.Next
						page.ExpectedSnapshot = first.Snapshot
					}
					var samples []time.Duration
					warmup := time.Now()
					for n := 0; n < 35; n++ {
						start := time.Now()
						if _, e := s.ListLibrary(t.Context(), q, page, scope); e != nil {
							t.Fatal(e)
						}
						elapsed := time.Since(start)
						if n == 4 {
							t.Logf("warmup lane=%s order=%s prefix=%q continuation=%v count=5 elapsed=%s", lane, order, prefix, continuation, time.Since(warmup))
						}
						if n >= 5 {
							samples = append(samples, elapsed)
						}
					}
					sorted := append([]time.Duration(nil), samples...)
					sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
					t.Logf("ready lane=%s order=%s prefix=%q continuation=%v n=30 raw=%v p50=%s p95=%s max=%s", lane, order, prefix, continuation, samples, sorted[14], sorted[28], sorted[29])
				}
			}
		}
	}
	catalogSchemaExec(t, o.database.db, "UPDATE tc_catalog_content SET title=title WHERE content_id=?", catalogSchemaID("cnt", 90))
	build("refresh after write")
}

func TestLibraryLifecycleFenceBOptions(t *testing.T) {
	f := newSupersessionFixture(t)
	o := f.owner
	s := lib.NewService(o)
	scope := o.LibraryReadScope()
	libraryReady(t, o, s, scope)
	owner := f.registrationFixture.command.Subject.ContentID()
	prefix := "registered"
	q := lib.Query{OwnerContentID: &owner, Versions: lib.Current, Sort: lib.TitleDesc, Search: &prefix}
	first, e := s.ListLibrary(t.Context(), q, lib.Page{Limit: 1}, scope)
	if e != nil || first.Next == "" {
		t.Fatal(first, e)
	}
	p := lib.Page{Limit: 1, Cursor: first.Next, ExpectedSnapshot: first.Snapshot}
	if _, e := s.ListLibrary(t.Context(), q, p, scope); e != nil {
		t.Fatal(e)
	}
	backup, e := o.database.CreateBackup(t.Context(), filepath.Join(t.TempDir(), "b-backup.sqlite"), SchemaMigrations())
	if e != nil {
		t.Fatal(e)
	}
	if e := o.Restore(t.Context(), backup, filepath.Join(t.TempDir(), "b-restored.sqlite")); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ListLibrary(t.Context(), q, p, scope); e != lib.ErrUnauthorized {
		t.Fatal(e)
	}
	scope = o.LibraryReadScope()
	libraryReady(t, o, s, scope)
	if _, e := s.ListLibrary(t.Context(), q, p, scope); e != lib.ErrStaleCursor {
		t.Fatal("restore revived B token", e)
	}
	fresh, e := s.ListLibrary(t.Context(), q, lib.Page{Limit: 1}, scope)
	if e != nil {
		t.Fatal(e)
	}
	p = lib.Page{Limit: 1, Cursor: fresh.Next, ExpectedSnapshot: fresh.Snapshot}
	path := o.config.Path
	if e := o.Close(t.Context()); e != nil {
		t.Fatal(e)
	}
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(p)
	query := []byte(owner.String())
	command := exec.CommandContext(t.Context(), executable, "-test.run=^TestLibraryLifecycleFenceBProcessHelper$", "-test.v")
	command.Env = append(os.Environ(), "TC_LIBRARY_B_PATH="+path, "TC_LIBRARY_B_PAGE="+string(raw), "TC_LIBRARY_B_QUERY="+string(query))
	output, e := command.CombinedOutput()
	if e != nil || !bytes.Contains(output, []byte("B restart checked")) {
		t.Fatal(e, string(output))
	}
}
func TestLibraryLifecycleFenceBProcessHelper(t *testing.T) {
	path := os.Getenv("TC_LIBRARY_B_PATH")
	if path == "" {
		return
	}
	var p lib.Page
	var q lib.Query
	owner, err := identity.ParseContentID(os.Getenv("TC_LIBRARY_B_QUERY"))
	prefix := "registered"
	q = lib.Query{OwnerContentID: &owner, Versions: lib.Current, Sort: lib.TitleDesc, Search: &prefix}
	if json.Unmarshal([]byte(os.Getenv("TC_LIBRARY_B_PAGE")), &p) != nil || err != nil {
		t.Fatal("bad process input")
	}
	o, e := OpenLifecycleOwner(t.Context(), Config{Path: path}, SchemaMigrations())
	if e != nil {
		t.Fatal(e)
	}
	defer o.Close(context.Background())
	s := lib.NewService(o)
	scope := o.LibraryReadScope()
	for n := 0; n < 2; n++ {
		if n == 1 {
			libraryReady(t, o, s, scope)
		}
		if _, e := s.ListLibrary(t.Context(), q, p, scope); e != lib.ErrStaleCursor {
			t.Fatal(e)
		}
	}
	t.Log("B restart checked")
}

func TestLibrarySearchPayloadBounds(t *testing.T) {
	o, _ := librarySearchFixture(t, nil)
	service := lib.NewService(o)
	scope := o.LibraryReadScope()
	libraryReady(t, o, service, scope)
	items := libraryList(t, service, scope, 128)
	for _, item := range items {
		raw, e := libraryEncodeItem(item)
		if e != nil {
			t.Fatal(e)
		}
		got, e := libraryDecodeItem(raw, item.ContentID, item.VersionID)
		if e != nil || !reflect.DeepEqual(item, got) {
			t.Fatal("private payload changed projection", e)
		}
	}
	item := items[0]
	item.Baseline.Title = strings.Repeat("a", 4096)
	raw, e := libraryEncodeItem(item)
	if e != nil || raw[0] != 1 {
		t.Fatal("large payload not bounded-compressed", e)
	}
	got, e := libraryDecodeItem(raw, item.ContentID, item.VersionID)
	if e != nil || !reflect.DeepEqual(item, got) {
		t.Fatal(e)
	}
	for _, bad := range [][]byte{nil, {0}, {1, 2, 3}, append([]byte{0}, bytes.Repeat([]byte{' '}, 32768)...), {0, '{', '}'}} {
		if _, e := libraryDecodeItem(bad, item.ContentID, item.VersionID); e != lib.ErrUnavailable {
			t.Fatal(e)
		}
	}
	var bomb bytes.Buffer
	bomb.WriteByte(1)
	writer := libraryPayloadWriters.Get().(*zlib.Writer)
	writer.Reset(&bomb)
	if _, e := writer.Write(bytes.Repeat([]byte{'a'}, 32769)); e != nil {
		t.Fatal(e)
	}
	if e := writer.Close(); e != nil {
		t.Fatal(e)
	}
	libraryPayloadWriters.Put(writer)
	if _, e := libraryDecodeItem(bomb.Bytes(), item.ContentID, item.VersionID); e != lib.ErrUnavailable {
		t.Fatal("inflation cap", e)
	}
}

func TestLibraryEpochABABOptions(t *testing.T) {
	for _, order := range []string{lib.TitleAsc, lib.TitleDesc} {
		f := newSupersessionFixture(t)
		o := f.owner
		s := lib.NewService(o)
		scope := o.LibraryReadScope()
		libraryReady(t, o, s, scope)
		owner := f.registrationFixture.command.Subject.ContentID()
		prefix := "registered"
		q := lib.Query{OwnerContentID: &owner, Versions: lib.Current, Sort: order, Search: &prefix}
		first, e := s.ListLibrary(t.Context(), q, lib.Page{Limit: 1}, scope)
		if e != nil || first.Next == "" {
			t.Fatal(first, e)
		}
		p := lib.Page{Limit: 1, Cursor: first.Next, ExpectedSnapshot: first.Snapshot}
		var baseline string
		if e := o.database.db.QueryRow("SELECT title FROM tc_catalog_content WHERE content_id=?", owner.String()).Scan(&baseline); e != nil {
			t.Fatal(e)
		}
		catalogSchemaExec(t, o.database.db, "UPDATE tc_catalog_content SET title='temporary' WHERE content_id=?", owner.String())
		catalogSchemaExec(t, o.database.db, "UPDATE tc_catalog_content SET title=? WHERE content_id=?", baseline, owner.String())
		if _, e := s.ListLibrary(t.Context(), q, p, scope); e != lib.ErrStaleCursor {
			t.Fatal("B ABA", e)
		}
		libraryReady(t, o, s, scope)
		if _, e := s.ListLibrary(t.Context(), q, p, scope); e != lib.ErrStaleCursor {
			t.Fatal("B ABA revived", e)
		}
	}
}
