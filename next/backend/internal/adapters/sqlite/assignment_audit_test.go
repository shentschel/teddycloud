package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/assignment"
	catalogapp "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	tagapp "github.com/shentschel/teddycloud/next/backend/internal/application/tagregistry"
	d "github.com/shentschel/teddycloud/next/backend/internal/domain/assignment"
	c "github.com/shentschel/teddycloud/next/backend/internal/domain/catalog"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/identity"
)

// No assignment rows or Tag rows are seeded. Both editorial owners and their
// identical audio fingerprints are authored independently through their services.
func assignmentAuditFixture(t *testing.T) (*assignmentRepositoryFixture, *LifecycleOwner, supersessionFixture) {
	t.Helper()
	core := newRegistrationFixture(t)
	sources := make([]registrationFixture, 2)
	reviews := []c.ReviewCapability{core.review}
	for i, kind := range []c.AcquisitionClass{c.OfflinePublic, c.OfflineCustom} {
		source := core
		id, _ := c.NewSourceID()
		issuer, _ := c.NewIssuerPrincipal()
		var err error
		source.source, source.ingest, err = c.NewSourceRegistration(id, issuer, c.SourceSpec{Adapter: "audit-fixture", AdapterVersion: "1", Scope: "offline", Acquisition: kind, AllowedClaims: []c.ClaimKey{c.TitleClaimKey, c.FingerprintClaimKey}})
		if err != nil {
			t.Fatal(err)
		}
		source.review, err = c.NewReviewCapability(core.review.Reviewer(), source.source)
		if err != nil {
			t.Fatal(err)
		}
		sources[i] = source
		reviews = append(reviews, source.review)
	}
	var err error
	core.capability, err = c.NewCatalogRegistrationCapability(core.review, reviews)
	if err != nil {
		t.Fatal(err)
	}
	core.bind(t)
	registrations := make([]registrationFixture, 2)
	for i, source := range sources {
		r := core
		r.command.Subject, _ = c.ContentSubject(catalogQueryContent(t, 100+i*100))
		r.command.ID, _ = c.NewCatalogCommandID()
		claim, _ := c.TitleClaim("Identical public/custom title")
		observation := source.observation(t, r.command.Subject, claim)
		r.command.Observations = []c.CatalogObservation{observation}
		r.command.Selections = []c.CatalogRegistrationSelection{{Key: c.TitleClaimKey, Observation: observation.ID()}}
		if _, err := r.service.RegisterContent(t.Context(), r.command); err != nil {
			t.Fatal(err)
		}
		count := 1
		if i == 0 {
			count = 2
		}
		for j := 1; j <= count; j++ {
			in := r.version(t, 100+i*100+j, uint64(j))
			fp := source.observation(t, in.Subject, in.Observations[0].Input().Claim)
			in.Observations[0] = fp
			in.Selections[0].Observation = fp.ID()
			if _, err := r.service.RegisterCatalogVersion(t.Context(), in); err != nil {
				t.Fatal(err)
			}
		}
		registrations[i] = r
	}
	tags := tagapp.New(core.owner)
	for i, uid := range []string{"11:22:33:44:55:66:77:88", "22:22:33:44:55:66:77:88"} {
		if _, err := tags.Register(t.Context(), tagapp.RegisterCommand{OpaqueID: catalogSchemaID("tag", i+1), Physical: tagapp.PhysicalIdentityInput{UID: tagapp.OptionalText{Present: true, Value: uid}}}); err != nil {
			t.Fatal(err)
		}
	}
	sup := supersessionFixture{registrationFixture: registrations[0]}
	sup.cap, err = c.NewCatalogSupersessionCapability(core.review)
	if err != nil {
		t.Fatal(err)
	}
	sup.sup, err = catalogapp.NewSupersessionService(sup.base, sup.cap)
	if err != nil {
		t.Fatal(err)
	}
	page, err := catalogRead(t, core.owner, catalogapp.Query{Kind: catalogapp.ListVersions, Content: sup.registrationFixture.command.Subject.ContentID(), Page: catalogapp.PageRequest{Limit: 128}})
	if err != nil || len(page.Entries) != 2 {
		t.Fatal("registered versions", err)
	}
	sup.command = sup.link(t, page.Entries[0].Version, page.Entries[1].Version, 3, 1)
	f := &assignmentRepositoryFixture{db: core.owner.database, service: app.New(core.owner)}
	f.id, _ = d.ParseID(catalogSchemaID("asn", 1))
	f.tag, _ = identity.ParseTagID(catalogSchemaID("tag", 1))
	f.target, _ = d.NewOriginalTarget(sup.command.Content, sup.command.Old.ID())
	f.other, _ = d.NewOriginalTarget(sup.command.Content, sup.command.New.ID())
	f.foreign, _ = d.NewOriginalTarget(registrations[1].command.Subject.ContentID(), catalogQueryVersion(t, 201))
	collisions, err := catalogRead(t, core.owner, catalogapp.Query{Kind: catalogapp.FindVersionsByPair, Fingerprint: sup.command.Old.Fingerprint(), Page: catalogapp.PageRequest{Limit: 128}})
	if err != nil || collisions.Kind != catalogapp.Ambiguous || len(collisions.Entries) != 3 {
		t.Fatal("fixture lacks ambiguous public/custom fingerprint", collisions, err)
	}
	return f, core.owner, sup
}

func TestAssignmentMilestone(t *testing.T) {
	f, owner, sup := assignmentAuditFixture(t)
	service := f.service
	ctx := t.Context()
	path := dbPathForSupersessionTest(t, f.db)
	var inputs []app.Command
	var results []app.Result
	record := func(in app.Command) app.Result {
		t.Helper()
		got, err := service.Execute(ctx, in)
		if err != nil {
			t.Fatal("milestone command", in.Kind, in.ExpectedRevision, err)
		}
		inputs = append(inputs, in)
		results = append(results, got)
		if replay, err := service.Execute(ctx, in); err != nil || replay != got {
			t.Fatal("step exact replay", err)
		}
		return got
	}
	query := func(at time.Time) app.AssignmentView {
		t.Helper()
		view, err := service.GetAssignment(ctx, app.EffectiveQuery{TagID: f.tag, At: at})
		if err != nil {
			t.Fatal(err)
		}
		return view
	}
	first := f.command(t, 1, app.Assign, 0)
	record(first)
	view := query(first.EffectiveAt)
	if !view.CatalogOnly || view.BoundUnchecked || !view.Dimensions.PublicSupport() || view.Dimensions.CustomOrigin() || view.Classification != d.Unknown || view.OriginalState != d.OriginalTargetUnknown {
		t.Fatal("catalog-only public assignment inferred authority", view)
	}
	secondTag := f.command(t, 100, app.Assign, 0)
	secondTag.TagID, _ = identity.ParseTagID(catalogSchemaID("tag", 2))
	secondTag.AssignmentID, _ = d.ParseID(catalogSchemaID("asn", 2))
	secondTag.Target = f.foreign
	secondTag.ExpectedCatalogRevision = 2
	secondResult, err := service.Execute(ctx, secondTag)
	if err != nil {
		t.Fatal(err)
	}
	secondView, err := service.GetAssignment(ctx, app.EffectiveQuery{TagID: secondTag.TagID, At: secondTag.EffectiveAt})
	if err != nil || !secondView.Dimensions.CustomOrigin() || secondView.Dimensions.Protection() != d.ProtectedCustom || secondView.Classification != d.Unknown {
		t.Fatal("custom content classified physical Tag", err)
	}

	// Two independently instantiated services race the same owner/revision.
	a := f.command(t, 2, app.Assign, 1)
	a.Target = f.foreign
	a.ExpectedCatalogRevision = 2
	b := f.command(t, 3, app.Assign, 1)
	type outcome struct {
		in     app.Command
		result app.Result
		err    error
	}
	done := make(chan outcome, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, in := range []app.Command{a, b} {
		wg.Add(1)
		go func(in app.Command) {
			defer wg.Done()
			<-start
			r, e := app.New(owner).Execute(ctx, in)
			done <- outcome{in, r, e}
		}(in)
	}
	close(start)
	wg.Wait()
	close(done)
	wins, losses := 0, 0
	for out := range done {
		if out.err == nil {
			wins++
			inputs = append(inputs, out.in)
			results = append(results, out.result)
		} else {
			losses++
			if !errors.Is(out.err, app.ErrBusy) && !errors.Is(out.err, app.ErrRevisionConflict) {
				t.Fatal("unexpected race rejection", out.err)
			}
		}
	}
	if wins != 1 || losses != 1 {
		t.Fatal("same-revision race did not have one winner", wins, losses)
	}
	// Explicitly exercise original physical Tag with custom content.
	reassign := f.command(t, 4, app.Assign, 2)
	reassign.Target = f.foreign
	reassign.ExpectedCatalogRevision = 2
	record(reassign)
	original := f.command(t, 5, app.RecordClassification, 3)
	record(original)
	custom := f.command(t, 6, app.RecordClassification, 4)
	custom.Classification = d.CustomCard
	record(custom)
	view = query(reassign.EffectiveAt)
	if view.Classification != d.Conflict || view.Desired.Target != f.foreign {
		t.Fatal("physical conflict retargeted", view)
	}
	resolve := f.command(t, 7, app.ResolveClassification, 5)
	resolve.Support = []d.EvidenceID{original.Evidence.ID()}
	record(resolve)
	baseline := f.command(t, 8, app.RecordOriginalTarget, 6)
	record(baseline)
	view = query(reassign.EffectiveAt)
	if view.Classification != d.Original || view.Desired.Target != f.foreign || view.OriginalTarget != f.target || view.Dimensions.Protection() != d.ProtectedCustom {
		t.Fatal("baseline/physical/custom dimensions collapsed", view)
	}

	uri := f.command(t, 9, app.Assign, 7)
	uri.URI = app.OptionalURI{Present: true, Value: "HTTPS://Unresolvable.Invalid:443/original%2Dcard"}
	if got, err := assignmentLostResponse(owner).Execute(ctx, uri); got != (app.Result{}) || err != app.ErrIndeterminate {
		t.Fatal("lost response not indeterminate", err)
	}
	want := app.Result{CommandID: uri.CommandID, AssignmentID: f.id, TagID: f.tag, Revision: 8, PreviousCommand: baseline.CommandID}
	assignmentAssertReceipt(t, service, uri, want, true)
	inputs = append(inputs, uri)
	results = append(results, want)
	if _, err := sup.sup.SupersedeVersion(ctx, sup.command); err != nil {
		t.Fatal(err)
	}
	view = query(uri.EffectiveAt)
	if !view.Superseded || view.Desired.Target != f.target || view.Classification != d.Original || view.Desired.HeadRevision != 8 {
		t.Fatal("supersession changed assignment", view)
	}
	if got, err := service.Execute(ctx, uri); err != nil || got != want {
		t.Fatal("lost-response replay after catalog change", err)
	}
	unassign := f.command(t, 10, app.Unassign, 8)
	record(unassign)
	retire := f.command(t, 11, app.RetireAssignment, 9)
	retire.EffectiveAt = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	record(retire)
	view = query(unassign.EffectiveAt)
	if !view.HeadRetired || view.Desired.State != d.Unassigned || view.Desired.Target != (d.OriginalTarget{}) || view.OriginalTarget != f.target {
		t.Fatal("baseline fallback or premature effective retirement", view)
	}
	if v := query(retire.EffectiveAt); v.Desired.State != d.Retired {
		t.Fatal("retirement boundary", v)
	}
	future := f.command(t, 12, app.RecordClassification, 10)
	if _, err := service.Execute(ctx, future); err != app.ErrRetired {
		t.Fatal("future retirement accepted new review", err)
	}
	historical := uint64(3)
	old, err := service.GetAssignment(ctx, app.EffectiveQuery{TagID: f.tag, At: retire.EffectiveAt, KnowledgeRevision: &historical})
	if err != nil || old.Desired.Target != f.foreign || old.Classification != d.Unknown || old.OriginalState != d.OriginalTargetUnknown {
		t.Fatal("as-known projection used head classification", old, err)
	}
	q := app.HistoryQuery{EffectiveQuery: app.EffectiveQuery{TagID: f.tag, At: retire.EffectiveAt}, Limit: 128}
	before := assignmentHistoryPage(t, service, q, results...)
	cursorQuery := q
	cursorQuery.Limit = 1
	page, err := service.ListAssignmentHistory(ctx, cursorQuery)
	if err != nil || page.Next == "" {
		t.Fatal(err)
	}
	cursorQuery.Cursor = page.Next
	var stale app.Port
	if err := owner.WithinAssignmentTransaction(ctx, func(p app.Port) error { stale = p; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(ctx); err != nil {
		t.Fatal(err)
	}
	restarted, err := OpenLifecycleOwner(ctx, Config{Path: path}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close(context.Background()) })
	service = app.New(restarted)
	assignmentHistoryError(t, service, cursorQuery, app.ErrStaleCursor)
	if got := assignmentHistoryPage(t, service, q, results...); !reflect.DeepEqual(got, before) {
		t.Fatal("restart changed intact history")
	}
	for i, in := range inputs {
		if got, err := service.Execute(ctx, in); err != nil || got != results[i] {
			t.Fatal("restart exact replay", i, err)
		}
	}
	assignmentAssertReceipt(t, service, secondTag, secondResult, true)
	if _, err := stale.Authenticate(ctx); err != app.ErrUnavailable {
		t.Fatal("stale port remained live", err)
	}
	// Restore the exact v10 snapshot: fresh cursor scope, same evidence/receipts.
	backup, err := restarted.database.CreateBackup(ctx, filepath.Join(t.TempDir(), "audit-backup.sqlite"), SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	page, err = service.ListAssignmentHistory(ctx, app.HistoryQuery{EffectiveQuery: q.EffectiveQuery, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	cursorQuery.Cursor = page.Next
	oldDB := restarted.database
	if err := restarted.Restore(ctx, backup, filepath.Join(t.TempDir(), "audit-restored.sqlite")); err != nil {
		t.Fatal(err)
	}
	if _, err := oldDB.CurrentVersion(ctx); err == nil {
		t.Fatal("restore left old DB live")
	}
	assignmentHistoryError(t, service, cursorQuery, app.ErrStaleCursor)
	if got := assignmentHistoryPage(t, service, q, results...); !reflect.DeepEqual(got, before) {
		t.Fatal("restore changed intact history")
	}
	for i, in := range inputs {
		assignmentAssertReceipt(t, service, in, results[i], true)
	}
}
