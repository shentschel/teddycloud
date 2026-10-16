package sqlite

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	domain "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

func catalogQueryOwner(t *testing.T) *LifecycleOwner {
	t.Helper()
	owner, err := OpenLifecycleOwner(t.Context(), Config{Path: filepath.Join(t.TempDir(), "queries.sqlite")}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	return owner
}

func catalogQueryContent(t *testing.T, n int) identity.ContentID {
	t.Helper()
	id, err := identity.ParseContentID(catalogSchemaID("cnt", n))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func catalogQueryVersion(t *testing.T, n int) identity.ContentVersionID {
	t.Helper()
	id, err := identity.ParseContentVersionID(catalogSchemaID("ver", n))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func catalogRead(t *testing.T, owner *LifecycleOwner, query app.Query) (app.QueryResult, error) {
	t.Helper()
	var result app.QueryResult
	err := owner.WithinCatalogTransaction(t.Context(), func(repository app.CatalogRepository) error {
		var err error
		result, err = repository.Query(t.Context(), query)
		return err
	})
	return result, err
}

func catalogSeedVersions(t *testing.T, owner *LifecycleOwner, count int) {
	t.Helper()
	catalogSchemaExec(t, owner.database.db, "INSERT INTO tc_catalog_content VALUES (?,'Stored <baseline>', '01-0100',NULL)", catalogSchemaID("cnt", 1))
	for n := count; n > 0; n-- {
		catalogSchemaExec(t, owner.database.db, "INSERT INTO tc_content_versions VALUES (?,?,123,?,0,NULL,NULL)",
			catalogSchemaID("ver", n), catalogSchemaID("cnt", 1), bytes.Repeat([]byte{1}, 20))
	}
}

func TestCatalogQueryAfterCommitOrder(t *testing.T) {
	f, _, op, next, result := orderWriteFixture(t)
	query := app.Query{Kind: app.ListVersions, Content: f.subject.ContentID(), Page: app.PageRequest{Limit: 1}}
	before, err := catalogRead(t, f.owner, query)
	if err != nil || before.Next == "" {
		t.Fatal("initial page", before, err)
	}
	for n := uint64(2); n <= 3; n++ {
		if err := commitOrderFixture(t, f, op, next, result); err != nil {
			t.Fatal(err)
		}
		for _, kind := range []app.QueryKind{app.GetContent, app.GetVersion, app.ListVersions} {
			q := query
			q.Kind = kind
			if kind == app.GetVersion {
				q.Version = f.versions[0].Version.ID()
			}
			got, err := catalogRead(t, f.owner, q)
			wantKind := app.Found
			if kind == app.ListVersions {
				wantKind = app.Incomplete
			}
			if err != nil || got.Kind != wantKind || got.Revision.Uint64() != n || len(got.Entries) != 1 || got.EncodedBytes == 0 {
				t.Fatalf("revision %d kind %d: %+v %v", n, kind, got, err)
			}
			if kind == app.ListVersions {
				if got.Complete || got.Next == "" || got.Entries[0].Version.ID() != f.versions[0].Version.ID() {
					t.Fatal("bounded first page", got)
				}
				q.Page.Cursor, q.Page.ExpectedRevision = got.Next, got.Revision
				last, err := catalogRead(t, f.owner, q)
				if err != nil || !last.Complete || last.Revision != got.Revision || len(last.Entries) != 1 || last.Entries[0].Version.ID() != f.versions[1].Version.ID() {
					t.Fatal("second page", last, err)
				}
			}
		}
		stale := query
		stale.Page.Cursor, stale.Page.ExpectedRevision = before.Next, before.Revision
		if _, err := catalogRead(t, f.owner, stale); !errors.Is(err, app.ErrStaleCursor) {
			t.Fatal("stale cursor", err)
		}
		if err := commitOrderFixture(t, f, op, next, result); err != nil {
			t.Fatal("exact replay", err)
		}
		op.Command.ID, _ = domain.NewCatalogCommandID()
		op.ExpectedAggregate, op.Command.Expected = result.AggregateRevision, next.Revision()
		op.Command.Evidence = nil
		var decision domain.CatalogOrderResult
		next, decision, err = next.Apply(f.order, op.Command)
		if err != nil {
			t.Fatal(err)
		}
		result.AggregateRevision, _ = domain.NewCatalogRevision(n + 1)
		result.Decision = decision
	}
}

func TestCatalogQueryAfterCommitOrderMixedHistory(t *testing.T) {
	f := catalogRecoveryCurrent(t)
	query := app.Query{Kind: app.ListVersions, Content: f.subject.ContentID(), Page: app.PageRequest{Limit: 2}}
	assertQuery := func(revision uint64) {
		t.Helper()
		got, err := catalogRead(t, f.owner, query)
		if err != nil || got.Kind != app.Found || !got.Complete || got.Revision.Uint64() != revision || len(got.Entries) != 2 {
			t.Fatal("mixed history", got, err)
		}
		for _, entry := range got.Entries {
			title, ok := entry.Title.Value()
			if !ok || title.Text() != "written title" {
				t.Fatal("review projection changed", entry)
			}
		}
	}
	assertQuery(5)
	catalogRecoveryReplays(t, f)
	err := f.owner.WithinCatalogTransaction(t.Context(), func(r app.CatalogRepository) error {
		stored, found, err := r.LoadReview(t.Context(), f.subject)
		if err != nil || !found {
			t.Fatal(found, err)
		}
		op := f.reviewOp
		op.Command.ID, _ = domain.NewCatalogCommandID()
		op.ExpectedAggregate, op.Command.Expected = stored.AggregateRevision, stored.State.Revision()
		op.Command.Kind, op.Command.Key = domain.AcceptSupport, domain.TitleClaimKey
		op.Command.Support = []evidence.ID{op.Command.Observations[0].ID()}
		op.Command.Observations = nil
		raw, err := stored.State.PersistenceEncoding()
		if err != nil {
			return err
		}
		live, err := domain.RehydrateCatalogReviewState(raw, domain.CatalogPersistenceBindings{AllowUnbound: true, Sources: []domain.SourceRegistration{f.source}, Reviewers: []domain.IssuerPrincipal{f.review.Reviewer()}})
		if err != nil {
			return err
		}
		next, decision, err := live.Apply(f.review, op.Command)
		if err != nil {
			return err
		}
		revision, _ := domain.NewCatalogRevision(6)
		return r.CommitReview(t.Context(), op, next, app.ReviewResult{AggregateRevision: revision, Decision: decision})
	})
	if err != nil {
		t.Fatal(err)
	}
	assertQuery(6)
}

func TestCatalogQueryAfterCommitOrderCorruptHistory(t *testing.T) {
	for _, failure := range []string{"gap", "duplicate", "digest"} {
		t.Run(failure, func(t *testing.T) {
			f := catalogRecoveryCurrent(t)
			catalogSchemaExec(t, f.owner.database.db, "DROP TRIGGER tc_catalog_order_states_no_update")
			switch failure {
			case "gap":
				catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_catalog_order_states SET aggregate_revision=6 WHERE order_revision=3")
			case "duplicate":
				catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_catalog_order_states SET aggregate_revision=2 WHERE order_revision=3")
			case "digest":
				catalogSchemaExec(t, f.owner.database.db, "UPDATE tc_catalog_order_states SET state_sha256=randomblob(32) WHERE order_revision=3")
			}
			_, err := catalogRead(t, f.owner, app.Query{Kind: app.ListVersions, Content: f.subject.ContentID(), Page: app.PageRequest{Limit: 2}})
			if !errors.Is(err, app.ErrRepositoryUnavailable) {
				t.Fatal("corruption accepted", err)
			}
		})
	}
}

func TestCatalogOnlyVersionQuery(t *testing.T) {
	owner := catalogQueryOwner(t)
	catalogSeedVersions(t, owner, 3)
	for _, kind := range []app.QueryKind{app.GetContent, app.GetVersion, app.ListVersions} {
		query := app.Query{Kind: kind, Content: catalogQueryContent(t, 1), Page: app.PageRequest{Limit: 128}}
		want := 1
		if kind == app.GetVersion {
			query.Version = catalogQueryVersion(t, 2)
		}
		if kind == app.ListVersions {
			want = 3
		}
		result, err := catalogRead(t, owner, query)
		if err != nil || result.Kind != app.Found || !result.Complete || len(result.Entries) != want || result.Revision.Uint64() != 1 || result.EncodedBytes == 0 {
			t.Fatalf("kind %d: %#v, %v", kind, result, err)
		}
		for i, entry := range result.Entries {
			if entry.Baseline.Title() != "Stored <baseline>" || entry.Review != app.CatalogUnmanaged || entry.Title.Status != domain.CatalogFactUnknown || len(entry.Title.Alternatives) != 0 {
				t.Fatalf("baseline became reviewed: %#v", entry)
			}
			if model, ok := entry.Baseline.ProductIdentifiers().Model(); !ok || model.String() != "01-0100" {
				t.Fatal("stored model lost")
			}
			if kind == app.GetContent {
				if entry.Media != app.MediaUnknown || !entry.Version.ID().IsZero() {
					t.Fatal("content invented a version/media relation")
				}
			} else if entry.Media != app.CatalogOnly {
				t.Fatalf("unbound version media: %d", entry.Media)
			}
			if kind == app.ListVersions && entry.Version.ID() != catalogQueryVersion(t, i+1) {
				t.Fatal("unstable opaque version order")
			}
		}
	}
	for _, table := range []string{"tc_blobs", "tc_version_blobs", "tc_blob_imports", "tc_catalog_managed", "tc_catalog_decisions"} {
		var count int
		if err := owner.database.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("query fabricated %s: %d %v", table, count, err)
		}
	}
	query := app.Query{Kind: app.GetVersion, Content: catalogQueryContent(t, 1), Version: catalogQueryVersion(t, 99), Page: app.PageRequest{Limit: 1}}
	result, err := catalogRead(t, owner, query)
	if err != nil || result.Kind != app.NoMatch || !result.Complete || len(result.Entries) != 0 {
		t.Fatalf("absent version: %#v %v", result, err)
	}
	query.Kind, query.Content, query.Version = app.GetContent, catalogQueryContent(t, 99), identity.ContentVersionID{}
	result, err = catalogRead(t, owner, query)
	if err != nil || result.Kind != app.NoMatch {
		t.Fatalf("absent content: %#v %v", result, err)
	}
	catalogSchemaExec(t, owner.database.db, "INSERT INTO tc_catalog_managed VALUES (?,1,1)", catalogSchemaID("cnt", 1))
	query.Content = catalogQueryContent(t, 1)
	result, err = catalogRead(t, owner, query)
	if err != nil || result.Entries[0].Review != app.CatalogManagedUnreviewed || !result.Entries[0].ProtectedCustom {
		t.Fatalf("managed unreviewed: %#v %v", result, err)
	}
}

func TestCatalogReviewedProjectionQuery(t *testing.T) {
	db := catalogSchemaFixture(t)
	owner := &LifecycleOwner{gate: newLifecycleGate(), database: db}
	// The schema-only fixture deliberately does not maintain domain revisions.
	catalogSchemaExec(t, db.db, "UPDATE tc_catalog_managed SET aggregate_revision=2 WHERE content_id=?", catalogSchemaID("cnt", 1))
	catalogSchemaExec(t, db.db, "UPDATE tc_catalog_subjects SET review_revision=2 WHERE subject_id=?", catalogSchemaID("cnt", 1))
	query := app.Query{Kind: app.GetContent, Content: catalogQueryContent(t, 1), Page: app.PageRequest{Limit: 1}}
	accepted, err := catalogRead(t, owner, query)
	if err != nil || accepted.Kind != app.Found || accepted.Entries[0].Review != app.CatalogReviewed || accepted.Entries[0].Baseline.Title() != "Baseline" {
		t.Fatalf("accepted projection: %#v %v", accepted, err)
	}
	title := accepted.Entries[0].Title
	if value, ok := title.Value(); !ok || value.Text() != "Candidate" || len(title.Alternatives[0].Support) != 1 {
		t.Fatal("accepted support missing")
	}
	// Another explicit decision retains dissent. Source and confidence have no
	// precedence; the adapter returns both alternatives without choosing one.
	catalogSchemaExec(t, db.db, `INSERT INTO tc_catalog_observations SELECT ?,subject_id,content_id,claim_key,'Different',NULL,NULL,NULL,batch_id,record_index,source_id,registration_revision,asserted_origin,observed_at,2 FROM tc_catalog_observations WHERE observation_id=?`,
		catalogSchemaID("obs", 3), catalogSchemaID("obs", 1))
	catalogSchemaExec(t, db.db, `INSERT INTO tc_catalog_decisions SELECT ?,subject_id,content_id,source_id,registration_revision,reviewer_principal,policy_revision,2,1,2,3,2,3,reason,canonical_command,canonical_result FROM tc_catalog_decisions WHERE command_id=?`, strings.Repeat("6", 32), strings.Repeat("5", 32))
	catalogSchemaExec(t, db.db, `INSERT INTO tc_catalog_decision_support SELECT subject_id,source_id,registration_revision,claim_key,?,? FROM tc_catalog_decision_support WHERE command_id=?`,
		strings.Repeat("6", 32), catalogSchemaID("obs", 3), strings.Repeat("5", 32))
	catalogSchemaExec(t, db.db, "UPDATE tc_catalog_managed SET aggregate_revision=3 WHERE content_id=?", catalogSchemaID("cnt", 1))
	catalogSchemaExec(t, db.db, "UPDATE tc_catalog_subjects SET review_revision=3 WHERE subject_id=?", catalogSchemaID("cnt", 1))
	conflicted, err := catalogRead(t, owner, query)
	if err != nil || conflicted.Kind != app.Conflicted || conflicted.Entries[0].Title.Status != domain.CatalogFactConflict {
		t.Fatalf("conflict: %#v %v", conflicted, err)
	}
	if _, ok := conflicted.Entries[0].Title.Value(); ok {
		t.Fatal("implicit winner selected")
	}
	again, err := catalogRead(t, owner, query)
	if err != nil || !reflect.DeepEqual(again, conflicted) {
		t.Fatal("projection/support order changed")
	}
	catalogSchemaExec(t, db.db, `INSERT INTO tc_catalog_decisions SELECT ?,subject_id,content_id,source_id,registration_revision,reviewer_principal,policy_revision,3,1,3,4,3,4,reason,canonical_command,canonical_result FROM tc_catalog_decisions WHERE command_id=?`, strings.Repeat("7", 32), strings.Repeat("5", 32))
	catalogSchemaExec(t, db.db, `INSERT INTO tc_catalog_decision_support SELECT subject_id,source_id,registration_revision,claim_key,?,observation_id FROM tc_catalog_decision_support WHERE command_id=?`,
		strings.Repeat("7", 32), strings.Repeat("5", 32))
	catalogSchemaExec(t, db.db, "UPDATE tc_catalog_managed SET aggregate_revision=4 WHERE content_id=?", catalogSchemaID("cnt", 1))
	catalogSchemaExec(t, db.db, "UPDATE tc_catalog_subjects SET review_revision=4 WHERE subject_id=?", catalogSchemaID("cnt", 1))
	resolved, err := catalogRead(t, owner, query)
	if err != nil || resolved.Kind != app.Found || len(resolved.Entries[0].Title.Alternatives) != 1 || resolved.Entries[0].Title.Alternatives[0].Claim.Text() != "Candidate" {
		t.Fatalf("explicit resolution: %#v %v", resolved, err)
	}
}

func TestCatalogRevisionBoundCursor(t *testing.T) {
	owner := catalogQueryOwner(t)
	catalogSeedVersions(t, owner, 3)
	query := app.Query{Kind: app.ListVersions, Content: catalogQueryContent(t, 1), Page: app.PageRequest{Limit: 1}}
	first, err := catalogRead(t, owner, query)
	if err != nil || first.Kind != app.Incomplete || first.Complete || first.Next == "" {
		t.Fatalf("first page: %#v %v", first, err)
	}
	query.Page.Cursor, query.Page.ExpectedRevision = first.Next, first.Revision
	second, err := catalogRead(t, owner, query)
	if err != nil || second.Entries[0].Version.ID() != catalogQueryVersion(t, 2) || second.Next == "" {
		t.Fatalf("second page: %#v %v", second, err)
	}
	query.Page.Cursor = second.Next
	third, err := catalogRead(t, owner, query)
	if err != nil || !third.Complete || third.Next != "" || third.Entries[0].Version.ID() != catalogQueryVersion(t, 3) {
		t.Fatalf("last page: %#v %v", third, err)
	}
	query.Page.Cursor = first.Next
	query.Page.Limit = 2
	if _, err := catalogRead(t, owner, query); !errors.Is(err, app.ErrStaleCursor) {
		t.Fatalf("changed typed query: %v", err)
	}
	query.Page.Limit = 1
	query.Page.Cursor = first.Next[:len(first.Next)-1] + "!"
	if _, err := catalogRead(t, owner, query); !errors.Is(err, app.ErrStaleCursor) {
		t.Fatalf("tamper: %v", err)
	}
	query.Page.Cursor = first.Next
	query.Page.ExpectedRevision, _ = domain.NewCatalogRevision(2)
	if _, err := catalogRead(t, owner, query); !errors.Is(err, app.ErrStaleCursor) {
		t.Fatalf("stale revision: %v", err)
	}
	query.Page.ExpectedRevision = first.Revision
	catalogSchemaExec(t, owner.database.db, "UPDATE tc_catalog_content SET title='Changed baseline' WHERE content_id=?", catalogSchemaID("cnt", 1))
	if _, err := catalogRead(t, owner, query); !errors.Is(err, app.ErrStaleCursor) {
		t.Fatalf("same-revision legacy write: %v", err)
	}
	catalogSchemaExec(t, owner.database.db, "UPDATE tc_catalog_content SET title='Stored <baseline>' WHERE content_id=?", catalogSchemaID("cnt", 1))
	if _, err := catalogRead(t, owner, query); !errors.Is(err, app.ErrStaleCursor) {
		t.Fatalf("same-value ABA write: %v", err)
	}
	query.Page.Cursor, query.Page.ExpectedRevision = "", domain.CatalogRevision{}
	fresh, err := catalogRead(t, owner, query)
	if err != nil {
		t.Fatal(err)
	}
	query.Page.Cursor, query.Page.ExpectedRevision = fresh.Next, fresh.Revision
	external, err := Open(t.Context(), owner.config, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	catalogSchemaExec(t, external.db, "UPDATE tc_catalog_content SET title='External ABA'")
	catalogSchemaExec(t, external.db, "UPDATE tc_catalog_content SET title='Stored <baseline>'")
	if err := external.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := catalogRead(t, owner, query); !errors.Is(err, app.ErrStaleCursor) {
		t.Fatalf("external same-value ABA write: %v", err)
	}
	query.Page.Cursor, query.Page.ExpectedRevision = "", domain.CatalogRevision{}
	fresh, err = catalogRead(t, owner, query)
	if err != nil {
		t.Fatal(err)
	}
	query.Page.Cursor, query.Page.ExpectedRevision = fresh.Next, fresh.Revision
	owner.generation.Add(1)
	if _, err := catalogRead(t, owner, query); !errors.Is(err, app.ErrStaleCursor) {
		t.Fatalf("restore generation: %v", err)
	}
}

func TestCatalogQueryReadOnlyTransaction(t *testing.T) {
	owner := catalogQueryOwner(t)
	catalogSeedVersions(t, owner, 1)
	var escaped app.CatalogRepository
	callbackError := errors.New("rollback requested")
	calls := 0
	err := owner.WithinCatalogTransaction(t.Context(), func(repository app.CatalogRepository) error {
		calls++
		escaped = repository
		if _, err := repository.(*catalogRepository).transaction.ExecContext(t.Context(), "UPDATE tc_catalog_content SET title='Mutation'"); err == nil {
			t.Fatal("read-only transaction allowed a write")
		}
		if err := repository.CommitReview(t.Context(), app.ReviewOperation{}, domain.CatalogReviewState{}, app.ReviewResult{}); !errors.Is(err, app.ErrInvalidInput) {
			t.Fatalf("write port: %v", err)
		}
		return callbackError
	})
	if err != callbackError || calls != 1 {
		t.Fatalf("callback replay/error: %d %v", calls, err)
	}
	query := app.Query{Kind: app.GetContent, Content: catalogQueryContent(t, 1), Page: app.PageRequest{Limit: 1}}
	if _, err := escaped.Query(t.Context(), query); !errors.Is(err, app.ErrRepositoryUnavailable) {
		t.Fatalf("escaped repository: %v", err)
	}
	if err := escaped.CommitOrder(t.Context(), app.OrderOperation{}, domain.CatalogOrderState{}, app.OrderResult{}); !errors.Is(err, app.ErrRepositoryUnavailable) {
		t.Fatalf("escaped write method: %v", err)
	}
	// Connection-local query_only must be restored before the pool is reused.
	catalogSchemaExec(t, owner.database.db, "UPDATE tc_catalog_content SET title='After rollback'")
	result, err := catalogRead(t, owner, query)
	if err != nil || result.Entries[0].Baseline.Title() != "After rollback" {
		t.Fatalf("connection/readback: %#v %v", result, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	err = owner.WithinCatalogTransaction(ctx, func(app.CatalogRepository) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation before completion: %v", err)
	}
	catalogSchemaExec(t, owner.database.db, "UPDATE tc_catalog_content SET title='After cancellation'")
}

func TestCatalogQueryBounds(t *testing.T) {
	owner := catalogQueryOwner(t)
	catalogSeedVersions(t, owner, 1)
	query := app.Query{Kind: app.ListVersions, Content: catalogQueryContent(t, 1), Page: app.PageRequest{Limit: 128}}
	if _, err := catalogRead(t, owner, query); err != nil {
		t.Fatal(err)
	}
	query.Page.Limit = 129
	if _, err := catalogRead(t, owner, query); !errors.Is(err, app.ErrLimitExceeded) {
		t.Fatalf("entry one-over: %v", err)
	}
	query.Page.Limit = 1
	query.Page.Cursor = strings.Repeat("x", 257)
	if _, err := catalogRead(t, owner, query); !errors.Is(err, app.ErrLimitExceeded) {
		t.Fatalf("cursor one-over: %v", err)
	}
	query.Page.Cursor = ""
	catalogSchemaExec(t, owner.database.db, `WITH RECURSIVE n(x) AS (VALUES(2) UNION ALL SELECT x+1 FROM n WHERE x<1024)
 INSERT INTO tc_content_versions SELECT printf('ver_%026d',x),?,123,zeroblob(20),0,NULL,NULL FROM n`, catalogSchemaID("cnt", 1))
	result, err := catalogRead(t, owner, query)
	if err != nil || result.Inspected != 1024 || result.Kind != app.Incomplete {
		t.Fatalf("candidate exact bound: %#v %v", result, err)
	}
	catalogSchemaExec(t, owner.database.db, "INSERT INTO tc_content_versions VALUES (?,?,123,zeroblob(20),0,NULL,NULL)", catalogSchemaID("ver", 1025), catalogSchemaID("cnt", 1))
	if _, err := catalogRead(t, owner, query); !errors.Is(err, app.ErrLimitExceeded) {
		t.Fatalf("candidate one-over: %v", err)
	}
	// Real escaped encoding counts support and text, not private empty structs.
	claim, err := domain.TitleClaim(strings.Repeat("<", 4096))
	if err != nil {
		t.Fatal(err)
	}
	support, _ := evidence.ParseID(catalogSchemaID("obs", 1))
	revision, _ := domain.NewCatalogRevision(1)
	record := app.CatalogRecord{Content: catalogQueryContent(t, 1), Baseline: domain.NewContentFacts("", domain.NewProductIdentifiers(nil, nil)), Review: app.CatalogReviewed, Title: domain.CatalogField{Status: domain.CatalogFactAccepted, Alternatives: []domain.CatalogAlternative{{Claim: claim, Support: []evidence.ID{support}}}}}
	page := app.QueryResult{Kind: app.Found, Revision: revision, Complete: true}
	for n := 0; n < 128; n++ {
		page.Entries = append(page.Entries, record)
	}
	if _, err := catalogPageSize(page); !errors.Is(err, app.ErrLimitExceeded) {
		t.Fatalf("escaped query bytes: %v", err)
	}
}

func TestCatalogQueryCorruptRelations(t *testing.T) {
	for _, mutation := range []string{
		"UPDATE tc_catalog_managed SET aggregate_revision=3 WHERE content_id='" + catalogSchemaID("cnt", 1) + "'",
		"DROP TRIGGER tc_catalog_observations_no_update; PRAGMA ignore_check_constraints=ON; UPDATE tc_catalog_observations SET text_value='bad'||char(0)||'text'; PRAGMA ignore_check_constraints=OFF",
		"DROP TRIGGER tc_catalog_decision_support_no_delete; DELETE FROM tc_catalog_decision_support",
		"DROP TRIGGER tc_catalog_decision_support_no_update; PRAGMA foreign_keys=OFF; UPDATE tc_catalog_decision_support SET observation_id='" + catalogSchemaID("obs", 99) + "'; PRAGMA foreign_keys=ON",
	} {
		t.Run(mutation[:min(len(mutation), 40)], func(t *testing.T) {
			db := catalogSchemaFixture(t)
			owner := &LifecycleOwner{gate: newLifecycleGate(), database: db}
			catalogSchemaExec(t, db.db, "UPDATE tc_catalog_managed SET aggregate_revision=2 WHERE content_id=?", catalogSchemaID("cnt", 1))
			catalogSchemaExec(t, db.db, "UPDATE tc_catalog_subjects SET review_revision=2 WHERE subject_id=?", catalogSchemaID("cnt", 1))
			catalogSchemaExec(t, db.db, mutation)
			query := app.Query{Kind: app.GetContent, Content: catalogQueryContent(t, 1), Page: app.PageRequest{Limit: 1}}
			if _, err := catalogRead(t, owner, query); !errors.Is(err, app.ErrRepositoryUnavailable) {
				t.Fatalf("corrupt relation was projected: %v", err)
			}
		})
	}
}

func TestCatalogQueryBoundMediaAndReopen(t *testing.T) {
	owner := catalogQueryOwner(t)
	command := blobCommand(t, 1, true)
	if err := owner.WithinTransaction(t.Context(), func(repository app.ContentRepository) error {
		return repository.Save(t.Context(), testContent(t, "Bound baseline", false))
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := blobRecord(t, owner, blobKey(t, 1), command); err != nil {
		t.Fatal(err)
	}
	query := app.Query{Kind: app.GetVersion, Content: command.Version().ContentID(), Version: command.Version().ID(), Page: app.PageRequest{Limit: 1}}
	result, err := catalogRead(t, owner, query)
	if err != nil || result.Entries[0].Media != app.MediaBoundUnchecked || result.Entries[0].Version != command.Version() {
		t.Fatalf("binding became verified or tuple changed: %#v %v", result, err)
	}
	if replay, found, err := blobLookup(t, owner, blobKey(t, 1), command); err != nil || !found || replay != importResult(command) {
		t.Fatalf("query changed old receipt: %v %v", found, err)
	}
	var retained app.CatalogRepository
	if err := owner.WithinCatalogTransaction(t.Context(), func(repository app.CatalogRepository) error {
		retained = repository
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := retained.LoadReview(t.Context(), domain.CatalogSubject{}); !errors.Is(err, app.ErrRepositoryUnavailable) {
		t.Fatalf("escaped read method: %v", err)
	}
	// Reopen invalidates cursor authentication even in the same process.
	other := catalogQueryOwner(t)
	catalogSeedVersions(t, other, 2)
	list := app.Query{Kind: app.ListVersions, Content: catalogQueryContent(t, 1), Page: app.PageRequest{Limit: 1}}
	first, err := catalogRead(t, other, list)
	if err != nil {
		t.Fatal(err)
	}
	config := other.config
	if err := other.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenLifecycleOwner(t.Context(), config, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(context.Background())
	list.Page.Cursor, list.Page.ExpectedRevision = first.Next, first.Revision
	if _, err := catalogRead(t, reopened, list); !errors.Is(err, app.ErrStaleCursor) {
		t.Fatalf("cursor survived owner restart: %v", err)
	}
}

func TestCatalogQueryThroughApplication(t *testing.T) {
	owner := catalogQueryOwner(t)
	catalogSeedVersions(t, owner, 2)
	sourceID, err := domain.NewSourceID()
	if err != nil {
		t.Fatal(err)
	}
	principal, err := domain.NewIssuerPrincipal()
	if err != nil {
		t.Fatal(err)
	}
	source, _, err := domain.NewSourceRegistration(sourceID, principal, domain.SourceSpec{Adapter: "test", AdapterVersion: "1", Scope: "query", Acquisition: domain.CoreOperator, AllowedClaims: []domain.ClaimKey{domain.TitleClaimKey}})
	if err != nil {
		t.Fatal(err)
	}
	review, err := domain.NewReviewCapability(principal, source)
	if err != nil {
		t.Fatal(err)
	}
	namespace, err := domain.NewCatalogOrderNamespace(source, "query/1")
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewService(owner, source, review, namespace)
	if err != nil {
		t.Fatal(err)
	}
	query := app.Query{Kind: app.ListVersions, Content: catalogQueryContent(t, 1), Page: app.PageRequest{Limit: 1}}
	first, err := service.Query(t.Context(), query)
	if err != nil || first.Kind != app.Incomplete {
		t.Fatalf("application rejected first page: %#v %v", first, err)
	}
	query.Page.Cursor, query.Page.ExpectedRevision = first.Next, first.Revision
	last, err := service.Query(t.Context(), query)
	if err != nil || !last.Complete || last.Entries[0].Version.ID() != catalogQueryVersion(t, 2) {
		t.Fatalf("application rejected continuation: %#v %v", last, err)
	}
	query.Kind, query.Content, query.Page = app.FindVersionsByAudioID, identity.ContentID{}, app.PageRequest{Limit: 1}
	query.AudioID, _ = domain.NewAudioID(123)
	result, err := service.Query(t.Context(), query)
	if err != nil || result.Kind != app.Incomplete || result.Complete || len(result.Entries) != 1 {
		t.Fatalf("application global find: %#v %v", result, err)
	}
	query.Page.Cursor, query.Page.ExpectedRevision = result.Next, result.Revision
	last, err = service.Query(t.Context(), query)
	if err != nil || last.Kind != app.Incomplete || last.Complete || last.Next != "" {
		t.Fatalf("global continuation implied uniqueness: %#v %v", last, err)
	}
	query.Page = app.PageRequest{Limit: 128}
	result, err = service.Query(t.Context(), query)
	if err != nil || result.Kind != app.Ambiguous || !result.Complete || len(result.Entries) != 2 {
		t.Fatalf("application global ambiguity: %#v %v", result, err)
	}
}

func catalogFindQuery(t *testing.T, kind app.QueryKind, limit uint64) app.Query {
	t.Helper()
	audio, err := domain.NewAudioID(123)
	if err != nil {
		t.Fatal(err)
	}
	query := app.Query{Kind: kind, Page: app.PageRequest{Limit: limit}}
	if kind == app.FindVersionsByAudioID {
		query.AudioID = audio
	} else {
		hash, err := domain.ParseAudioHash(strings.Repeat("01", 20))
		if err != nil {
			t.Fatal(err)
		}
		query.Fingerprint, err = domain.NewAudioFingerprint(audio, hash)
		if err != nil {
			t.Fatal(err)
		}
	}
	return query
}

func TestCatalogGlobalFindIdentitiesAndOutcomes(t *testing.T) {
	for _, kind := range []app.QueryKind{app.FindVersionsByPair, app.FindVersionsByAudioID} {
		t.Run("find", func(t *testing.T) {
			owner := catalogQueryOwner(t)
			query := catalogFindQuery(t, kind, 128)
			empty, err := catalogRead(t, owner, query)
			if err != nil || empty.Kind != app.NoMatch || !empty.Complete || empty.Inspected != 0 {
				t.Fatalf("empty catalog: %#v %v", empty, err)
			}
			catalogSeedVersions(t, owner, 1)
			one, err := catalogRead(t, owner, query)
			if err != nil || one.Kind != app.Found || !one.Complete || len(one.Entries) != 1 {
				t.Fatalf("unique: %#v %v", one, err)
			}
			// Reverse version identity order across owners to exercise tuple order.
			catalogSchemaExec(t, owner.database.db, "UPDATE tc_content_versions SET version_id=? WHERE version_id=?", catalogSchemaID("ver", 3), catalogSchemaID("ver", 1))
			catalogSchemaExec(t, owner.database.db, "INSERT INTO tc_catalog_content VALUES (?,'Other owner',NULL,NULL)", catalogSchemaID("cnt", 2))
			catalogSchemaExec(t, owner.database.db, "INSERT INTO tc_content_versions VALUES (?,?,123,?,0,NULL,NULL)", catalogSchemaID("ver", 1), catalogSchemaID("cnt", 2), bytes.Repeat([]byte{1}, 20))
			catalogSchemaExec(t, owner.database.db, "INSERT INTO tc_content_versions VALUES (?,?,123,?,0,NULL,NULL)", catalogSchemaID("ver", 2), catalogSchemaID("cnt", 1), bytes.Repeat([]byte{1}, 20))
			result, err := catalogRead(t, owner, query)
			if err != nil || result.Kind != app.Ambiguous || !result.Complete || len(result.Entries) != 3 || result.Inspected != 3 {
				t.Fatalf("duplicate pairs: %#v %v", result, err)
			}
			for i, version := range []int{2, 3, 1} {
				if result.Entries[i].Version.ID() != catalogQueryVersion(t, version) || result.Entries[i].Media != app.CatalogOnly {
					t.Fatal("opaque identities/order/media lost")
				}
			}
			again, err := catalogRead(t, owner, query)
			if err != nil || !reflect.DeepEqual(again, result) {
				t.Fatal("global result was nondeterministic")
			}
			query.Content = catalogQueryContent(t, 2)
			result, err = catalogRead(t, owner, query)
			if err != nil || result.Kind != app.Found || result.Entries[0].Content != query.Content {
				t.Fatalf("optional owner filter: %#v %v", result, err)
			}
			query.Content = catalogQueryContent(t, 99)
			result, err = catalogRead(t, owner, query)
			if err != nil || result.Kind != app.NoMatch || !result.Complete {
				t.Fatalf("absent owner filter: %#v %v", result, err)
			}
			query = catalogFindQuery(t, kind, 128)
			catalogSchemaExec(t, owner.database.db, "UPDATE tc_content_versions SET audio_sha1=? WHERE version_id=?", bytes.Repeat([]byte{2}, 20), catalogSchemaID("ver", 1))
			result, err = catalogRead(t, owner, query)
			want := 3
			if kind == app.FindVersionsByPair {
				want = 2
			}
			if err != nil || result.Kind != app.Ambiguous || len(result.Entries) != want {
				t.Fatalf("pair versus audio matching: %#v %v", result, err)
			}
			for _, table := range []string{"tc_blobs", "tc_version_blobs", "tc_blob_imports", "tc_catalog_managed", "tc_catalog_decisions"} {
				var count int
				if err := owner.database.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
					t.Fatalf("find fabricated %s: %d %v", table, count, err)
				}
			}
		})
	}
}

func TestCatalogGlobalFindCursor(t *testing.T) {
	for _, kind := range []app.QueryKind{app.FindVersionsByPair, app.FindVersionsByAudioID} {
		owner := catalogQueryOwner(t)
		catalogSeedVersions(t, owner, 3)
		query := catalogFindQuery(t, kind, 1)
		first, err := catalogRead(t, owner, query)
		if err != nil || first.Kind != app.Incomplete || first.Next == "" || first.Complete {
			t.Fatalf("first: %#v %v", first, err)
		}
		for n := 2; n <= 3; n++ {
			if n == 2 {
				query.Page.Cursor = first.Next
			}
			query.Page.ExpectedRevision = first.Revision
			page, err := catalogRead(t, owner, query)
			if err != nil || page.Kind != app.Incomplete || page.Complete || page.Entries[0].Version.ID() != catalogQueryVersion(t, n) || (page.Next == "") != (n == 3) {
				t.Fatalf("page %d: %#v %v", n, page, err)
			}
			query.Page.Cursor = page.Next
		}
		query.Page.Cursor = first.Next
		original := query
		query.Page.Limit = 2
		if _, err := catalogRead(t, owner, query); !errors.Is(err, app.ErrStaleCursor) {
			t.Fatalf("changed limit: %v", err)
		}
		query = original
		query.Content = catalogQueryContent(t, 1)
		if _, err := catalogRead(t, owner, query); !errors.Is(err, app.ErrStaleCursor) {
			t.Fatalf("changed owner filter: %v", err)
		}
		query = catalogFindQuery(t, kind, 1)
		if kind == app.FindVersionsByAudioID {
			query.AudioID, _ = domain.NewAudioID(124)
		} else {
			hash, _ := domain.ParseAudioHash(strings.Repeat("02", 20))
			audio, _ := domain.NewAudioID(123)
			query.Fingerprint, _ = domain.NewAudioFingerprint(audio, hash)
		}
		query.Page = original.Page
		if _, err := catalogRead(t, owner, query); !errors.Is(err, app.ErrStaleCursor) {
			t.Fatalf("changed search attribute: %v", err)
		}
		query = original
		query.Page.Cursor = first.Next[:len(first.Next)-1] + "!"
		if _, err := catalogRead(t, owner, query); !errors.Is(err, app.ErrStaleCursor) {
			t.Fatalf("tamper: %v", err)
		}
		query = original
		query.Page.ExpectedRevision, _ = domain.NewCatalogRevision(2)
		if _, err := catalogRead(t, owner, query); !errors.Is(err, app.ErrStaleCursor) {
			t.Fatalf("stale revision: %v", err)
		}
		query = original
		catalogSchemaExec(t, owner.database.db, "UPDATE tc_catalog_content SET title='ABA'")
		catalogSchemaExec(t, owner.database.db, "UPDATE tc_catalog_content SET title='Stored <baseline>'")
		if _, err := catalogRead(t, owner, query); !errors.Is(err, app.ErrStaleCursor) {
			t.Fatalf("same-value ABA: %v", err)
		}
		query.Page = app.PageRequest{Limit: 1}
		fresh, err := catalogRead(t, owner, query)
		if err != nil {
			t.Fatal(err)
		}
		query.Page.Cursor, query.Page.ExpectedRevision = fresh.Next, fresh.Revision
		external, err := Open(t.Context(), owner.config, SchemaMigrations())
		if err != nil {
			t.Fatal(err)
		}
		catalogSchemaExec(t, external.db, "UPDATE tc_catalog_content SET title='External ABA'")
		catalogSchemaExec(t, external.db, "UPDATE tc_catalog_content SET title='Stored <baseline>'")
		if err := external.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := catalogRead(t, owner, query); !errors.Is(err, app.ErrStaleCursor) {
			t.Fatalf("external ABA: %v", err)
		}
		query.Page = app.PageRequest{Limit: 1}
		fresh, err = catalogRead(t, owner, query)
		if err != nil {
			t.Fatal(err)
		}
		query.Page.Cursor, query.Page.ExpectedRevision = fresh.Next, fresh.Revision
		owner.generation.Add(1)
		if _, err := catalogRead(t, owner, query); !errors.Is(err, app.ErrStaleCursor) {
			t.Fatalf("restore generation: %v", err)
		}
	}
}

func TestCatalogGlobalFindBoundsAndCorruption(t *testing.T) {
	owner := catalogQueryOwner(t)
	catalogSeedVersions(t, owner, 1)
	query := catalogFindQuery(t, app.FindVersionsByAudioID, 128)
	query.Page.Limit = 129
	if _, err := catalogRead(t, owner, query); !errors.Is(err, app.ErrLimitExceeded) {
		t.Fatalf("entry one-over: %v", err)
	}
	query.Page.Limit = 128
	query.Page.Cursor = strings.Repeat("x", 257)
	if _, err := catalogRead(t, owner, query); !errors.Is(err, app.ErrLimitExceeded) {
		t.Fatalf("cursor one-over: %v", err)
	}
	query.Page.Cursor = ""
	catalogSchemaExec(t, owner.database.db, `WITH RECURSIVE n(x) AS (VALUES(2) UNION ALL SELECT x+1 FROM n WHERE x<1024)
 INSERT INTO tc_content_versions SELECT printf('ver_%026d',x),?,123,zeroblob(20),0,NULL,NULL FROM n`, catalogSchemaID("cnt", 1))
	result, err := catalogRead(t, owner, query)
	if err != nil || result.Inspected != 1024 || result.Kind != app.Incomplete {
		t.Fatalf("global exact scan bound: %#v %v", result, err)
	}
	catalogSchemaExec(t, owner.database.db, "INSERT INTO tc_content_versions VALUES (?,?,999,zeroblob(20),0,NULL,NULL)", catalogSchemaID("ver", 1025), catalogSchemaID("cnt", 1))
	if _, err := catalogRead(t, owner, query); !errors.Is(err, app.ErrLimitExceeded) {
		t.Fatalf("nonmatching row exceeds global scan: %v", err)
	}
	for _, mutation := range []string{
		"PRAGMA ignore_check_constraints=ON; UPDATE tc_content_versions SET audio_sha1=zeroblob(19); PRAGMA ignore_check_constraints=OFF",
		"PRAGMA foreign_keys=OFF; UPDATE tc_content_versions SET content_id='" + catalogSchemaID("cnt", 99) + "'; PRAGMA foreign_keys=ON",
		"UPDATE tc_catalog_content SET title='bad'||char(0)||'text'",
		"PRAGMA ignore_check_constraints=ON; UPDATE tc_content_versions SET order_known=2; PRAGMA ignore_check_constraints=OFF",
	} {
		t.Run(mutation[:min(len(mutation), 40)], func(t *testing.T) {
			owner := catalogQueryOwner(t)
			catalogSeedVersions(t, owner, 1)
			catalogSchemaExec(t, owner.database.db, mutation)
			query := catalogFindQuery(t, app.FindVersionsByAudioID, 128)
			query.AudioID, _ = domain.NewAudioID(999)
			if _, err := catalogRead(t, owner, query); !errors.Is(err, app.ErrRepositoryUnavailable) {
				t.Fatalf("corruption hidden by no match: %v", err)
			}
		})
	}
}

func TestCatalogGlobalFindEncodedBytes(t *testing.T) {
	owner := catalogQueryOwner(t)
	catalogSeedVersions(t, owner, 128)
	catalogSchemaExec(t, owner.database.db, "UPDATE tc_catalog_content SET title=?", strings.Repeat("<", 4096))
	query := catalogFindQuery(t, app.FindVersionsByPair, 128)
	first, err := catalogRead(t, owner, query)
	if err != nil || first.Kind != app.Incomplete || first.Next == "" || len(first.Entries) == 0 || len(first.Entries) >= 128 || first.EncodedBytes > domain.QueryBytes.Maximum() {
		t.Fatalf("escaped byte bounded page: %#v %v", first, err)
	}
	seen := len(first.Entries)
	page := first
	for page.Next != "" {
		query.Page.Cursor, query.Page.ExpectedRevision = page.Next, page.Revision
		page, err = catalogRead(t, owner, query)
		if err != nil || page.Kind != app.Incomplete || page.Complete || page.EncodedBytes > domain.QueryBytes.Maximum() {
			t.Fatalf("byte bounded continuation: %#v %v", page, err)
		}
		seen += len(page.Entries)
	}
	if seen != 128 {
		t.Fatalf("lost candidates: %d", seen)
	}
}

func TestCatalogGlobalFindReviewedProjection(t *testing.T) {
	db := catalogSchemaFixture(t)
	owner := &LifecycleOwner{gate: newLifecycleGate(), database: db}
	catalogSchemaExec(t, db.db, "UPDATE tc_catalog_managed SET aggregate_revision=2 WHERE content_id=?", catalogSchemaID("cnt", 1))
	catalogSchemaExec(t, db.db, "UPDATE tc_catalog_subjects SET review_revision=2 WHERE subject_id=?", catalogSchemaID("cnt", 1))
	query := catalogFindQuery(t, app.FindVersionsByPair, 128)
	result, err := catalogRead(t, owner, query)
	if err != nil || result.Kind != app.Ambiguous || result.Revision.Uint64() != 2 || result.Entries[0].Review != app.CatalogReviewed || len(result.Entries[0].Title.Alternatives[0].Support) != 1 {
		t.Fatalf("review projection/support lost: %#v %v", result, err)
	}
	catalogSchemaExec(t, db.db, "DROP TRIGGER tc_catalog_decision_support_no_delete; DELETE FROM tc_catalog_decision_support")
	if _, err := catalogRead(t, owner, query); !errors.Is(err, app.ErrRepositoryUnavailable) {
		t.Fatalf("missing reviewed support: %v", err)
	}
}
