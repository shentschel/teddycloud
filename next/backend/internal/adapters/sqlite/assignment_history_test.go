package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/assignment"
	catalogapp "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	tagapp "github.com/shentschel/teddycloud/next/backend/internal/application/tagregistry"
	d "github.com/shentschel/teddycloud/next/backend/internal/domain/assignment"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
	domaintag "github.com/shentschel/teddycloud/next/backend/internal/domain/tag"
	sqlite3 "modernc.org/sqlite/lib"
)

func assignmentHistoryPage(t *testing.T, s *app.Service, q app.HistoryQuery, want ...app.Result) app.HistoryPage {
	t.Helper()
	page, err := s.ListAssignmentHistory(t.Context(), q)
	if err != nil || len(page.Events) != len(want) || len(page.Next) > app.MaxHistoryCursor {
		t.Fatalf("history page: %d entries, cursor %d, error %v", len(page.Events), len(page.Next), err)
	}
	for i, event := range page.Events {
		if event.Result != want[i] {
			t.Fatalf("history receipt %d: got %+v, want %+v", i, event.Result, want[i])
		}
	}
	raw, err := json.Marshal(page)
	if err != nil || len(raw) > app.MaxHistoryBytes {
		t.Fatalf("history response: %d bytes, error %v", len(raw), err)
	}
	return page
}

func assignmentHistoryError(t *testing.T, s *app.Service, q app.HistoryQuery, want error) {
	t.Helper()
	page, err := s.ListAssignmentHistory(t.Context(), q)
	assignmentWantError(t, err, want)
	if !reflect.DeepEqual(page, app.HistoryPage{}) {
		t.Fatal("failed history released a partial page")
	}
}

func TestAssignmentHistoryPagination(t *testing.T) {
	f := newAssignmentRepositoryFixture(t)
	owner, s, _ := assignmentActivateOwner(t, f)
	ctx := t.Context()
	first := f.command(t, 1, app.Assign, 0)
	first.Target = f.foreign
	first.ExpectedCatalogRevision = 2
	one := f.execute(t, first)
	class := f.command(t, 2, app.RecordClassification, 1)
	two := f.execute(t, class)
	current := f.command(t, 3, app.Assign, 2)
	three := f.execute(t, current)

	// At the same effective time an older knowledge revision still knows only
	// the first exact tuple. Revision 2 has no transition of its own.
	q := app.HistoryQuery{EffectiveQuery: app.EffectiveQuery{TagID: f.tag, At: current.EffectiveAt}, Limit: 1}
	for _, tc := range []struct {
		knowledge, transition uint64
		target                d.OriginalTarget
		class                 d.PhysicalClassification
	}{
		{0, 0, d.OriginalTarget{}, d.Unknown},
		{1, 1, f.foreign, d.Unknown},
		{2, 1, f.foreign, d.Original},
		{3, 3, f.target, d.Original},
	} {
		query := q.EffectiveQuery
		query.KnowledgeRevision = &tc.knowledge
		view, err := s.GetEffectiveAssignment(ctx, query)
		got, getErr := s.GetAssignment(ctx, query)
		if err != nil || getErr != nil || view != got || !view.AggregatePresent || view.HeadRetired ||
			view.Desired.HeadRevision != 3 || view.Desired.KnowledgeRevision != tc.knowledge ||
			view.Desired.TransitionRevision != tc.transition || view.Desired.Target != tc.target || view.Classification != tc.class ||
			(tc.transition != 0 && (!view.CatalogOnly || view.BoundUnchecked || view.CatalogRevision == 0)) {
			t.Fatal("retained/as-known view", tc, view, err, getErr)
		}
	}
	zero := q
	zero.KnowledgeRevision = new(uint64(0))
	if got := assignmentHistoryPage(t, s, zero); got.HeadRevision != 3 || got.KnowledgeRevision != 0 || got.AssignmentID != f.id.String() || got.Next != "" {
		t.Fatal("knowledge zero lost aggregate identity", got)
	}
	page := assignmentHistoryPage(t, s, q, one)
	if page.Next == "" || page.TagID != f.tag.String() || page.TagRevision != 1 || page.HeadRevision != 3 || page.KnowledgeRevision != 3 {
		t.Fatal("initial page metadata", page)
	}
	next := q
	next.Cursor = page.Next
	page = assignmentHistoryPage(t, app.New(owner), next, two)
	next.Cursor = page.Next
	if page = assignmentHistoryPage(t, s, next, three); page.Next != "" {
		t.Fatal("unexpected continuation on final page")
	}
	for _, tc := range []struct {
		edit func(*app.HistoryQuery)
		want error
	}{
		{func(q *app.HistoryQuery) { q.Limit = 0 }, app.ErrInvalidInput},
		{func(q *app.HistoryQuery) { q.Limit = app.MaxHistoryEntries + 1 }, app.ErrInvalidInput},
		{func(q *app.HistoryQuery) { q.Cursor = strings.Repeat("a", app.MaxHistoryCursor+1) }, app.ErrLimit},
		{func(q *app.HistoryQuery) { q.KnowledgeRevision = new(uint64(4)) }, app.ErrKnowledgeRevision},
		{func(q *app.HistoryQuery) { q.KnowledgeRevision = new(uint64(2)) }, app.ErrStaleCursor},
		{func(q *app.HistoryQuery) { q.At = q.At.Add(time.Nanosecond) }, app.ErrStaleCursor},
		{func(q *app.HistoryQuery) { q.TagID, _ = identity.ParseTagID(catalogSchemaID("tag", 2)) }, app.ErrStaleCursor},
		{func(q *app.HistoryQuery) { q.Cursor = q.Cursor[:len(q.Cursor)-1] + "!" }, app.ErrStaleCursor},
	} {
		bad := next
		tc.edit(&bad)
		assignmentHistoryError(t, s, bad, tc.want)
	}

	// Hold knowledge fixed to prove independently that head, a Tag write
	// and a catalog write outside selected knowledge each stale a cursor.
	q.KnowledgeRevision = new(uint64(2))
	continuation := func() app.HistoryQuery {
		t.Helper()
		page := assignmentHistoryPage(t, s, q, one)
		if page.Next == "" {
			t.Fatal("missing fixed-knowledge continuation")
		}
		next := q
		next.Cursor = page.Next
		return next
	}
	next = continuation()
	f.execute(t, f.command(t, 4, app.Unassign, 3))
	assignmentHistoryError(t, s, next, app.ErrStaleCursor)

	next = continuation()
	tags := tagapp.New(owner)
	tag, err := tags.FindByID(ctx, f.tag.String())
	if err != nil {
		t.Fatal(err)
	}
	const key = domaintag.FactKey(0)
	obs := tagMetadataObservation(t, tag, 1000, key, "true")
	tag, err = tags.UpdateMetadata(ctx, tagapp.MetadataCommand{TagID: tag.ID(), Change: domaintag.MetadataChange{
		ExpectedRevision: tag.Revision(), Key: key, Observations: []evidence.Observation{obs},
	}})
	if err != nil || tag.Revision() != 2 {
		t.Fatal("Tag dependency write", err)
	}
	assignmentHistoryError(t, s, next, app.ErrStaleCursor)

	next = continuation()
	upstream := f.upstream
	upstream.owner = owner
	upstream.bind(t)
	upstream.sup, err = catalogapp.NewSupersessionService(upstream.base, upstream.cap)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := upstream.sup.SupersedeVersion(ctx, upstream.command); err != nil {
		t.Fatal("catalog dependency write", err)
	}
	assignmentHistoryError(t, s, next, app.ErrStaleCursor)
	view, err := s.GetAssignment(ctx, app.EffectiveQuery{TagID: f.tag, At: current.EffectiveAt})
	if err != nil || !view.Superseded || !view.CatalogOnly || view.CatalogRevision <= current.ExpectedCatalogRevision ||
		view.Desired.Target != f.target || view.Desired.Until.IsZero() {
		t.Fatal("live catalog annotation retargeted or lost historical interval", view, err)
	}

	// The backed-up data is identical to the cursor snapshot. Only lifecycle
	// identity changes, first on same-owner restore and then on reopen.
	next = continuation()
	snapshot, err := owner.database.CreateBackup(ctx, filepath.Join(t.TempDir(), "snapshot.sqlite"), SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	restoredPath := filepath.Join(t.TempDir(), "restored.sqlite")
	if err := owner.Restore(ctx, snapshot, restoredPath); err != nil {
		t.Fatal(err)
	}
	assignmentHistoryError(t, s, next, app.ErrStaleCursor)
	next = continuation()
	if page := assignmentHistoryPage(t, s, next, two); page.Next != "" || page.HeadRevision != 4 || page.KnowledgeRevision != 2 {
		t.Fatal("restore lost retained page", page)
	}
	if err := owner.Close(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenLifecycleOwner(ctx, Config{Path: restoredPath}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })
	s = app.New(reopened)
	assignmentHistoryError(t, s, next, app.ErrStaleCursor)
	assignmentHistoryPage(t, s, continuation(), two)
}

func TestAssignmentHistoryMediaAnnotation(t *testing.T) {
	f := newAssignmentRepositoryFixture(t)
	owner, s, _ := assignmentActivateOwner(t, f)
	first := f.command(t, 1, app.Assign, 0)
	one := f.execute(t, first)
	two := f.execute(t, f.command(t, 2, app.RecordClassification, 1))
	q := app.HistoryQuery{EffectiveQuery: app.EffectiveQuery{TagID: f.tag, At: first.EffectiveAt}, Limit: 1}
	q.Cursor = assignmentHistoryPage(t, s, q, one).Next
	assignmentHistoryPage(t, s, q, two)

	// Commit the real media metadata without creating a blob file. Assignment
	// metadata reads have no media dependency and must return bound-unchecked.
	digest := sha256.Sum256([]byte("metadata-only assignment fixture"))
	in, err := content.NewImportCommand(f.upstream.command.Old, content.NewBlobID(digest), content.MinTAFBytes, content.TAFProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := blobRecord(t, owner, blobKey(t, 1), in); err != nil || result != importResult(in) {
		t.Fatal("bind exact version metadata", err)
	}
	assignmentHistoryError(t, s, q, app.ErrStaleCursor)
	view, err := s.GetEffectiveAssignment(t.Context(), q.EffectiveQuery)
	if err != nil || view.Desired.Target != f.target || view.CatalogOnly || !view.BoundUnchecked ||
		view.CatalogRevision != first.ExpectedCatalogRevision || view.Desired.HeadRevision != 2 {
		t.Fatal("binding changed assignment or attempted media verification", view, err)
	}
	q.Cursor = ""
	q.Limit = 2
	assignmentHistoryPage(t, s, q, one, two)
}

func TestAssignmentHistorySQLiteByteLimit(t *testing.T) {
	f := newAssignmentRepositoryFixture(t)
	_, s, _ := assignmentActivateOwner(t, f)
	results := make([]app.Result, 0, app.MaxHistoryEntries)
	for n := 1; n <= app.MaxHistoryEntries; n++ {
		in := f.command(t, n, app.Assign, uint64(n-1))
		in.Reason = strings.Repeat("<", 1024)
		results = append(results, f.execute(t, in))
	}
	q := app.HistoryQuery{EffectiveQuery: app.EffectiveQuery{TagID: f.tag, At: time.Unix(10, 0)}, Limit: app.MaxHistoryEntries}
	page, err := s.ListAssignmentHistory(t.Context(), q)
	if err != nil || len(page.Events) == 0 || len(page.Events) >= q.Limit || page.Next == "" {
		t.Fatalf("byte-limited SQLite page: %d entries, error %v", len(page.Events), err)
	}
	firstCount := len(page.Events)
	page = assignmentHistoryPage(t, s, q, results[:firstCount]...)
	q.Cursor = page.Next
	last := assignmentHistoryPage(t, s, q, results[firstCount:]...)
	raw, err := json.Marshal(page)
	extra, entryErr := json.Marshal(last.Events[0])
	if err != nil || entryErr != nil || last.Next != "" || len(raw)+len(extra)+1 <= app.MaxHistoryBytes {
		t.Fatalf("byte limit or continuation: %d + %d bytes, next %t, errors %v %v", len(raw), len(extra), last.Next != "", err, entryErr)
	}
}

func TestAssignmentTombstoneRetention(t *testing.T) {
	f := newAssignmentRepositoryFixture(t)
	owner, s, _ := assignmentActivateOwner(t, f)
	ctx := t.Context()
	first := f.command(t, 1, app.Assign, 0)
	firstResult := f.execute(t, first)
	baseline := f.command(t, 2, app.RecordOriginalTarget, 1)
	baselineResult := f.execute(t, baseline)
	later := f.command(t, 3, app.Assign, 2)
	later.Target = f.other
	later.URI = app.OptionalURI{Present: true, Value: "HTTPS://Catalog.Example:443/a%62"}
	laterResult := f.execute(t, later)
	unassign := f.command(t, 4, app.Unassign, 3)
	unassignedResult := f.execute(t, unassign)
	retire := f.command(t, 5, app.RetireAssignment, 4)
	retiredResult := f.execute(t, retire)

	for _, tc := range []struct {
		at         time.Time
		knowledge  *uint64
		state      d.DesiredState
		transition uint64
		target     d.OriginalTarget
		baseline   d.OriginalTarget
	}{
		{retire.EffectiveAt, nil, d.Retired, 5, d.OriginalTarget{}, f.target},
		{unassign.EffectiveAt, nil, d.Unassigned, 4, d.OriginalTarget{}, f.target},
		{later.EffectiveAt, nil, d.Assigned, 3, f.other, f.target},
		{retire.EffectiveAt, new(uint64(2)), d.Assigned, 1, f.target, f.target},
		{retire.EffectiveAt, new(uint64(1)), d.Assigned, 1, f.target, d.OriginalTarget{}},
	} {
		view, err := s.GetAssignment(ctx, app.EffectiveQuery{TagID: f.tag, At: tc.at, KnowledgeRevision: tc.knowledge})
		if err != nil || !view.AggregatePresent || !view.HeadRetired || view.Desired.AssignmentID != f.id ||
			view.Desired.State != tc.state || view.Desired.TransitionRevision != tc.transition ||
			view.Desired.Target != tc.target || view.OriginalTarget != tc.baseline ||
			(tc.state != d.Assigned && (view.CatalogOnly || view.CatalogRevision != 0)) {
			t.Fatal("tombstone/retained view", tc, view, err)
		}
	}
	q := app.HistoryQuery{EffectiveQuery: app.EffectiveQuery{TagID: f.tag, At: retire.EffectiveAt}, Limit: 5}
	page := assignmentHistoryPage(t, s, q, firstResult, baselineResult, laterResult, unassignedResult, retiredResult)
	if !page.HeadRetired || page.Next != "" || page.Events[0].Command.URI.Present ||
		page.Events[2].Command.URI.Value != "https://catalog.example/ab" || page.Events[2].Command.Target != f.other ||
		page.Events[4].Result.PreviousCommand != unassign.CommandID {
		t.Fatal("history lost tombstone, exact target or original receipt")
	}
	for _, tc := range []struct {
		query string
		arg   string
	}{
		{"DELETE FROM tc_tags WHERE tag_id=?", f.tag.String()},
		{"DELETE FROM tc_content_versions WHERE version_id=?", f.target.VersionID().String()},
		{"UPDATE tc_content_versions SET version_id='ver_00000000000000000000000999' WHERE version_id=?", f.other.VersionID().String()},
	} {
		// SQLite reports RESTRICT through its foreign-key trigger.
		assignmentSchemaReject(t, owner.database.db, sqlite3.SQLITE_CONSTRAINT_TRIGGER, tc.query, tc.arg)
	}
	if got := assignmentHistoryPage(t, s, q, firstResult, baselineResult, laterResult, unassignedResult, retiredResult); !reflect.DeepEqual(got, page) {
		t.Fatal("rejected writes changed receipts or retained material")
	}
}

func TestAssignmentCorruptLedger(t *testing.T) {
	for _, tc := range []struct {
		name        string
		breakLedger func(*testing.T, *assignmentRepositoryFixture, app.Command)
	}{
		{"head revision", func(t *testing.T, f *assignmentRepositoryFixture, _ app.Command) {
			catalogSchemaExec(t, f.db.db, "UPDATE tc_assignment_heads SET head_revision=1,event_count=1")
		}},
		{"projection revision", func(t *testing.T, f *assignmentRepositoryFixture, _ app.Command) {
			catalogSchemaExec(t, f.db.db, "UPDATE tc_assignment_projections SET head_revision=1")
		}},
		{"hash-valid projection", func(t *testing.T, f *assignmentRepositoryFixture, _ app.Command) {
			raw := []byte(`{}`)
			digest := sha256.Sum256(raw)
			catalogSchemaExec(t, f.db.db, "UPDATE tc_assignment_projections SET canonical_projection=?,projection_sha256=?", raw, digest[:])
		}},
		{"late receipt", func(t *testing.T, f *assignmentRepositoryFixture, late app.Command) {
			catalogSchemaExec(t, f.db.db, "DROP TRIGGER tc_assignment_commands_no_delete")
			catalogSchemaExec(t, f.db.db, "DELETE FROM tc_assignment_commands WHERE command_id=?", late.CommandID.String())
		}},
		{"late evidence", func(t *testing.T, f *assignmentRepositoryFixture, late app.Command) {
			catalogSchemaExec(t, f.db.db, "DROP TRIGGER tc_assignment_support_no_delete")
			catalogSchemaExec(t, f.db.db, "DROP TRIGGER tc_assignment_evidence_no_delete")
			catalogSchemaExec(t, f.db.db, "DELETE FROM tc_assignment_support WHERE revision=2")
			catalogSchemaExec(t, f.db.db, "DELETE FROM tc_assignment_evidence WHERE evidence_id=?", late.Evidence.ID().String())
		}},
		{"late retained target support", func(t *testing.T, f *assignmentRepositoryFixture, late app.Command) {
			catalogSchemaExec(t, f.db.db, "DROP TRIGGER tc_catalog_registration_support_no_delete")
			catalogSchemaExec(t, f.db.db, "DELETE FROM tc_catalog_registration_support WHERE subject_id=? AND claim_key=5", late.Target.VersionID().String())
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAssignmentRepositoryFixture(t)
			_, s, _ := assignmentActivateOwner(t, f)
			first := f.command(t, 1, app.Assign, 0)
			result := f.execute(t, first)
			late := f.command(t, 2, app.RecordOriginalTarget, 1)
			late.Target = f.other
			f.execute(t, late)
			q := app.HistoryQuery{EffectiveQuery: app.EffectiveQuery{TagID: f.tag, At: first.EffectiveAt}, Limit: 1}
			page := assignmentHistoryPage(t, s, q, result)
			if page.Next == "" {
				t.Fatal("test requires persisted evidence beyond page one")
			}
			tc.breakLedger(t, f, late)
			assignmentHistoryError(t, s, q, app.ErrUnavailable)
			q.Cursor = page.Next
			assignmentHistoryError(t, s, q, app.ErrUnavailable)
			q.KnowledgeRevision = new(uint64(1))
			for _, read := range []func(context.Context, app.EffectiveQuery) (app.AssignmentView, error){s.GetAssignment, s.GetEffectiveAssignment} {
				view, err := read(t.Context(), q.EffectiveQuery)
				assignmentWantError(t, err, app.ErrUnavailable)
				if view != (app.AssignmentView{}) {
					t.Fatal("failed closed read released a view")
				}
			}
		})
	}
}

func TestAssignmentHistoryReadStates(t *testing.T) {
	f := newAssignmentRepositoryFixture(t)
	owner, s, _ := assignmentActivateOwner(t, f)
	q := app.HistoryQuery{EffectiveQuery: app.EffectiveQuery{TagID: f.tag, At: time.Unix(10, 0)}, Limit: 1}
	view, err := s.GetAssignment(t.Context(), q.EffectiveQuery)
	if err != nil || view.AggregatePresent || view.TagRevision != 1 || view.Desired.State != d.NoAssignment {
		t.Fatal("known Tag without aggregate", view, err)
	}
	if page := assignmentHistoryPage(t, s, q); page.AssignmentID != "" || page.TagRevision != 1 || page.Next != "" {
		t.Fatal("empty history lost known Tag", page)
	}
	absent := q
	absent.TagID, _ = identity.ParseTagID(catalogSchemaID("tag", 99))
	assignmentHistoryError(t, s, absent, app.ErrTagNotFound)
	_, err = s.GetEffectiveAssignment(t.Context(), absent.EffectiveQuery)
	assignmentWantError(t, err, app.ErrTagNotFound)

	// Block the A2 gate to exercise an earlier deadline and concurrent
	// busy response through the production assignment owner.
	if err := owner.gate.enterOperation(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer owner.gate.leave()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.ListAssignmentHistory(ctx, q)
		done <- err
	}()
	for !owner.assignmentAdmitted.Load() {
		select {
		case err := <-done:
			t.Fatal("query did not wait for owner", err)
		case <-time.After(time.Millisecond):
		}
	}
	assignmentHistoryError(t, s, q, app.ErrBusy)
	cancel()
	assignmentWantError(t, <-done, context.Canceled)
	ctx, cancel = context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err = s.ListAssignmentHistory(ctx, q)
	assignmentWantError(t, err, context.DeadlineExceeded)
}

func TestAssignmentHistoryOversizedState(t *testing.T) {
	f := newAssignmentRepositoryFixture(t)
	_, s, _ := assignmentActivateOwner(t, f)
	for n := 1; n <= 3; n++ {
		f.execute(t, f.command(t, n, app.Assign, uint64(n-1)))
	}
	// Each persisted row stays within the SQL envelope ceiling. Collectively
	// the rows and matching head charges exceed D5's reconstruction ceiling.
	catalogSchemaExec(t, f.db.db, "DROP TRIGGER tc_assignment_events_no_update")
	catalogSchemaExec(t, f.db.db, "PRAGMA ignore_check_constraints=ON")
	defer catalogSchemaExec(t, f.db.db, "PRAGMA ignore_check_constraints=OFF")
	catalogSchemaExec(t, f.db.db, "UPDATE tc_assignment_events SET canonical_event=zeroblob(?),retained_bytes=?", 6<<20, 7<<20)
	catalogSchemaExec(t, f.db.db, "UPDATE tc_assignment_heads SET retained_bytes=?", 21<<20)
	q := app.HistoryQuery{EffectiveQuery: app.EffectiveQuery{TagID: f.tag, At: time.Unix(10, 0)}, Limit: 1}
	assignmentHistoryError(t, s, q, app.ErrUnsupportedState)
	_, err := s.GetAssignment(t.Context(), q.EffectiveQuery)
	assignmentWantError(t, err, app.ErrUnsupportedState)

	catalogSchemaExec(t, f.db.db, "UPDATE tc_assignment_heads SET retained_bytes=retained_bytes+1")
	assignmentHistoryError(t, s, q, app.ErrUnavailable)
}

func TestAssignmentHistorySchemaUnavailable(t *testing.T) {
	owner, err := OpenLifecycleOwner(t.Context(), Config{Path: filepath.Join(t.TempDir(), "v9.sqlite")}, SchemaMigrations()[:9])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close(context.Background()) })
	s := app.New(owner)
	tag, _ := identity.ParseTagID(catalogSchemaID("tag", 1))
	q := app.HistoryQuery{EffectiveQuery: app.EffectiveQuery{TagID: tag, At: time.Unix(10, 0)}, Limit: 1}
	assignmentHistoryError(t, s, q, app.ErrUnsupportedState)
	_, err = s.GetAssignment(t.Context(), q.EffectiveQuery)
	assignmentWantError(t, err, app.ErrUnsupportedState)
	var tables int
	if err := owner.database.db.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_schema WHERE name LIKE 'tc_assignment%'").Scan(&tables); err != nil || tables != 0 {
		t.Fatal("schema-unavailable reads created assignment tables", tables, err)
	}
}
