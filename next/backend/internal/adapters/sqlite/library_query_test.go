package sqlite

import (
	"bytes"
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	catalog "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	lib "github.com/shentschel/teddycloud/next/backend/internal/application/library"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
)

func libraryReady(t *testing.T, o *LifecycleOwner, s *lib.Service, scope lib.ReadScope) lib.Preparation {
	t.Helper()
	before := catalogRecoveryRows(t, o.database.db)
	p, err := s.Prepare(t.Context(), scope)
	if err != nil || p.Ready {
		t.Fatal(p, err)
	}
	for i := 0; !p.Ready; i++ {
		if i > 1000 {
			t.Fatal("unbounded build")
		}
		p, err = s.Advance(t.Context(), scope)
		if err != nil {
			t.Fatal("advance", p, err)
		}
	}
	if got := catalogRecoveryRows(t, o.database.db); !reflect.DeepEqual(before, got) {
		t.Fatal("prepare mutated source/schema/receipts")
	}
	return p
}
func libraryList(t *testing.T, s *lib.Service, scope lib.ReadScope, limit uint64) []lib.Item {
	t.Helper()
	p := lib.Page{Limit: limit}
	var all []lib.Item
	for i := 0; i < 10000; i++ {
		r, err := s.ListLibrary(t.Context(), lib.Query{}, p, scope)
		if err != nil {
			t.Fatal(err)
		}
		if r.Inspected > limit+1 || r.Complete != (r.Next == "") {
			t.Fatal(r)
		}
		all = append(all, r.Items...)
		if r.Complete {
			return all
		}
		p.Cursor = r.Next
		p.ExpectedSnapshot = r.Snapshot
	}
	t.Fatal("unbounded pagination")
	return nil
}
func libraryUnmanaged(t *testing.T, o *LifecycleOwner, content, first, count int, title string) {
	t.Helper()
	catalogSchemaExec(t, o.database.db, "INSERT INTO tc_catalog_content VALUES (?,?,NULL,NULL)", catalogSchemaID("cnt", content), title)
	tx, err := o.database.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for n := first; n < first+count; n++ {
		if _, err := tx.ExecContext(t.Context(), "INSERT INTO tc_content_versions VALUES (?,?,123,?,0,NULL,NULL)", catalogSchemaID("ver", n), catalogSchemaID("cnt", content), bytes.Repeat([]byte{1}, 20)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func TestLibraryInventoryIdentity(t *testing.T) {
	f := newSupersessionFixture(t)
	if _, err := f.sup.SupersedeVersion(t.Context(), f.command); err != nil {
		t.Fatal(err)
	}
	o := f.owner
	s := lib.NewService(o)
	scope := o.LibraryReadScope()
	libraryUnmanaged(t, o, 1, 1000, 130, "same title")
	libraryUnmanaged(t, o, 2, 2000, 2, "same title")
	libraryUnmanaged(t, o, 3, 300, 0, "zero versions")
	var oracle []lib.Item
	for _, id := range []int{1, 2, 3, 100} {
		r, err := catalogRead(t, o, catalog.Query{Kind: catalog.ListVersions, Content: catalogQueryContent(t, id), Page: catalog.PageRequest{Limit: 128}})
		if err != nil {
			t.Fatal(err)
		}
		// Derive expected immutable identity facts independently from retained rows;
		// the two-page old API is checked separately to preserve its detail contract.
		if id != 1 {
			for _, e := range r.Entries {
				oracle = append(oracle, libraryProject(e, r.Revision.Uint64()))
			}
		} else {
			for n := 1; n <= 130; n++ {
				oracle = append(oracle, lib.Item{ContentID: catalogSchemaID("cnt", 1), VersionID: catalogSchemaID("ver", 999+n)})
			}
		}
	}
	sort.Slice(oracle, func(i, j int) bool {
		return oracle[i].ContentID+oracle[i].VersionID < oracle[j].ContentID+oracle[j].VersionID
	})
	p := libraryReady(t, o, s, scope)
	if p.Items != 134 || p.Contents != 4 {
		t.Fatal(p)
	}
	before := catalogRecoveryRows(t, o.database.db)
	for _, limit := range []uint64{1, 16, 128} {
		got := libraryList(t, s, scope, limit)
		if len(got) != len(oracle) {
			t.Fatal(len(got), len(oracle))
		}
		for i, e := range got {
			if e.ContentID != oracle[i].ContentID || e.VersionID != oracle[i].VersionID || e.AudioID != 123 || e.ContentRevision == 0 || e.Media != catalog.CatalogOnly {
				t.Fatal(i, e)
			}
			if e.ContentID == catalogSchemaID("cnt", 100) && !reflect.DeepEqual(e, oracle[i]) {
				t.Fatal("managed summary", e, oracle[i])
			}
		}
	}
	if !reflect.DeepEqual(before, catalogRecoveryRows(t, o.database.db)) {
		t.Fatal("query changed retained source")
	}
}
func TestLibraryProjection(t *testing.T) {
	f := newRegistrationIntegrationFixture(t)
	o := f.owner
	s := lib.NewService(o)
	scope := o.LibraryReadScope()
	// No content store or media open is invoked by Library; the fixture's stored
	// relation/receipt are independently retained and compared in every phase.
	libraryUnmanaged(t, o, 900, 900, 1, "<baseline> é \" \\")
	check := func() {
		t.Helper()
		libraryReady(t, o, s, scope)
		before := catalogRecoveryRows(t, o.database.db)
		all := libraryList(t, s, scope, 128)
		for _, item := range all {
			var index = -1
			for i, c := range f.contents {
				if item.ContentID == c.ContentID().String() {
					index = i
				}
			}
			if index < 0 {
				if item.Title.Value != nil || item.Title.Status != domain.CatalogFactUnknown || item.Baseline.Title == "" {
					t.Fatal(item)
				}
				continue
			}
			record := f.readContent(t, index)
			if item.Title.Status != record.Title.Status || item.ProtectedCustom != (index == 1) || item.Review != record.Review || item.Media != catalog.CatalogOnly {
				t.Fatal(item, record)
			}
			if record.Title.Status == domain.CatalogFactAccepted {
				v, _ := record.Title.Value()
				if item.Title.Value == nil || *item.Title.Value != v.Text() {
					t.Fatal(item)
				}
			} else if item.Title.Value != nil {
				t.Fatal("conflict scalar winner", item)
			}
		}
		if !reflect.DeepEqual(before, catalogRecoveryRows(t, o.database.db)) {
			t.Fatal("projection changed detail/receipts")
		}
	}
	check()
	// Add contradictory accepted support using the real review service, inspect
	// conflict before resolving, then prove a resolution is independently visible.
	index := 1
	var review catalog.ReviewSnapshot
	if err := o.WithinCatalogTransaction(t.Context(), func(r catalog.CatalogRepository) error {
		var found bool
		var err error
		review, found, err = r.LoadReview(t.Context(), f.contents[index])
		if !found && err == nil {
			t.Fatal("review missing")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	command := catalog.ReviewCommand{ID: registrationIntegrationCommandID(t), Subject: f.contents[index], ExpectedAggregate: review.AggregateRevision, ExpectedReview: review.State.Revision(), Key: domain.TitleClaimKey, Support: []evidence.ID{f.dissent[index].ID()}, Reason: "explicit conflicting title support"}
	if _, err := f.operator.base.AcceptSupport(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	check()
	if err := o.WithinCatalogTransaction(t.Context(), func(r catalog.CatalogRepository) error {
		var err error
		review, _, err = r.LoadReview(t.Context(), f.contents[index])
		return err
	}); err != nil {
		t.Fatal(err)
	}
	command.ID = registrationIntegrationCommandID(t)
	command.ExpectedAggregate = review.AggregateRevision
	command.ExpectedReview = review.State.Revision()
	if _, err := f.operator.base.ResolveConflict(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	check()
	f.replays(t)
}
func TestLibraryPagePartition(t *testing.T) {
	o := catalogQueryOwner(t)
	s := lib.NewService(o)
	scope := o.LibraryReadScope()
	// Legal 4096-byte baseline containing escaping-heavy text makes real pages
	// byte-short. Equal titles/AudioIDs remain independent identities.
	libraryUnmanaged(t, o, 1, 1, 128, strings.Repeat("\"", 4096))
	libraryReady(t, o, s, scope)
	before := catalogRecoveryRows(t, o.database.db)
	r, err := s.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 128}, scope)
	if err != nil || r.Complete || len(r.Items) == 0 || len(r.Items) >= 128 || r.EncodedBytes > lib.MaxResponseBytes || r.Inspected != 128 {
		t.Fatal(len(r.Items), r, err)
	}
	raw, _ := lib.CanonicalJSON(r)
	if uint64(len(raw)) != r.EncodedBytes {
		t.Fatal("actual accounting")
	}
	p := lib.Page{Limit: 128, Cursor: r.Next, ExpectedSnapshot: r.Snapshot}
	next, err := s.ListLibrary(t.Context(), lib.Query{}, p, scope)
	if err != nil || next.Items[0].VersionID != catalogSchemaID("ver", len(r.Items)+1) {
		t.Fatal(next, err)
	}
	replay, err := s.ListLibrary(t.Context(), lib.Query{}, p, scope)
	if err != nil || !reflect.DeepEqual(next, replay) {
		t.Fatal("replay", err)
	}
	if all := libraryList(t, s, scope, 128); len(all) != 128 {
		t.Fatal(len(all))
	}
	if !reflect.DeepEqual(before, catalogRecoveryRows(t, o.database.db)) {
		t.Fatal("page mutated source")
	}
	for _, order := range []string{lib.TitleAsc, lib.TitleDesc} {
		owner := catalogQueryContent(t, 1)
		q := lib.Query{Sort: order, OwnerContentID: &owner}
		ids, pages := libraryCollect(t, s, scope, q, 128)
		if len(ids) != 128 || len(pages) < 2 || pages[0].Complete || pages[0].EncodedBytes > lib.MaxResponseBytes {
			t.Fatal("B byte-short page", order, len(ids), len(pages))
		}
		for _, page := range pages {
			raw, e := lib.CanonicalJSON(page)
			if e != nil || uint64(len(raw)) != page.EncodedBytes {
				t.Fatal("B accounting", e)
			}
		}
	}
	// Isolate exact accounting arithmetic with a nonempty 210-byte Next. These
	// synthetic summaries exercise defensive oversized-item rejection, not a
	// claim that a legal single D1 summary can reach 1 MiB.
	candidate := r.Items[0]
	candidate.Baseline.Title = ""
	cursor := func(lib.Item) string { return strings.Repeat("A", 210) }
	base, _ := libraryPartition([]lib.Item{candidate, candidate}, lib.Page{Limit: 1}, r.Snapshot, cursor)
	candidate.Baseline.Title = strings.Repeat("a", int(lib.MaxResponseBytes-base.EncodedBytes))
	for {
		measured, _ := lib.Account(lib.Result{Items: []lib.Item{candidate}, Snapshot: r.Snapshot, Next: cursor(candidate), Inspected: 2})
		delta := int64(lib.MaxResponseBytes) - int64(measured.EncodedBytes)
		if delta == 0 {
			break
		}
		candidate.Baseline.Title = strings.Repeat("a", len(candidate.Baseline.Title)+int(delta))
	}
	exact, err := libraryPartition([]lib.Item{candidate, candidate}, lib.Page{Limit: 1}, r.Snapshot, cursor)
	if err != nil || exact.EncodedBytes != lib.MaxResponseBytes {
		t.Fatal(exact.EncodedBytes, err)
	}
	candidate.Baseline.Title += "a"
	if got, err := libraryPartition([]lib.Item{candidate, candidate}, lib.Page{Limit: 1}, r.Snapshot, cursor); err != lib.ErrLimitExceeded || len(got.Items) != 0 || got.Next != "" {
		t.Fatal(got, err)
	}
}
func TestLibraryPagePartitionEmpty(t *testing.T) {
	o := catalogQueryOwner(t)
	s := lib.NewService(o)
	scope := o.LibraryReadScope()
	libraryReady(t, o, s, scope)
	r, err := s.ListLibrary(context.Background(), lib.Query{}, lib.Page{Limit: 1}, scope)
	if err != nil || !r.Complete || r.Next != "" || r.Items == nil || r.Inspected != 0 {
		t.Fatal(r, err)
	}
}

func TestLibraryInventoryIdentityBeyondResolution(t *testing.T) {
	o := catalogQueryOwner(t)
	libraryUnmanaged(t, o, 1, 1, 1024, "same")
	libraryUnmanaged(t, o, 2, 2000, 1024, "same")
	s := lib.NewService(o)
	scope := o.LibraryReadScope()
	p := libraryReady(t, o, s, scope)
	if p.Items != 2048 {
		t.Fatal(p)
	}
	all := libraryList(t, s, scope, 128)
	if len(all) != 2048 {
		t.Fatal(len(all))
	}
	// Existing ambiguity admission is unchanged and is never the page engine.
	if _, err := catalogRead(t, o, catalog.Query{Kind: catalog.FindVersionsByAudioID, AudioID: func() domain.AudioID { a, _ := domain.NewAudioID(123); return a }(), Page: catalog.PageRequest{Limit: 1}}); err != catalog.ErrLimitExceeded {
		t.Fatal("PI-08 global limit changed", err)
	}
}
func TestLibraryProjectionStoredMedia(t *testing.T) {
	o := catalogQueryOwner(t)
	libraryUnmanaged(t, o, 1, 1, 2, "")
	catalogSchemaExec(t, o.database.db, "INSERT INTO tc_blobs VALUES (?,1,4097,1)", make([]byte, 32))
	catalogSchemaExec(t, o.database.db, "INSERT INTO tc_version_blobs VALUES (?,?)", catalogSchemaID("ver", 1), make([]byte, 32))
	// Stored metadata only: there is deliberately no blob file or media store.
	s := lib.NewService(o)
	scope := o.LibraryReadScope()
	libraryReady(t, o, s, scope)
	before := catalogRecoveryRows(t, o.database.db)
	all := libraryList(t, s, scope, 128)
	if all[0].Media != catalog.MediaBoundUnchecked || all[1].Media != catalog.CatalogOnly {
		t.Fatal(all)
	}
	if all[0].Supersession.IncomingCommand != nil || all[0].Supersession.OutgoingCommand != nil {
		t.Fatal("zero command IDs must be null")
	}
	if !reflect.DeepEqual(before, catalogRecoveryRows(t, o.database.db)) {
		t.Fatal("metadata read altered source")
	}
}

func TestLibraryProjectionReceipts(t *testing.T) {
	// Author stored PI-07 metadata/receipt through its repository, without a file
	// store, publishing bytes or making a media/network read available to Library.
	o := blobOwner(t)
	command := blobCommand(t, 1, false)
	key := blobKey(t, 1)
	if _, err := blobRecord(t, o, key, command); err != nil {
		t.Fatal(err)
	}
	libraryUnmanaged(t, o, 1, 100, 2, "unbound")
	before := catalogRecoveryRows(t, o.database.db)
	if len(before["tc_blob_imports"]) != 1 {
		t.Fatal("receipt fixture empty")
	}
	s := lib.NewService(o)
	scope := o.LibraryReadScope()
	libraryReady(t, o, s, scope)
	all := libraryList(t, s, scope, 1)
	if len(all) != 3 {
		t.Fatal(all)
	}
	found := false
	for _, item := range all {
		if item.VersionID == command.Version().ID().String() {
			found = true
			if item.Media != catalog.MediaBoundUnchecked {
				t.Fatal(item)
			}
		}
	}
	if !found {
		t.Fatal("stored receipt version excluded")
	}
	if _, err := s.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 129}, scope); err != lib.ErrLimitExceeded {
		t.Fatal(err)
	}
	if _, err := o.library.index.Exec("UPDATE memberships SET payload=x'00' WHERE substr(identity_key,31)=CAST(? AS BLOB)", command.Version().ID().String()); err != nil {
		t.Fatal(err)
	}
	if r, err := s.ListLibrary(t.Context(), lib.Query{}, lib.Page{Limit: 128}, scope); err != lib.ErrUnavailable || len(r.Items) != 0 || o.library.index != nil {
		t.Fatal(r, err)
	}
	if !reflect.DeepEqual(before, catalogRecoveryRows(t, o.database.db)) {
		t.Fatal("prepare/query/error altered receipt or retained detail")
	}
	if result, found, err := blobLookup(t, o, key, command); err != nil || !found || result != importResult(command) {
		t.Fatal("PI-07 readback changed", result, found, err)
	}
}

func TestLibraryProjectionModelArticle(t *testing.T) {
	f := newRegistrationFixture(t)
	model1, _ := domain.ParseModelNumber("01-0100")
	model2, _ := domain.ParseModelNumber("02-0200")
	article1, _ := domain.ParseArticleNumber("11000289")
	article2, _ := domain.ParseArticleNumber("22000390")
	m1, _ := domain.ModelClaim(model1)
	m2, _ := domain.ModelClaim(model2)
	a1, _ := domain.ArticleClaim(article1)
	a2, _ := domain.ArticleClaim(article2)
	dissent := map[domain.ClaimKey]evidence.ID{}
	for _, claims := range [][2]domain.CatalogClaim{{m1, m2}, {a1, a2}} {
		selected := f.observation(t, f.command.Subject, claims[0])
		other := f.observation(t, f.command.Subject, claims[1])
		f.command.Observations = append(f.command.Observations, selected, other)
		f.command.Selections = append(f.command.Selections, domain.CatalogRegistrationSelection{Key: claims[0].Key(), Observation: selected.ID()})
		dissent[claims[0].Key()] = other.ID()
	}
	if _, err := f.service.RegisterContent(t.Context(), f.command); err != nil {
		t.Fatal(err)
	}
	for n := 101; n <= 102; n++ {
		if _, err := f.service.RegisterCatalogVersion(t.Context(), f.version(t, n, uint64(n-100))); err != nil {
			t.Fatal(err)
		}
	}
	libraryUnmanaged(t, f.owner, 1, 1, 1, "stored baseline")
	catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_catalog_content SET model_number='03-0300',article_number='33000390' WHERE content_id=?", catalogSchemaID("cnt", 1))
	s := lib.NewService(f.owner)
	scope := f.owner.LibraryReadScope()
	conflicted := map[domain.ClaimKey]bool{}
	check := func() {
		t.Helper()
		libraryReady(t, f.owner, s, scope)
		all := libraryList(t, s, scope, 128)
		for _, item := range all {
			if item.ContentID == catalogSchemaID("cnt", 1) {
				if item.Baseline.Title != "stored baseline" || item.Baseline.Model == nil || *item.Baseline.Model != "03-0300" || item.Baseline.Article == nil || *item.Baseline.Article != "33000390" || item.Model.Value != nil || item.Article.Value != nil {
					t.Fatal("baseline became accepted fallback", item)
				}
				continue
			}
			for key, field := range map[domain.ClaimKey]lib.Field{domain.ModelClaimKey: item.Model, domain.ArticleClaimKey: item.Article} {
				if conflicted[key] {
					if field.Status != domain.CatalogFactConflict || field.Value != nil {
						t.Fatal("conflict scalar", field)
					}
				} else {
					want := "01-0100"
					if key == domain.ArticleClaimKey {
						want = "11000289"
					}
					if field.Status != domain.CatalogFactAccepted || field.Value == nil || *field.Value != want {
						t.Fatal(field, want)
					}
				}
			}
		}
	}
	check()
	for _, key := range []domain.ClaimKey{domain.ModelClaimKey, domain.ArticleClaimKey} {
		var snapshot catalog.ReviewSnapshot
		if err := f.owner.WithinCatalogTransaction(t.Context(), func(r catalog.CatalogRepository) error {
			var err error
			snapshot, _, err = r.LoadReview(t.Context(), f.command.Subject)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		command := catalog.ReviewCommand{ID: registrationIntegrationCommandID(t), Subject: f.command.Subject, ExpectedAggregate: snapshot.AggregateRevision, ExpectedReview: snapshot.State.Revision(), Key: key, Support: []evidence.ID{dissent[key]}, Reason: "accept contradictory retained product evidence"}
		if _, err := f.base.AcceptSupport(t.Context(), command); err != nil {
			t.Fatal(err)
		}
		conflicted[key] = true
		check()
	}
}
