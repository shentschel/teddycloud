package sqlite

import (
	"context"
	"crypto/sha256"
	"reflect"
	"testing"

	app "github.com/shentschel/teddycloud/next/backend/internal/application/assignment"
	catalogapp "github.com/shentschel/teddycloud/next/backend/internal/application/catalog"
	tagapp "github.com/shentschel/teddycloud/next/backend/internal/application/tagregistry"
	d "github.com/shentschel/teddycloud/next/backend/internal/domain/assignment"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/content"
	"github.com/shentschel/teddycloud/next/backend/internal/domain/evidence"
	domaintag "github.com/shentschel/teddycloud/next/backend/internal/domain/tag"
)

func TestAssignmentLostResponseAfterRestart(t *testing.T) {
	f := newAssignmentRepositoryFixture(t)
	owner, service, path := assignmentActivateOwner(t, f)
	ctx := t.Context()
	legacy := f.command(t, 1, app.Assign, 0)
	first := f.execute(t, legacy)
	class := f.execute(t, f.command(t, 2, app.RecordClassification, 1))
	uri := f.command(t, 3, app.Assign, 2)
	uri.URI = app.OptionalURI{Present: true, Value: "HTTPS://Catalog.Example:443/a%62"}
	want := app.Result{CommandID: uri.CommandID, AssignmentID: f.id, TagID: f.tag, Revision: 3, PreviousCommand: class.CommandID}
	if got, err := assignmentLostResponse(owner).Execute(ctx, uri); got != (app.Result{}) || err != app.ErrIndeterminate {
		t.Fatalf("lost B1 response: %+v, %v", got, err)
	}

	// Move both live revisions past the original dependency fences and bind
	// media metadata. Replay must still return the original retained result.
	tags := tagapp.New(owner)
	tag, err := tags.FindByID(ctx, f.tag.String())
	if err != nil {
		t.Fatal(err)
	}
	const key = domaintag.CloudAuth
	observation := tagMetadataObservation(t, tag, 1000, key, "true")
	tag, err = tags.UpdateMetadata(ctx, tagapp.MetadataCommand{TagID: tag.ID(), Change: domaintag.MetadataChange{
		ExpectedRevision: tag.Revision(), Key: key, Observations: []evidence.Observation{observation},
	}})
	if err != nil || tag.Revision() != 2 {
		t.Fatal("Tag revision did not advance", err)
	}

	upstream := f.upstream
	upstream.owner = owner
	upstream.bind(t)
	sup, err := catalogapp.NewSupersessionService(upstream.base, upstream.cap)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sup.SupersedeVersion(ctx, upstream.command); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("restart media metadata"))
	media, err := content.NewImportCommand(upstream.command.Old, content.NewBlobID(digest), content.MinTAFBytes, content.TAFProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := blobRecord(t, owner, blobKey(t, 1), media); err != nil || got != importResult(media) {
		t.Fatal("binding original version media metadata", err)
	}

	q := app.HistoryQuery{EffectiveQuery: app.EffectiveQuery{TagID: f.tag, At: uri.EffectiveAt}, Limit: 3}
	page := assignmentHistoryPage(t, service, q, first, class, want)
	view, err := service.GetAssignment(ctx, q.EffectiveQuery)
	if err != nil || view.Desired.Target != f.target || view.Classification != d.Original ||
		!view.Superseded || !view.BoundUnchecked || view.CatalogRevision <= uri.ExpectedCatalogRevision {
		t.Fatal("fixture lacks exact historical target and changed live annotations", view, err)
	}
	before := registrationSchemaRows(t, owner.database.db)

	if err := owner.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if got, found, err := service.LookupAssignmentCommand(ctx, uri); got != (app.Result{}) || found || err != app.ErrUnavailable {
		t.Fatalf("old owner lookup: %+v, %t, %v", got, found, err)
	}
	reopened, err := OpenLifecycleOwner(ctx, Config{Path: path}, SchemaMigrations())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })
	service = app.New(reopened)
	assignmentAssertRows(t, reopened.database.db, before)

	absent := uri
	absent.CommandID, _ = d.ParseCommandID("000000000000000000000000000000ff")
	assignmentAssertReceipt(t, service, absent, app.Result{}, false)
	conflict := uri
	conflict.URI.Value = "https://catalog.example/different"
	if got, found, err := service.LookupAssignmentCommand(ctx, conflict); got != (app.Result{}) || found || err != app.ErrCommandConflict {
		t.Fatalf("same-ID conflict lookup: %+v, %t, %v", got, found, err)
	}
	if got, err := service.Execute(ctx, conflict); got != (app.Result{}) || err != app.ErrCommandConflict {
		t.Fatalf("same-ID conflict replay: %+v, %v", got, err)
	}

	uri.URI.Value = "https://catalog.example/ab"
	for _, tc := range []struct {
		in   app.Command
		want app.Result
	}{{legacy, first}, {uri, want}} {
		assignmentAssertReceipt(t, service, tc.in, tc.want, true)
		if got, err := service.Execute(ctx, tc.in); got != tc.want || err != nil {
			t.Fatalf("restart replay: %+v, %v", got, err)
		}
	}
	if after := assignmentHistoryPage(t, service, q, first, class, want); !reflect.DeepEqual(page, after) {
		t.Fatal("replay changed retained events or canonical provenance")
	}
	if after, err := service.GetAssignment(ctx, q.EffectiveQuery); err != nil || !reflect.DeepEqual(view, after) {
		t.Fatal("replay changed assignment, catalog/media annotations or classification", err)
	}
	assignmentAssertRows(t, reopened.database.db, before)
}
